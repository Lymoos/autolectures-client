//go:build windows

package win

import (
	"syscall"
	"unsafe"
)

var (
	pGetDC           = user32.NewProc("GetDC")
	pReleaseDC       = user32.NewProc("ReleaseDC")
	pDrawTextW       = user32.NewProc("DrawTextW")
	pCreateFontW     = gdi32.NewProc("CreateFontW")
	pSelectObject    = gdi32.NewProc("SelectObject")
	pSetTextColor    = gdi32.NewProc("SetTextColor")
	pSetBkMode       = gdi32.NewProc("SetBkMode")
	pCreateCompatDC  = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatBmp = gdi32.NewProc("CreateCompatibleBitmap")
	pBitBlt          = gdi32.NewProc("BitBlt")
	pDeleteDC        = gdi32.NewProc("DeleteDC")
	pRoundRect       = gdi32.NewProc("RoundRect")
	pCreatePen       = gdi32.NewProc("CreatePen")
	pGetStockObject  = gdi32.NewProc("GetStockObject")
)

const (
	dtCenter     = 0x0001
	dtVCenter    = 0x0004
	dtSingleLine = 0x0020
	dtWordBreak  = 0x0010
	transparent  = 1
	srcCopy      = 0x00CC0020
	nullPen      = 8
	psSolid      = 0
)

type splashState struct {
	active  bool
	title   string
	text    string
	percent int
	phase   int
}

// SetSplash рисует экран подготовки прямо в окне: WebView2 в этот момент может
// быть ещё не установлен, а сообщение о прогрессе нужно показать сразу.
func (w *Window) SetSplash(title, text string, percent int) {
	w.splash.active = true
	w.splash.title, w.splash.text, w.splash.percent = title, text, percent
	w.splash.phase++
	w.RedrawSplash()
}

func (w *Window) SplashActive() bool { return w.splash.active }

func (w *Window) HideSplash() {
	if !w.splash.active {
		return
	}
	w.splash.active = false
}

func (w *Window) RedrawSplash() {
	if !w.splash.active {
		return
	}
	hdc, _, _ := pGetDC.Call(w.HWnd)
	if hdc == 0 {
		return
	}
	w.paintSplash(hdc)
	_, _, _ = pReleaseDC.Call(w.HWnd, hdc)
}

func makeFont(dpi int, size int32, weight int32) uintptr {
	h := -(size * int32(dpi) / 72)
	f, _, _ := pCreateFontW.Call(uintptr(h), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(utf16("Segoe UI"))))
	return f
}

func fillRect(hdc uintptr, r RECT, colorBGR uint32) {
	brush, _, _ := pCreateSolidBrush.Call(uintptr(colorBGR))
	if brush == 0 {
		return
	}
	_, _, _ = pFillRect.Call(hdc, uintptr(unsafe.Pointer(&r)), brush)
	_, _, _ = pDeleteObject.Call(brush)
}

func roundRect(hdc uintptr, r RECT, radius int32, colorBGR uint32) {
	brush, _, _ := pCreateSolidBrush.Call(uintptr(colorBGR))
	pen, _, _ := pCreatePen.Call(psSolid, 1, uintptr(colorBGR))
	oldBrush, _, _ := pSelectObject.Call(hdc, brush)
	oldPen, _, _ := pSelectObject.Call(hdc, pen)
	_, _, _ = pRoundRect.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(r.Right), uintptr(r.Bottom),
		uintptr(radius), uintptr(radius))
	_, _, _ = pSelectObject.Call(hdc, oldBrush)
	_, _, _ = pSelectObject.Call(hdc, oldPen)
	_, _, _ = pDeleteObject.Call(brush)
	_, _, _ = pDeleteObject.Call(pen)
}

func drawText(hdc uintptr, text string, r RECT, font uintptr, colorBGR uint32, flags uint32) {
	old, _, _ := pSelectObject.Call(hdc, font)
	_, _, _ = pSetBkMode.Call(hdc, transparent)
	_, _, _ = pSetTextColor.Call(hdc, uintptr(colorBGR))
	u, err := syscall.UTF16FromString(text)
	if err != nil {
		u = []uint16{0}
	}
	_, _, _ = pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&u[0])), ^uintptr(0),
		uintptr(unsafe.Pointer(&r)), uintptr(flags))
	_, _, _ = pSelectObject.Call(hdc, old)
}

