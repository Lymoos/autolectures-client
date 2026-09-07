package config

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Lymoos/autolectures/client/internal/logger"
)

const defaultServer = "http://194.87.148.14:8000"

type Mail struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Sender   string `json:"sender,omitempty"`
	// "" — по порту, "ssl" — сразу TLS (993), "starttls" — открытое
	// соединение с переходом на TLS (143). Почта МИРЭА живёт на STARTTLS.
	Security string `json:"security,omitempty"`
}

const (
	MailSSL      = "ssl"
	MailStartTLS = "starttls"
)

// Mode возвращает способ шифрования: либо выбранный руками, либо угаданный по порту.
func (m Mail) Mode() string {
	switch strings.ToLower(strings.TrimSpace(m.Security)) {
	case MailSSL:
		return MailSSL
	case MailStartTLS:
		return MailStartTLS
	}
	if m.Port == 143 || m.Port == 1143 {
		return MailStartTLS
	}
	return MailSSL
}

func (m Mail) AutoSecurity() bool { return strings.TrimSpace(m.Security) == "" }

const DefaultSender = "invitation@mts-link.ru"

func (m Mail) SenderFilter() string {
	if s := strings.TrimSpace(m.Sender); s != "" {
		return s
	}
	return DefaultSender
}

func (m Mail) Valid() bool { return m.Host != "" && m.User != "" && m.Password != "" }

type Data struct {
	ServerURL         string            `json:"server_url"`
	Mode              string            `json:"mode"`
	AccessToken       string            `json:"access_token"`
	Login             string            `json:"login"`
	Nickname          string            `json:"nickname"`
	Group             string            `json:"group"`
	LastURL           string            `json:"last_url"`
	MailMonitoring    bool              `json:"mail_monitoring"`
	TransparentWindow *bool             `json:"transparent_window,omitempty"`
	Volume            *int              `json:"volume,omitempty"`
	OnboardingDone    bool              `json:"onboarding_done"`
	Mail              Mail              `json:"mail"`
	MailLastUID       uint32            `json:"mail_last_uid"`
	Links             []json.RawMessage `json:"links"`
	// Предмет → страница курса в СДО, откуда берутся вебинары. Свои ссылки
	// хранятся отдельно от пресета группы: свои всегда важнее.
	SdoLinks       map[string]string `json:"sdo_links,omitempty"`
	SdoPreset      map[string]string `json:"sdo_preset,omitempty"`
	SdoPresetGroup string            `json:"sdo_preset_group,omitempty"`
}

type Config struct {
	mu       sync.RWMutex
	d        Data
	path     string
	dataDir  string
	onChange func(key string)
}

var (
	once     sync.Once
	instance *Config
)

func Get() *Config {
	once.Do(func() {
		instance = &Config{}
		instance.init()
	})
	return instance
}

// BaseDir считается без обращения к синглтону: снос данных должен отработать
// до первой загрузки конфига.
func BaseDir() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil || base == "" {
			base, _ = os.UserHomeDir()
		}
	}
	return filepath.Join(base, "Autolectures")
}

