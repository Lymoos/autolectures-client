package app

type Eco struct {
	enabled, sessionActive, userEntered, windowVisible, previewTab bool
	overlay                                                        bool
	lowPower                                                       bool
	OnChange                                                       func(lowPower bool)
}

func newEco() *Eco { return &Eco{enabled: true, windowVisible: true, previewTab: true, lowPower: true} }

func (e *Eco) SetEnabled(v bool) { e.enabled = v; e.recompute() }
func (e *Eco) SetSessionActive(v bool) {
	e.sessionActive = v
	if !v {
		e.userEntered = false
	}
	e.recompute()
}
func (e *Eco) SetUserEntered(v bool)   { e.userEntered = v; e.recompute() }
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
	case !e.userEntered:
		return "Эко-режим: кадры не рендерятся до входа в лекцию"
	}
	return ""
}

func (e *Eco) recompute() {
	var low bool
	if !e.enabled {
		low = !e.windowVisible || !e.previewTab || e.overlay
	} else {
		low = !e.sessionActive || !e.windowVisible || !e.previewTab || e.overlay || !e.userEntered
	}
	if low == e.lowPower {
		return
	}
	e.lowPower = low
	if e.OnChange != nil {
		e.OnChange(low)
	}
}

