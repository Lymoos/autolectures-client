//go:build windows

package win

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pSendMessageW                  = user32.NewProc("SendMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pDestroyWindow                 = user32.NewProc("DestroyWindow")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pGetWindowRect                 = user32.NewProc("GetWindowRect")
	pRegisterHotKey                = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey              = user32.NewProc("UnregisterHotKey")
	pReleaseCapture                = user32.NewProc("ReleaseCapture")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pSetForegroundWindow           = user32.NewProc("SetForegroundWindow")
	pIsIconic                      = user32.NewProc("IsIconic")
	pIsZoomed                      = user32.NewProc("IsZoomed")
	pIsWindowVisible               = user32.NewProc("IsWindowVisible")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pCreatePopupMenu               = user32.NewProc("CreatePopupMenu")
	pAppendMenuW                   = user32.NewProc("AppendMenuW")
	pTrackPopupMenu                = user32.NewProc("TrackPopupMenu")
	pDestroyMenu                   = user32.NewProc("DestroyMenu")
	pGetCursorPos                  = user32.NewProc("GetCursorPos")
	pMonitorFromWindow             = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	pGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	pCreateIconFromResourceEx      = user32.NewProc("CreateIconFromResourceEx")
	pSetFocus                      = user32.NewProc("SetFocus")
	pOpenClipboard                 = user32.NewProc("OpenClipboard")
	pCloseClipboard                = user32.NewProc("CloseClipboard")
	pEmptyClipboard                = user32.NewProc("EmptyClipboard")
	pSetClipboardData              = user32.NewProc("SetClipboardData")
	pGetModuleHandleW              = kernel32.NewProc("GetModuleHandleW")
	pGlobalAlloc                   = kernel32.NewProc("GlobalAlloc")
	pGlobalLock                    = kernel32.NewProc("GlobalLock")
	pGlobalUnlock                  = kernel32.NewProc("GlobalUnlock")
	pShellNotifyIconW              = shell32.NewProc("Shell_NotifyIconW")
	pShellExecuteW                 = shell32.NewProc("ShellExecuteW")
	pDwmSetWindowAttribute         = dwmapi.NewProc("DwmSetWindowAttribute")
	pDwmExtendFrameIntoClientArea  = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	pFillRect                      = user32.NewProc("FillRect")
	pMessageBoxW                   = user32.NewProc("MessageBoxW")
	pCreateSolidBrush              = gdi32.NewProc("CreateSolidBrush")
	pDeleteObject                  = gdi32.NewProc("DeleteObject")
)