// WipeStorage удаляет конфиг и профиль браузера: клиент стартует как после
// первой установки — гость, пустое расписание, обучение с нуля.
func WipeStorage() error {
	dir := BaseDir()
	var first error
	for _, name := range []string{"config.json", "config.json.tmp", "profile", "update"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (c *Config) init() {
	c.dataDir = BaseDir()
	_ = os.MkdirAll(c.dataDir, 0o755)
	c.path = filepath.Join(c.dataDir, "config.json")
	c.load()
}

func (c *Config) load() {
	c.d = Data{ServerURL: defaultServer, Mode: "guest", Mail: Mail{Host: "imap.mail.ru", Port: 993}}
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var d Data
	if json.Unmarshal(raw, &d) != nil {
		return
	}
	if d.ServerURL == "" {
		d.ServerURL = defaultServer
	}
	if d.Mode == "" {
		d.Mode = "guest"
	}
	if d.Mail.Port == 0 {
		d.Mail.Port = 993
	}
	c.d = d
	if c.normalizeSecrets() {
		c.save()
	}
}

// Токен и пароль почты запечатаны DPAPI: на другом ПК или под другой учётной
// записью Windows они не расшифруются, поэтому вход и почта сбрасываются, а не
// подхватываются из скопированного config.json.
func (c *Config) normalizeSecrets() bool {
	resealed := false
	if tok, ok := openPlain(c.d.AccessToken); !ok {
		logger.Warnf("Настройки", "Сохранённый вход не принадлежит этому пользователю Windows — включён гостевой режим")
		c.d.AccessToken = ""
		c.d.Login = ""
		c.d.Mode = "guest"
		resealed = true
	} else if tok != "" && !strings.HasPrefix(c.d.AccessToken, secretPrefix) {
		c.d.AccessToken = sealPlain(tok)
		resealed = true
	}
	if pass, ok := openSecret(c.d.Mail.Password); !ok {
		logger.Warnf("Настройки", "Пароль почты не расшифровывается на этом ПК — подключите ящик заново")
		c.d.Mail = Mail{Host: c.d.Mail.Host, Port: c.d.Mail.Port, User: c.d.Mail.User, Sender: c.d.Mail.Sender}
		c.d.MailMonitoring = false
		c.d.MailLastUID = 0
		resealed = true
	} else if pass != "" && !strings.HasPrefix(c.d.Mail.Password, secretPrefix) {
		c.d.Mail.Password = sealSecret(pass)
		resealed = true
	}
	return resealed
}

func (c *Config) save() {
	raw, err := json.MarshalIndent(c.d, "", "  ")
	if err != nil {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

func (c *Config) OnChange(fn func(key string)) { c.onChange = fn }

func (c *Config) set(key string, mutate func(d *Data)) {
	c.mu.Lock()
	mutate(&c.d)
	c.save()
	fn := c.onChange
	c.mu.Unlock()
	if fn != nil {
		fn(key)
	}
}

func (c *Config) read(fn func(d *Data)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	fn(&c.d)
}

func (c *Config) DataDir() string { return c.dataDir }

func (c *Config) Path() string { return c.path }

func (c *Config) ServerURL() string {
	var v string
	c.read(func(d *Data) { v = d.ServerURL })
	return v
}
func (c *Config) SetServerURL(v string) {
	c.set("server_url", func(d *Data) { d.ServerURL = strings.TrimSpace(v) })
}

func (c *Config) WsURL() (string, error) {
	u, err := url.Parse(c.ServerURL())
	if err != nil || u.Host == "" {
		return "", errors.New("некорректный адрес сервера")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/ws/node"
	u.RawQuery = ""
	return u.String(), nil
}

func (c *Config) IsGuest() bool {
	var v bool
	c.read(func(d *Data) { v = d.Mode != "account" })
	return v
}
func (c *Config) SetGuest(g bool) {
	c.set("mode", func(d *Data) {
		if g {
			d.Mode = "guest"
		} else {
			d.Mode = "account"
		}
	})
}
func (c *Config) AccessToken() string {
	var v string
	c.read(func(d *Data) { v = d.AccessToken })
	tok, _ := openPlain(v)
	return tok
}
func (c *Config) SetAccessToken(v string) {
	c.set("access_token", func(d *Data) { d.AccessToken = sealPlain(v) })
}
func (c *Config) Login() string     { var v string; c.read(func(d *Data) { v = d.Login }); return v }
func (c *Config) SetLogin(v string) { c.set("login", func(d *Data) { d.Login = v }) }

func (c *Config) Nickname() string { var v string; c.read(func(d *Data) { v = d.Nickname }); return v }
func (c *Config) SetNickname(v string) {
	c.set("nickname", func(d *Data) { d.Nickname = strings.TrimSpace(v) })
}
func (c *Config) Group() string { var v string; c.read(func(d *Data) { v = d.Group }); return v }
func (c *Config) SetGroup(v string) {
	c.set("group", func(d *Data) { d.Group = strings.ToUpper(strings.TrimSpace(v)) })
}
func (c *Config) LastURL() string { var v string; c.read(func(d *Data) { v = d.LastURL }); return v }
func (c *Config) SetLastURL(v string) {
	c.set("last_url", func(d *Data) { d.LastURL = strings.TrimSpace(v) })
}
func (c *Config) MailMonitoring() bool {
	var v bool
	c.read(func(d *Data) { v = d.MailMonitoring })
	return v
}
func (c *Config) SetMailMonitoring(v bool) {
	c.set("mail_monitoring", func(d *Data) { d.MailMonitoring = v })
}

// Эко-режим включён всегда: он бережёт батарею и процессор.
func (c *Config) EcoMode() bool { return true }
func (c *Config) TransparentWindow() bool {
	return c.flag(func(d *Data) *bool { return d.TransparentWindow }, true)
}
func (c *Config) SetTransparentWindow(v bool) {
	c.set("transparent_window", func(d *Data) { d.TransparentWindow = &v })
}
func (c *Config) Volume() int {
	var v = 100
	c.read(func(d *Data) {
		if d.Volume != nil {
			v = *d.Volume
		}
	})
	return v
}
func (c *Config) SetVolume(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	c.set("volume", func(d *Data) { d.Volume = &v })
}
func (c *Config) flag(pick func(d *Data) *bool, def bool) bool {
	v := def
	c.read(func(d *Data) {
		if p := pick(d); p != nil {
			v = *p
		}
	})
	return v
}

func (c *Config) OnboardingDone() bool {
	var v bool
	c.read(func(d *Data) { v = d.OnboardingDone })
	return v
}
func (c *Config) SetOnboardingDone(v bool) {
	c.set("onboarding_done", func(d *Data) { d.OnboardingDone = v })
}

func (c *Config) MailSettings() Mail {
	var m Mail
	c.read(func(d *Data) { m = d.Mail })
	m.Password, _ = openSecret(m.Password)
	return m
}
func (c *Config) SetMailSettings(m Mail) {
	m.Password = sealSecret(m.Password)
	c.set("mail", func(d *Data) { d.Mail = m })
}
func (c *Config) MailLastUID() uint32 {
	var v uint32
	c.read(func(d *Data) { v = d.MailLastUID })
	return v
}
func (c *Config) SetMailLastUID(v uint32) {
	c.set("mail_last_uid", func(d *Data) { d.MailLastUID = v })
}

func (c *Config) SdoLinks() map[string]string {
	out := map[string]string{}
	c.read(func(d *Data) {
		for k, v := range d.SdoLinks {
			out[k] = v
		}
	})
	return out
}

// SdoLink отдаёт ссылку на курс: сначала свою, потом из пресета группы.
func (c *Config) SdoLink(subject string) string {
	u, _ := c.sdoLink(subject)
	return u
}

// SdoLinkSource: "own" — вписана вручную, "preset" — пришла с пресетом группы.
func (c *Config) SdoLinkSource(subject string) string {
	_, src := c.sdoLink(subject)
	return src
}

func (c *Config) sdoLink(subject string) (string, string) {
	key := strings.ToLower(strings.TrimSpace(subject))
	var own, preset, presetGroup, group string
	c.read(func(d *Data) {
		own, preset, presetGroup, group = d.SdoLinks[key], d.SdoPreset[key], d.SdoPresetGroup, d.Group
	})
	if own != "" {
		return own, "own"
	}
	if preset != "" && strings.EqualFold(presetGroup, group) {
		return preset, "preset"
	}
	return "", ""
}

// PresetInfo — чей пресет лежит локально и сколько в нём ссылок.
func (c *Config) PresetInfo() (group string, count int) {
	c.read(func(d *Data) { group, count = d.SdoPresetGroup, len(d.SdoPreset) })
	return group, count
}

// SetSdoPreset запоминает пресет группы, пришедший с сервера.
func (c *Config) SetSdoPreset(group string, links map[string]string) {
	c.set("sdo_preset", func(d *Data) {
		d.SdoPresetGroup = strings.ToUpper(strings.TrimSpace(group))
		if len(links) == 0 {
			d.SdoPreset = nil
			return
		}
		d.SdoPreset = map[string]string{}
		for k, v := range links {
			if k = strings.ToLower(strings.TrimSpace(k)); k != "" && strings.TrimSpace(v) != "" {
				d.SdoPreset[k] = strings.TrimSpace(v)
			}
		}
	})
}

func (c *Config) SetSdoLink(subject, url string) {
	key := strings.ToLower(strings.TrimSpace(subject))
	if key == "" {
		return
	}
	c.set("sdo_links", func(d *Data) {
		if d.SdoLinks == nil {
			d.SdoLinks = map[string]string{}
		}
		if url = strings.TrimSpace(url); url == "" {
			delete(d.SdoLinks, key)
		} else {
			d.SdoLinks[key] = url
		}
	})
}

func (c *Config) Links() []json.RawMessage {
	var v []json.RawMessage
	c.read(func(d *Data) { v = append([]json.RawMessage(nil), d.Links...) })
	return v
}
func (c *Config) SetLinks(v []json.RawMessage) { c.set("links", func(d *Data) { d.Links = v }) }

func (c *Config) Syncable() map[string]any {
	return map[string]any{
		"nickname":           c.Nickname(),
		"group":              c.Group(),
		"mail_monitoring":    c.MailMonitoring(),
		"volume":             c.Volume(),
		"transparent_window": c.TransparentWindow(),
	}
}

func (c *Config) ApplySynced(s map[string]any) {
	if v, ok := s["nickname"].(string); ok {
		c.SetNickname(v)
	}
	if v, ok := s["group"].(string); ok {
		c.SetGroup(v)
	}
	if v, ok := s["mail_monitoring"].(bool); ok {
		c.SetMailMonitoring(v)
	}
	if v, ok := s["transparent_window"].(bool); ok {
		c.SetTransparentWindow(v)
	}
	if v, ok := s["volume"].(float64); ok {
		c.SetVolume(int(v))
	}
}