// Цвета в COLORREF (0x00BBGGRR).
const (
	splashBack   = 0x1B1815
	splashCard   = 0x231F1B
	splashAccent = 0xF7A27A
	splashTitle  = 0xF0EDEA
	splashMuted  = 0x9A928A
	splashTrack  = 0x322D28
)

func (w *Window) paintSplash(target uintptr) {
	client := GetClientRect(w.HWnd)
	cw, ch := client.Width(), client.Height()
	if cw <= 0 || ch <= 0 {
		return
	}
	// Рисуем в память и переносим одним блитом, иначе экран мигает.
	mem, _, _ := pCreateCompatDC.Call(target)
	if mem == 0 {
		return
	}
	bmp, _, _ := pCreateCompatBmp.Call(target, uintptr(cw), uintptr(ch))
	if bmp == 0 {
		_, _, _ = pDeleteDC.Call(mem)
		return
	}
	oldBmp, _, _ := pSelectObject.Call(mem, bmp)
	stockNull, _, _ := pGetStockObject.Call(nullPen)
	_ = stockNull

	dpi := DpiForWindow(w.HWnd)
	sc := func(v int32) int32 { return v * int32(dpi) / 96 }

	fillRect(mem, RECT{0, 0, cw, ch}, splashBack)

	cardW, cardH := sc(460), sc(220)
	if cardW > cw-sc(40) {
		cardW = cw - sc(40)
	}
	cx, cy := (cw-cardW)/2, (ch-cardH)/2
	roundRect(mem, RECT{cx, cy, cx + cardW, cy + cardH}, sc(18), splashCard)

	dotR := sc(22)
	roundRect(mem, RECT{cx + cardW/2 - dotR, cy + sc(28), cx + cardW/2 + dotR, cy + sc(28) + 2*dotR}, 2*dotR, splashAccent)

	fTitle := makeFont(dpi, 17, 600)
	fText := makeFont(dpi, 10, 400)
	defer pDeleteObject.Call(fTitle)
	defer pDeleteObject.Call(fText)

	titleTop := cy + sc(28) + 2*dotR + sc(14)
	drawText(mem, w.splash.title, RECT{cx + sc(24), titleTop, cx + cardW - sc(24), titleTop + sc(30)},
		fTitle, splashTitle, dtCenter|dtSingleLine|dtVCenter)
	textTop := titleTop + sc(30)
	drawText(mem, w.splash.text, RECT{cx + sc(24), textTop, cx + cardW - sc(24), textTop + sc(40)},
		fText, splashMuted, dtCenter|dtWordBreak)

	barH := sc(6)
	barL, barR := cx+sc(34), cx+cardW-sc(34)
	barTop := cy + cardH - sc(34)
	roundRect(mem, RECT{barL, barTop, barR, barTop + barH}, barH, splashTrack)
	full := barR - barL
	switch p := w.splash.percent; {
	case p >= 0:
		if p > 100 {
			p = 100
		}
		if wdt := full * int32(p) / 100; wdt > barH {
			roundRect(mem, RECT{barL, barTop, barL + wdt, barTop + barH}, barH, splashAccent)
		}
	default:
		seg := full / 3
		off := int32(w.splash.phase*7) % (full + seg)
		l, r := barL+off-seg, barL+off
		if l < barL {
			l = barL
		}
		if r > barR {
			r = barR
		}
		if r-l > barH {
			roundRect(mem, RECT{l, barTop, r, barTop + barH}, barH, splashAccent)
		}
	}

	_, _, _ = pBitBlt.Call(target, 0, 0, uintptr(cw), uintptr(ch), mem, 0, 0, srcCopy)
	_, _, _ = pSelectObject.Call(mem, oldBmp)
	_, _, _ = pDeleteObject.Call(bmp)
	_, _, _ = pDeleteDC.Call(mem)
}
