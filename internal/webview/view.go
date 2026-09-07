//go:build windows

package webview

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"github.com/jchv/go-webview2/pkg/edge"

	"github.com/Lymoos/autolectures/client/internal/win"
)

type Options struct {
	DataDir     string
	Transparent bool
	DenyMedia   bool
	Kiosk       bool
}

type View struct {
	Chromium *edge.Chromium
	HWnd     uintptr
	visible  bool
	bounds   struct {
		x, y, w, h int32
		set        bool
	}

	OnMessage   func(text string)
	OnNavigated func(ok bool, status uint32)
}

const (
	ErrConnectionAborted = 9
	ErrOperationCanceled = 14
)

func ChromiumFlags(flags string) {
	if os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS") == "" {
		_ = os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", flags)
	}
}

func New(host *win.Window, o Options) (*View, error) {
	v := &View{HWnd: host.CreateChild(), visible: true}
	c := edge.NewChromium()
	c.DataPath = o.DataDir
	c.MessageCallback = func(s string) {
		if v.OnMessage != nil {
			v.OnMessage(s)
		}
	}
	c.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, args *edge.ICoreWebView2NavigationCompletedEventArgs) {
		ok, status := navigationResult(args)
		if v.OnNavigated != nil {
			v.OnNavigated(ok, status)
		}
	}
	if o.DenyMedia {
		c.SetGlobalPermission(edge.CoreWebView2PermissionStateDeny)
	}
	if !c.Embed(v.HWnd) {
		return nil, errors.New("не удалось инициализировать WebView2: установите Microsoft Edge WebView2 Runtime")
	}
	v.Chromium = c

	if s, err := c.GetSettings(); err == nil {
		_ = s.PutIsStatusBarEnabled(false)
		_ = s.PutAreDefaultContextMenusEnabled(!o.Kiosk)
		_ = s.PutIsZoomControlEnabled(!o.Kiosk)
		_ = s.PutIsPinchZoomEnabled(!o.Kiosk)
		_ = s.PutIsSwipeNavigationEnabled(false)
		_ = s.PutAreBrowserAcceleratorKeysEnabled(!o.Kiosk)
		_ = s.PutAreDevToolsEnabled(true)
	}
	if o.Transparent {
		if c2 := c.GetController().GetICoreWebView2Controller2(); c2 != nil {
			_ = c2.PutDefaultBackgroundColor(edge.COREWEBVIEW2_COLOR{A: 0, R: 0, G: 0, B: 0})
		}
	}
	_ = c.GetController().PutIsVisible(true)
	c.Resize()
	return v, nil
}

// Повторные вызовы с теми же координатами пропускаются: во время растягивания
// окна лишние SetWindowPos и Resize дают заметное мигание.
func (v *View) SetBounds(x, y, w, h int32, visible bool) {
	b := &v.bounds
	moved := !b.set || b.x != x || b.y != y || b.w != w || b.h != h
	if !moved && visible == v.visible {
		return
	}
	if moved {
		b.x, b.y, b.w, b.h, b.set = x, y, w, h, true
		win.PlaceChild(v.HWnd, x, y, w, h, visible)
		v.Chromium.Resize()
	}
	if visible != v.visible {
		v.SetVisible(visible)
	}
}

// Порядок важен: WebView2 привязывает свою поверхность к окну, поэтому сначала
// показываем окно и только потом включаем отрисовку — иначе контроллер считает
// себя видимым, а рисовать ему некуда, и на месте трансляции остаётся пустота.
func (v *View) SetVisible(visible bool) {
	if v.visible == visible {
		return
	}
	v.visible = visible
	if visible {
		win.ShowWindow(v.HWnd, win.SW_SHOWNA)
		win.RaiseChild(v.HWnd)
		_ = v.Chromium.GetController().PutIsVisible(true)
		v.Chromium.Resize()
		return
	}
	_ = v.Chromium.GetController().PutIsVisible(false)
	win.ShowWindow(v.HWnd, win.SW_HIDE)
}

func (v *View) Visible() bool { return v.visible }

func (v *View) Navigate(url string)      { v.Chromium.Navigate(url) }
func (v *View) NavigateHTML(html string) { v.Chromium.NavigateToString(html) }
func (v *View) Eval(js string)           { v.Chromium.Eval(js) }
func (v *View) Init(js string)           { v.Chromium.Init(js) }
func (v *View) Focus()                   { v.Chromium.Focus() }
func (v *View) SetUserAgent(ua string) {
	if s, err := v.Chromium.GetSettings(); err == nil {
		_ = s.PutUserAgent(ua)
	}
}

func navigationResult(args *edge.ICoreWebView2NavigationCompletedEventArgs) (bool, uint32) {
	if args == nil {
		return true, 0
	}
	type vtbl struct {
		queryInterface, addRef, release uintptr
		getIsSuccess                    uintptr
		getWebErrorStatus               uintptr
	}
	vt := *(**vtbl)(unsafe.Pointer(args))
	var success int32
	if r, _, _ := syscall.SyscallN(vt.getIsSuccess, uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&success))); r != 0 {
		return true, 0
	}
	var status uint32
	_, _, _ = syscall.SyscallN(vt.getWebErrorStatus, uintptr(unsafe.Pointer(args)), uintptr(unsafe.Pointer(&status)))
	return success != 0, status
}