const (
	WS_POPUP        = 0x80000000
	WS_CHILD        = 0x40000000
	WS_VISIBLE      = 0x10000000
	WS_CLIPSIBLINGS = 0x04000000
	WS_CLIPCHILDREN = 0x02000000
	WS_THICKFRAME   = 0x00040000
	WS_SYSMENU      = 0x00080000
	WS_MINIMIZEBOX  = 0x00020000
	WS_MAXIMIZEBOX  = 0x00010000
	WS_EX_APPWINDOW = 0x00040000

	WM_DESTROY       = 0x0002
	WM_SIZE          = 0x0005
	WM_ACTIVATE      = 0x0006
	WM_NCACTIVATE    = 0x0086
	WM_SETFOCUS      = 0x0007
	WM_CLOSE         = 0x0010
	WM_ERASEBKGND    = 0x0014
	WM_GETMINMAXINFO = 0x0024
	WM_NCCALCSIZE    = 0x0083
	WM_NCHITTEST     = 0x0084
	WM_NCLBUTTONDOWN = 0x00A1
	WM_COMMAND       = 0x0111
	WM_SYSCOMMAND    = 0x0112
	WM_HOTKEY        = 0x0312
	WM_LBUTTONUP     = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP     = 0x0205
	WM_CONTEXTMENU   = 0x007B
	WM_DPICHANGED    = 0x02E0
	WM_NULL          = 0x0000
	WM_APP           = 0x8000
	WM_APP_DISPATCH  = WM_APP + 1
	WM_APP_TRAY      = WM_APP + 2

	SC_MINIMIZE = 0xF020
	SC_MAXIMIZE = 0xF030
	SC_RESTORE  = 0xF120

	HTCLIENT      = 1
	HTCAPTION     = 2
	HTLEFT        = 10
	HTRIGHT       = 11
	HTTOP         = 12
	HTTOPLEFT     = 13
	HTTOPRIGHT    = 14
	HTBOTTOM      = 15
	HTBOTTOMLEFT  = 16
	HTBOTTOMRIGHT = 17

	SW_HIDE     = 0
	SW_SHOW     = 5
	SW_MINIMIZE = 6
	SW_RESTORE  = 9
	SW_SHOWNA   = 8

	SWP_NOZORDER   = 0x0004
	SWP_NOACTIVATE = 0x0010
	SWP_SHOWWINDOW = 0x0040
	SWP_NOCOPYBITS = 0x0100
	HWND_TOP       = 0

	SM_CXSIZEFRAME    = 32
	SM_CXPADDEDBORDER = 92

	MOD_ALT      = 0x0001
	MOD_CONTROL  = 0x0002
	MOD_SHIFT    = 0x0004
	MOD_WIN      = 0x0008
	MOD_NOREPEAT = 0x4000

	MF_STRING       = 0x0000
	MF_SEPARATOR    = 0x0800
	TPM_RIGHTBUTTON = 0x0002
	TPM_BOTTOMALIGN = 0x0020

	NIM_ADD     = 0
	NIM_MODIFY  = 1
	NIM_DELETE  = 2
	NIF_MESSAGE = 0x01
	NIF_ICON    = 0x02
	NIF_TIP     = 0x04

	MONITOR_DEFAULTTONEAREST = 2
	CF_UNICODETEXT           = 13
	GMEM_MOVEABLE            = 0x0002
	IDC_ARROW                = 32512
	CS_HREDRAW               = 0x0002
	CS_VREDRAW               = 0x0001
	CS_DBLCLKS               = 0x0008

	DWMWA_USE_IMMERSIVE_DARK_MODE  = 20
	DWMWA_WINDOW_CORNER_PREFERENCE = 33
	DWMWA_SYSTEMBACKDROP_TYPE      = 38
	DWMWCP_ROUND                   = 2
	DWMSBT_MAINWINDOW              = 2
	DWMSBT_TRANSIENTWINDOW         = 3
)

type POINT struct{ X, Y int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }

func (r RECT) Width() int32  { return r.Right - r.Left }
func (r RECT) Height() int32 { return r.Bottom - r.Top }

type MSG struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type MINMAXINFO struct {
	Reserved     POINT
	MaxSize      POINT
	MaxPosition  POINT
	MinTrackSize POINT
	MaxTrackSize POINT
}

type NCCALCSIZE_PARAMS struct {
	Rgrc  [3]RECT
	Lppos uintptr
}

type MONITORINFO struct {
	CbSize  uint32
	Monitor RECT
	Work    RECT
	Flags   uint32
}

type MARGINS struct{ Left, Right, Top, Bottom int32 }

type NOTIFYICONDATA struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

func utf16(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func loword(v uintptr) int32 { return int32(int16(v & 0xFFFF)) }
func hiword(v uintptr) int32 { return int32(int16((v >> 16) & 0xFFFF)) }

func GetModuleHandle() uintptr { h, _, _ := pGetModuleHandleW.Call(0); return h }

func DefWindowProc(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), w, l)
	return r
}

func PostMessage(hwnd uintptr, msg uint32, w, l uintptr) {
	_, _, _ = pPostMessageW.Call(hwnd, uintptr(msg), w, l)
}

func SendMessage(hwnd uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pSendMessageW.Call(hwnd, uintptr(msg), w, l)
	return r
}

func ShowWindow(hwnd uintptr, cmd int) { _, _, _ = pShowWindow.Call(hwnd, uintptr(cmd)) }
func DestroyWindow(hwnd uintptr)       { _, _, _ = pDestroyWindow.Call(hwnd) }
func SetForegroundWindow(hwnd uintptr) { _, _, _ = pSetForegroundWindow.Call(hwnd) }
func SetFocus(hwnd uintptr)            { _, _, _ = pSetFocus.Call(hwnd) }
func ReleaseCapture()                  { _, _, _ = pReleaseCapture.Call() }
func IsIconic(hwnd uintptr) bool       { r, _, _ := pIsIconic.Call(hwnd); return r != 0 }
func IsZoomed(hwnd uintptr) bool       { r, _, _ := pIsZoomed.Call(hwnd); return r != 0 }
func IsWindowVisible(hwnd uintptr) bool {
	r, _, _ := pIsWindowVisible.Call(hwnd)
	return r != 0
}

