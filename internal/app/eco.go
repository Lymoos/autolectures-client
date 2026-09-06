package app

// Eco решает, рисовать ли кадры трансляции. Внутри лекции они нужны всегда,
// поэтому режим экономии включается либо руками, либо когда картинку всё равно
// никто не видит: окно свёрнуто, открыта другая вкладка или диалог.
type Eco struct {
	sessionActive, windowVisible, previewTab bool
	overlay                                  bool
	manual                                   bool
	lowPower                                 bool
	OnChange                                 func(lowPower bool)
}

func newEco() *Eco { return &Eco{windowVisible: true, previewTab: true, lowPower: true} }

func (e *Eco) SetSessionActive(v bool) {
	e.sessionActive = v
	if !v {
		e.manual = false
	}
	e.recompute()
}
func (e *Eco) SetManual(v bool)        { e.manual = v; e.recompute() }
func (e *Eco) Manual() bool            { return e.manual }
func (e *Eco) SetWindowVisible(v bool) { e.windowVisible = v; e.recompute() }
func (e *Eco) SetPreviewTab(v bool)    { e.previewTab = v; e.recompute() }
func (e *Eco) SetOverlay(v bool)       { e.overlay = v; e.recompute() }
func (e *Eco) LowPower() bool          { return e.lowPower }

func (e *Eco) Reason() string {
	switch {
	case !e.sessionActive:
		return "Сессия не запущена"
	case !e.windowVisible:
		return "Окно свёрнуто — работает только звук и anti-AFK"
	case !e.previewTab:
		return "Открыта другая вкладка"
	case e.overlay:
		return "Открыто диалоговое окно"
	case e.manual:
		return "Эко-режим включён вручную: кадры не рендерятся, звук и anti-AFK работают"
	}
	return ""
}

func (e *Eco) recompute() {
	low := !e.sessionActive || !e.windowVisible || !e.previewTab || e.overlay || e.manual
	if low == e.lowPower {
		return
	}
	e.lowPower = low
	if e.OnChange != nil {
		e.OnChange(low)
	}
}
