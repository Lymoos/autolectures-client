package hub

import (
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/Lymoos/autolectures/client/internal/config"
	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/proto"
)

const src = "Хаб"

var backoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second}

type Hub struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	cancel  context.CancelFunc
	running bool
	authed  bool
	version string

	OnConnection func(online bool)
	OnAuthFailed func(reason string)
	OnCommand    func(msg map[string]any)
	OnSettings   func(settings map[string]any)
}

func New(version string) *Hub { return &Hub{version: version} }

func (h *Hub) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.authed
}

func (h *Hub) Start() {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return
	}
	h.running = true
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.mu.Unlock()
	go h.run(ctx)
}

func (h *Hub) Stop() {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		return
	}
	h.running = false
	cancel := h.cancel
	conn := h.conn
	was := h.authed
	h.authed = false
	h.conn = nil
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "stop")
	}
	if was && h.OnConnection != nil {
		h.OnConnection(false)
	}
}

func (h *Hub) run(ctx context.Context) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		url, err := config.Get().WsURL()
		if err != nil {
			logger.Errorf(src, "%v", err)
			return
		}
		logger.Infof(src, "Подключение к %s", url)
		ok, fatal := h.session(ctx, url)
		if ok {
			attempt = 0
		}
		// Сервер отклонил токен: без нового входа переподключаться бессмысленно.
		if fatal {
			h.Stop()
			return
		}
		if ctx.Err() != nil {
			return
		}
		d := backoff[min(attempt, len(backoff)-1)] + time.Duration(rand.Intn(500))*time.Millisecond
		if attempt < len(backoff)-1 {
			attempt++
		}
		logger.Infof(src, "Повторное подключение через %.1f с", d.Seconds())
		select {
		case <-ctx.Done():
			return
		case <-time.After(d):
		}
	}
}

func (h *Hub) session(ctx context.Context, url string) (authed, fatal bool) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, _, err := websocket.Dial(dialCtx, url, nil)
	cancel()
	if err != nil {
		logger.Warnf(src, "Ошибка соединения: %v", err)
		return false, false
	}
	conn.SetReadLimit(2 * 1024 * 1024)
	defer conn.Close(websocket.StatusNormalClosure, "")

	auth := map[string]any{
		"type": proto.C2SAuth, "token": config.Get().AccessToken(),
		"protocol_version": proto.ProtocolVersion, "client_version": h.version,
	}
	if err := writeJSON(ctx, conn, auth); err != nil {
		return false, false
	}

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			h.mu.Lock()
			was := h.authed
			h.authed = false
			h.conn = nil
			h.mu.Unlock()
			if was {
				logger.Warnf(src, "Соединение с сервером потеряно")
				if h.OnConnection != nil {
					h.OnConnection(false)
				}
			}
			return authed, false
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg["type"] {
		case proto.S2CAuthOk:
			h.mu.Lock()
			h.authed = true
			h.conn = conn
			h.mu.Unlock()
			authed = true
			logger.Infof(src, "Авторизация на сервере выполнена")
			if h.OnConnection != nil {
				h.OnConnection(true)
			}
		case proto.S2CAuthError:
			reason, _ := msg["reason"].(string)
			logger.Errorf(src, "Сервер отклонил авторизацию: %s", reason)
			if h.OnAuthFailed != nil {
				h.OnAuthFailed(reason)
			}
			return false, true
		case proto.S2CSettingsUpdated:
			if s, ok := msg["settings"].(map[string]any); ok && h.OnSettings != nil {
				h.OnSettings(s)
			}
		case proto.S2CStartSession, proto.S2CStopSession, proto.S2CGetStatus, proto.S2CShutdown:
			if h.OnCommand != nil {
				h.OnCommand(msg)
			}
		default:
			logger.Debugf(src, "Неизвестное сообщение: %v", msg["type"])
		}
	}
}

func writeJSON(ctx context.Context, conn *websocket.Conn, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, raw)
}

func (h *Hub) Send(payload map[string]any) bool {
	h.mu.Lock()
	conn, ok := h.conn, h.authed
	h.mu.Unlock()
	if !ok || conn == nil {
		return false
	}
	return writeJSON(context.Background(), conn, payload) == nil
}

func ts() float64 { return float64(time.Now().UnixMilli()) / 1000 }

func (h *Hub) SendCurrentStatus(appState, browserState string) {
	h.Send(map[string]any{"type": proto.C2SCurrentStatus, "app_state": appState, "browser_state": browserState, "timestamp": ts()})
}
func (h *Hub) SendHeartbeatAck() {
	h.Send(map[string]any{"type": proto.C2SHeartbeatAck, "timestamp": ts()})
}
func (h *Hub) SendTokenResult(status, message string) {
	h.Send(map[string]any{"type": proto.C2STokenResult, "status": status, "message": message, "timestamp": ts()})
}
func (h *Hub) SendLiveURL(url string) {
	h.Send(map[string]any{"type": proto.C2SLiveUrlUpdate, "url": url, "timestamp": ts()})
}
func (h *Hub) SendStreamStarted(url, title string) {
	h.Send(map[string]any{"type": proto.C2SStreamStarted, "url": url, "title": title})
}
func (h *Hub) SendStreamStopped() { h.Send(map[string]any{"type": proto.C2SStreamStopped}) }
func (h *Hub) SendNotify(event, text string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	h.Send(map[string]any{"type": proto.C2SNotify, "event": event, "text": text, "data": data, "timestamp": ts()})
}
func (h *Hub) SendLinkUpsert(link map[string]any) {
	h.Send(map[string]any{"type": proto.C2SLinkUpsert, "link": link})
}
