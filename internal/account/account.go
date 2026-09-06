package account

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Lymoos/autolectures/client/internal/api"
	"github.com/Lymoos/autolectures/client/internal/config"
	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/proto"
)

const src = "Аккаунт"

type Manager struct {
	api *api.Client

	mu        sync.Mutex
	pushTimer *time.Timer

	OnChanged     func()
	OnLinksPulled func(links []json.RawMessage)
	OnSettings    func()
	OnSyncError   func(err string)
}

func New(a *api.Client) *Manager { return &Manager{api: a} }

func (m *Manager) IsGuest() bool { return config.Get().IsGuest() }
func (m *Manager) Authorized() bool {
	return !config.Get().IsGuest() && config.Get().AccessToken() != ""
}
func (m *Manager) Login() string { return config.Get().Login() }

func (m *Manager) applyAuth(body map[string]any, login string) {
	cfg := config.Get()
	tok, _ := body["access_token"].(string)
	if l, ok := body["login"].(string); ok && l != "" {
		login = l
	}
	cfg.SetAccessToken(tok)
	cfg.SetLogin(login)
	cfg.SetGuest(false)
	logger.Infof(src, "Вход выполнен: %s", login)
	if m.OnChanged != nil {
		m.OnChanged()
	}
	m.PullAll()
}

func (m *Manager) SignIn(login, password string) error {
	r := m.api.Post(proto.ApiAuthLogin, map[string]any{"login": login, "password": password})
	if !r.OK {
		return errors.New(r.Err)
	}
	m.applyAuth(r.Body, login)
	return nil
}

func (m *Manager) SignUp(login, password string) error {
	r := m.api.Post(proto.ApiAuthRegister, map[string]any{"login": login, "password": password})
	if !r.OK {
		return errors.New(r.Err)
	}
	m.applyAuth(r.Body, login)
	return nil
}

func (m *Manager) SignOut() {
	cfg := config.Get()
	cfg.SetAccessToken("")
	cfg.SetLogin("")
	cfg.SetGuest(true)
	logger.Infof(src, "Выход из аккаунта, включён гостевой режим")
	if m.OnChanged != nil {
		m.OnChanged()
	}
}

func (m *Manager) ContinueAsGuest() {
	if !m.IsGuest() {
		m.SignOut()
	} else if m.OnChanged != nil {
		m.OnChanged()
	}
}

func (m *Manager) RestoreSession() {
	if !m.Authorized() {
		if m.OnChanged != nil {
			m.OnChanged()
		}
		return
	}
	r := m.api.Get(proto.ApiAuthMe)
	switch {
	case r.OK:
		if l, ok := r.Body["login"].(string); ok && l != "" {
			config.Get().SetLogin(l)
		}
		if m.OnChanged != nil {
			m.OnChanged()
		}
		m.PullAll()
	case r.Status == 401:
		logger.Warnf(src, "Сессия аккаунта истекла, требуется повторный вход")
		m.SignOut()
	default:
		if m.OnChanged != nil {
			m.OnChanged()
		}
	}
}

func (m *Manager) PullAll() {
	if !m.Authorized() {
		return
	}
	if r := m.api.Get(proto.ApiSyncSettings); r.OK {
		if s, ok := r.Body["settings"].(map[string]any); ok {
			config.Get().ApplySynced(s)
		}
		if m.OnSettings != nil {
			m.OnSettings()
		}
	}
	if r := m.api.Get(proto.ApiSyncLinks); r.OK {
		if arr, ok := r.Body["links"].([]any); ok && m.OnLinksPulled != nil {
			var links []json.RawMessage
			for _, l := range arr {
				if raw, err := json.Marshal(l); err == nil {
					links = append(links, raw)
				}
			}
			m.OnLinksPulled(links)
		}
	}
}

func (m *Manager) PushSettings() {
	if !m.Authorized() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pushTimer != nil {
		m.pushTimer.Stop()
	}
	m.pushTimer = time.AfterFunc(1500*time.Millisecond, func() {
		if !m.Authorized() {
			return
		}
		r := m.api.Put(proto.ApiSyncSettings, map[string]any{"settings": config.Get().Syncable()})
		if !r.OK && m.OnSyncError != nil {
			m.OnSyncError(r.Err)
		}
	})
}

func (m *Manager) PushLinks(links []json.RawMessage) {
	if !m.Authorized() {
		return
	}
	items := make([]any, 0, len(links))
	for _, l := range links {
		var v any
		if json.Unmarshal(l, &v) == nil {
			items = append(items, v)
		}
	}
	r := m.api.Put(proto.ApiSyncLinks, map[string]any{"links": items})
	if !r.OK && m.OnSyncError != nil {
		m.OnSyncError(r.Err)
	}
}

func (m *Manager) TelegramCode() (code, deepLink string, err error) {
	if !m.Authorized() {
		return "", "", errors.New("привязка Telegram доступна только с аккаунтом")
	}
	r := m.api.Post(proto.ApiTelegramCode, map[string]any{})
	if !r.OK {
		return "", "", errors.New(r.Err)
	}
	code, _ = r.Body["code"].(string)
	deepLink, _ = r.Body["deep_link"].(string)
	return code, deepLink, nil
}

func (m *Manager) TelegramStatus() (linked bool, username string) {
	if !m.Authorized() {
		return false, ""
	}
	r := m.api.Get(proto.ApiTelegramStat)
	if !r.OK {
		return false, ""
	}
	linked, _ = r.Body["linked"].(bool)
	username, _ = r.Body["username"].(string)
	return linked, username
}
