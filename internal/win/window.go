//go:build windows

package win

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type Window struct {
	HWnd     uintptr
	hostCls  *uint16
	childCls *uint16
	icon     uintptr
	border   int32

	transparent bool
	splash      splashState

	queueMu sync.Mutex
	queue   []func()

	trayAdded bool
	menuItems []menuItem

	OnResize   func(w, h int32)
	OnClose    func() bool
	OnTray     func()
	OnMenu     func(id int)
	OnDpi      func(dpi int)
	OnActivate func(active bool)
	OnMinimize func(minimized bool)
}

type menuItem struct {
	id    int
	title string
}

var wndProcCb = syscall.NewCallback(wndProc)

var registry = map[uintptr]*Window{}
var registryMu sync.Mutex

func wndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	registryMu.Lock()
	w := registry[hwnd]
	registryMu.Unlock()
	if w == nil {
		return DefWindowProc(hwnd, msg, wp, lp)
	}
	return w.handle(msg, wp, lp)
}

func New(title string, width, height int32, iconPNG []byte) *Window {
	runtime.LockOSThread()
	w := &Window{border: 6}
	inst := GetModuleHandle()
	cursor, _, _ := pLoadCursorW.Call(0, IDC_ARROW)
	w.icon = IconFromPNG(iconPNG, 32)

	w.hostCls = utf16("AutolecturesHost")
	host := WNDCLASSEXW{
		Style: CS_DBLCLKS, LpfnWndProc: wndProcCb, HInstance: inst,
		HIcon: w.icon, HIconSm: w.icon, HCursor: cursor, LpszClassName: w.hostCls,
	}
	host.CbSize = uint32(unsafe.Sizeof(host))
	_, _, _ = pRegisterClassExW.Call(uintptr(unsafe.Pointer(&host)))

	w.childCls = utf16("AutolecturesChild")
	child := WNDCLASSEXW{LpfnWndProc: wndProcCb, HInstance: inst, HCursor: cursor, LpszClassName: w.childCls}
	child.CbSize = uint32(unsafe.Sizeof(child))
	_, _, _ = pRegisterClassExW.Call(uintptr(unsafe.Pointer(&child)))

	style := uintptr(WS_POPUP | WS_THICKFRAME | WS_SYSMENU | WS_MINIMIZEBOX | WS_MAXIMIZEBOX | WS_CLIPCHILDREN)
	work := WorkArea(0)
	x := work.Left + (work.Width()-width)/2
	y := work.Top + (work.Height()-height)/2
	hwnd, _, _ := pCreateWindowExW.Call(WS_EX_APPWINDOW, uintptr(unsafe.Pointer(w.hostCls)), uintptr(unsafe.Pointer(utf16(title))),
		style, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 0, 0, inst, 0)
	w.HWnd = hwnd
	registryMu.Lock()
	registry[hwnd] = w
	registryMu.Unlock()
	return w
}

func (w *Window) ApplyChrome(transparent bool) bool {
	DwmSet(w.HWnd, DWMWA_USE_IMMERSIVE_DARK_MODE, 1)
	DwmSet(w.HWnd, DWMWA_WINDOW_CORNER_PREFERENCE, DWMWCP_ROUND)
	if transparent {
		DwmExtendFrame(w.HWnd, MARGINS{-1, -1, -1, -1})
		if DwmSet(w.HWnd, DWMWA_SYSTEMBACKDROP_TYPE, DWMSBT_TRANSIENTWINDOW) {
			w.transparent = true
			return true
		}
		DwmExtendFrame(w.HWnd, MARGINS{1, 1, 1, 1})
	}
	w.transparent = false
	DwmSet(w.HWnd, DWMWA_SYSTEMBACKDROP_TYPE, DWMSBT_NONE)
	DwmExtendFrame(w.HWnd, MARGINS{1, 1, 1, 1})
	return false
}

func (w *Window) CreateChild() uintptr {
	h, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(w.childCls)), 0,
		WS_CHILD|WS_VISIBLE|WS_CLIPSIBLINGS|WS_CLIPCHILDREN, 0, 0, 10, 10, w.HWnd, 0, GetModuleHandle(), 0)
	return h
}

func PlaceChild(child uintptr, x, y, width, height int32, visible bool) {
	flags := uint32(SWP_NOACTIVATE | SWP_NOZORDER)
	if visible {
		flags |= SWP_SHOWWINDOW
	} else {
		flags |= SWP_HIDEWINDOW
	}
	SetWindowPos(child, 0, x, y, width, height, flags)
}