func SetWindowPos(hwnd, after uintptr, x, y, w, h int32, flags uint32) {
	_, _, _ = pSetWindowPos.Call(hwnd, after, uintptr(x), uintptr(y), uintptr(w), uintptr(h), uintptr(flags))
}

func GetClientRect(hwnd uintptr) RECT {
	var r RECT
	_, _, _ = pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func GetWindowRect(hwnd uintptr) RECT {
	var r RECT
	_, _, _ = pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func GetSystemMetrics(index int) int32 {
	r, _, _ := pGetSystemMetrics.Call(uintptr(index))
	return int32(r)
}


func DpiForWindow(hwnd uintptr) int {
	if pGetDpiForWindow.Find() != nil {
		return 96
	}
	r, _, _ := pGetDpiForWindow.Call(hwnd)
	if r == 0 {
		return 96
	}
	return int(r)
}


func EnablePerMonitorDPI() {
	if pSetProcessDpiAwarenessContext.Find() == nil {
		const perMonitorV2 = ^uintptr(3)
		_, _, _ = pSetProcessDpiAwarenessContext.Call(perMonitorV2)
	}
}


func WorkArea(hwnd uintptr) RECT {
	m, _, _ := pMonitorFromWindow.Call(hwnd, MONITOR_DEFAULTTONEAREST)
	var mi MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	_, _, _ = pGetMonitorInfoW.Call(m, uintptr(unsafe.Pointer(&mi)))
	return mi.Work
}


func DwmSet(hwnd uintptr, attr uint32, value int32) bool {
	r, _, _ := pDwmSetWindowAttribute.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&value)), 4)
	return r == 0
}


func DwmExtendFrame(hwnd uintptr, m MARGINS) {
	_, _, _ = pDwmExtendFrameIntoClientArea.Call(hwnd, uintptr(unsafe.Pointer(&m)))
}


func IconFromPNG(png []byte, size int32) uintptr {
	if len(png) == 0 {
		return 0
	}
	h, _, _ := pCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&png[0])), uintptr(len(png)), 1, 0x00030000,
		uintptr(size), uintptr(size), 0)
	return h
}


func OpenURL(url string) {
	_, _, _ = pShellExecuteW.Call(0, uintptr(unsafe.Pointer(utf16("open"))), uintptr(unsafe.Pointer(utf16(url))), 0, 0, SW_SHOW)
}


func SetClipboardText(hwnd uintptr, text string) bool {
	u, err := syscall.UTF16FromString(text)
	if err != nil {
		return false
	}
	if r, _, _ := pOpenClipboard.Call(hwnd); r == 0 {
		return false
	}
	defer pCloseClipboard.Call()
	_, _, _ = pEmptyClipboard.Call()
	size := uintptr(len(u) * 2)
	h, _, _ := pGlobalAlloc.Call(GMEM_MOVEABLE, size)
	if h == 0 {
		return false
	}
	p, _, _ := pGlobalLock.Call(h)
	if p == 0 {
		return false
	}
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	_, _, _ = pGlobalUnlock.Call(h)
	r, _, _ := pSetClipboardData.Call(CF_UNICODETEXT, h)
	return r != 0
}


func FillClient(hwnd, hdc uintptr, colorBGR uint32) {
	brush, _, _ := pCreateSolidBrush.Call(uintptr(colorBGR))
	if brush == 0 {
		return
	}
	r := GetClientRect(hwnd)
	_, _, _ = pFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), brush)
	_, _, _ = pDeleteObject.Call(brush)
}


func RegisterHotKey(hwnd uintptr, id int, mods, vk uint32) bool {
	r, _, _ := pRegisterHotKey.Call(hwnd, uintptr(id), uintptr(mods), uintptr(vk))
	return r != 0
}
func UnregisterHotKey(hwnd uintptr, id int) { _, _, _ = pUnregisterHotKey.Call(hwnd, uintptr(id)) }


func MessageBox(hwnd uintptr, title, text string) {
	_, _, _ = pMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(utf16(text))), uintptr(unsafe.Pointer(utf16(title))), 0x30)
}

