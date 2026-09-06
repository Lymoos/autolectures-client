//go:build windows

package app

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/webview"
)

type Call struct {
	Args  map[string]any
	id    float64
	b     *Bridge
	async bool
	done  bool
}

func (c *Call) Async() { c.async = true }

func (c *Call) Reply(result any, err error) {
	c.b.dispatch(func() {
		if c.done {
			return
		}
		c.done = true
		c.b.reply(c.id, result, err)
	})
}

func (c *Call) Str(k string) string    { v, _ := c.Args[k].(string); return v }
func (c *Call) Bool(k string) bool     { v, _ := c.Args[k].(bool); return v }
func (c *Call) Int(k string) int       { v, _ := c.Args[k].(float64); return int(v) }
func (c *Call) Float(k string) float64 { v, _ := c.Args[k].(float64); return v }

type Handler func(c *Call) (any, error)

type Bridge struct {
	view     *webview.View
	dispatch func(func())
	mu       sync.Mutex
	handlers map[string]Handler
	ready    bool
	queued   []string
}

func newBridge(v *webview.View, dispatch func(func())) *Bridge {
	b := &Bridge{view: v, dispatch: dispatch, handlers: map[string]Handler{}}
	v.OnMessage = b.onMessage
	return b
}

func (b *Bridge) Handle(name string, h Handler) { b.handlers[name] = h }

func jsString(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(raw)
}

func (b *Bridge) Emit(event string, payload any) {
	script := "window.AL && AL._event(" + jsString(event) + "," + jsString(payload) + ");"
	if !b.ready {
		b.queued = append(b.queued, script)
		return
	}
	b.view.Eval(script)
}

func (b *Bridge) EmitAsync(event string, payload any) {
	b.dispatch(func() { b.Emit(event, payload) })
}

func (b *Bridge) reply(id float64, result any, err error) {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	b.view.Eval("window.AL && AL._reply(" + jsString(id) + "," + jsString(result) + "," + jsString(errStr) + ");")
}

func (b *Bridge) onMessage(text string) {
	var msg struct {
		ID     float64        `json:"id"`
		Method string         `json:"method"`
		Args   map[string]any `json:"args"`
	}
	if json.Unmarshal([]byte(text), &msg) != nil || msg.Method == "" {
		return
	}
	if msg.Method == "ready" {
		b.ready = true
		for _, s := range b.queued {
			b.view.Eval(s)
		}
		b.queued = nil
	}
	h, ok := b.handlers[msg.Method]
	if !ok {
		b.reply(msg.ID, nil, errors.New("неизвестный метод "+msg.Method))
		return
	}
	call := &Call{Args: msg.Args, id: msg.ID, b: b}
	if call.Args == nil {
		call.Args = map[string]any{}
	}
	result, err := func() (res any, err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("Интерфейс", "Сбой обработчика %s: %v", msg.Method, r)
				err = errors.New("внутренняя ошибка")
			}
		}()
		return h(call)
	}()
	if call.async && err == nil {
		return
	}
	call.done = true
	b.reply(msg.ID, result, err)
}