// RaiseChild поднимает окно над соседями: дочерние окна создаются под уже
// существующими, поэтому показываемое поверх интерфейса окно нужно поднять.
func RaiseChild(child uintptr) {
	SetWindowPos(child, HWND_TOP, 0, 0, 0, 0, SWP_NOMOVE|SWP_NOSIZE|SWP_NOACTIVATE)
}

func (w *Window) Border() int32 {
	if IsZoomed(w.HWnd) {
		return 0
	}
	return w.border
}

func (w *Window) Show()     { ShowWindow(w.HWnd, SW_SHOW); SetForegroundWindow(w.HWnd) }
func (w *Window) Hide()     { ShowWindow(w.HWnd, SW_HIDE) }
func (w *Window) Minimize() { ShowWindow(w.HWnd, SW_MINIMIZE) }
func (w *Window) Restore() {
	ShowWindow(w.HWnd, SW_RESTORE)
	SetForegroundWindow(w.HWnd)
}
func (w *Window) ToggleMaximize() {
	if IsZoomed(w.HWnd) {
		SendMessage(w.HWnd, WM_SYSCOMMAND, SC_RESTORE, 0)
	} else {
		SendMessage(w.HWnd, WM_SYSCOMMAND, SC_MAXIMIZE, 0)
	}
}
func (w *Window) Visible() bool   { return IsWindowVisible(w.HWnd) && !IsIconic(w.HWnd) }
func (w *Window) Minimized() bool { return IsIconic(w.HWnd) }

func (w *Window) BeginDrag() {
	ReleaseCapture()
	SendMessage(w.HWnd, WM_NCLBUTTONDOWN, HTCAPTION, 0)
}

func (w *Window) Close() { PostMessage(w.HWnd, WM_CLOSE, 0, 0) }

func (w *Window) Redraw() {
	_, _, _ = pInvalidateRect.Call(w.HWnd, 0, 1)
	_, _, _ = pUpdateWindow.Call(w.HWnd)
}

// WM_DESTROY только просил цикл сообщений завершиться, само окно оставалось на
// экране до конца выхода — при обновлении это выглядело как зависший «Перезапуск…».
func (w *Window) Quit() { PostMessage(w.HWnd, WM_CLOSE, 0, 0) }

func (w *Window) Dispatch(fn func()) {
	w.queueMu.Lock()
	w.queue = append(w.queue, fn)
	w.queueMu.Unlock()
	PostMessage(w.HWnd, WM_APP_DISPATCH, 0, 0)
}

func (w *Window) Run() {
	var msg MSG
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		_, _, _ = pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (w *Window) handle(msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_APP_DISPATCH:
		w.queueMu.Lock()
		q := w.queue
		w.queue = nil
		w.queueMu.Unlock()
		for _, fn := range q {
			fn()
		}
		return 0

	case WM_NCCALCSIZE:
		if wp != 0 {
			if IsZoomed(w.HWnd) {
				p := (*NCCALCSIZE_PARAMS)(unsafe.Pointer(lp))
				frame := GetSystemMetrics(SM_CXSIZEFRAME) + GetSystemMetrics(SM_CXPADDEDBORDER)
				p.Rgrc[0].Left += frame
				p.Rgrc[0].Top += frame
				p.Rgrc[0].Right -= frame
				p.Rgrc[0].Bottom -= frame
			}
			return 0
		}

	case WM_NCHITTEST:
		if IsZoomed(w.HWnd) {
			return HTCLIENT
		}
		r := GetWindowRect(w.HWnd)
		x, y := loword(lp), hiword(lp)
		b := w.border
		left, right := x < r.Left+b, x >= r.Right-b
		top, bottom := y < r.Top+b, y >= r.Bottom-b
		switch {
		case top && left:
			return HTTOPLEFT
		case top && right:
			return HTTOPRIGHT
		case bottom && left:
			return HTBOTTOMLEFT
		case bottom && right:
			return HTBOTTOMRIGHT
		case left:
			return HTLEFT
		case right:
			return HTRIGHT
		case top:
			return HTTOP
		case bottom:
			return HTBOTTOM
		}
		return HTCLIENT

	case WM_GETMINMAXINFO:
		mmi := (*MINMAXINFO)(unsafe.Pointer(lp))
		mmi.MinTrackSize = POINT{980, 620}
		return 0

	case WM_ERASEBKGND:
		if w.splash.active {
			w.paintSplash(wp)
			return 1
		}
		// Чёрный в расширенной рамке DWM — это прозрачность: так возвращается
		// системный фон после экрана подготовки, а не остаются старые пиксели.
		if w.transparent {
			FillClient(w.HWnd, wp, 0x000000)
		} else {
			FillClient(w.HWnd, wp, 0x141414)
		}
		return 1

	case WM_SIZE:
		if w.OnMinimize != nil {
			w.OnMinimize(wp == 1)
		}
		if wp != 1 {
			if w.splash.active {
				w.RedrawSplash()
			}
			if w.OnResize != nil {
				w.OnResize(loword(lp), hiword(lp))
			}
		}
		return 0

	case WM_ACTIVATE:
		if w.OnActivate != nil {
			w.OnActivate(loword(wp) != 0)
		}

	case WM_NCACTIVATE:
		return DefWindowProc(w.HWnd, msg, wp, ^uintptr(0))

	case WM_DPICHANGED:
		r := (*RECT)(unsafe.Pointer(lp))
		SetWindowPos(w.HWnd, 0, r.Left, r.Top, r.Width(), r.Height(), SWP_NOZORDER|SWP_NOACTIVATE)
		if w.OnDpi != nil {
			w.OnDpi(int(loword(wp)))
		}
		return 0

	case WM_APP_TRAY:
		switch uint32(lp & 0xFFFF) {
		case WM_LBUTTONUP, WM_LBUTTONDBLCLK:
			if w.OnTray != nil {
				w.OnTray()
			}
		case WM_RBUTTONUP, WM_CONTEXTMENU:
			w.showTrayMenu()
		}
		return 0

	case WM_COMMAND:
		if w.OnMenu != nil {
			w.OnMenu(int(loword(wp)))
		}
		return 0

	case WM_CLOSE:
		if w.OnClose == nil || w.OnClose() {
			w.RemoveTray()
			DestroyWindow(w.HWnd)
		}
		return 0

	case WM_DESTROY:
		w.RemoveTray()
		_, _, _ = pPostQuitMessage.Call(0)
		return 0
	}
	return DefWindowProc(w.HWnd, msg, wp, lp)
}

func (w *Window) trayData(tip string) NOTIFYICONDATA {
	var d NOTIFYICONDATA
	d.CbSize = uint32(unsafe.Sizeof(d))
	d.HWnd = w.HWnd
	d.UID = 1
	d.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
	d.UCallbackMessage = WM_APP_TRAY
	d.HIcon = w.icon
	u, _ := syscall.UTF16FromString(tip)
	copy(d.SzTip[:], u)
	return d
}

func (w *Window) AddTray(tip string, items []struct {
	ID    int
	Title string
}) {
	w.menuItems = nil
	for _, it := range items {
		w.menuItems = append(w.menuItems, menuItem{it.ID, it.Title})
	}
	d := w.trayData(tip)
	op := uintptr(NIM_ADD)
	if w.trayAdded {
		op = NIM_MODIFY
	}
	_, _, _ = pShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&d)))
	w.trayAdded = true
}

func (w *Window) SetTrayItemTitle(id int, title string) {
	for i := range w.menuItems {
		if w.menuItems[i].id == id {
			w.menuItems[i].title = title
		}
	}
}

func (w *Window) RemoveTray() {
	if !w.trayAdded {
		return
	}
	d := w.trayData("")
	_, _, _ = pShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&d)))
	w.trayAdded = false
}

func (w *Window) showTrayMenu() {
	menu, _, _ := pCreatePopupMenu.Call()
	for _, it := range w.menuItems {
		if it.id == 0 {
			_, _, _ = pAppendMenuW.Call(menu, MF_SEPARATOR, 0, 0)
			continue
		}
		_, _, _ = pAppendMenuW.Call(menu, MF_STRING, uintptr(it.id), uintptr(unsafe.Pointer(utf16(it.title))))
	}
	var pt POINT
	_, _, _ = pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	SetForegroundWindow(w.HWnd)
	_, _, _ = pTrackPopupMenu.Call(menu, TPM_RIGHTBUTTON|TPM_BOTTOMALIGN, uintptr(pt.X), uintptr(pt.Y), 0, w.HWnd, 0)
	PostMessage(w.HWnd, WM_NULL, 0, 0)
	_, _, _ = pDestroyMenu.Call(menu)
}
