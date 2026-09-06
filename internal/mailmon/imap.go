package mailmon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/Lymoos/autolectures/client/internal/config"
	"github.com/Lymoos/autolectures/client/internal/logger"
)

const (
	src          = "Почта"
	pollInterval = 60 * time.Second
	opTimeout    = 45 * time.Second
)

type Monitor struct {
	mu       sync.Mutex
	settings config.Mail
	running  bool
	busy     bool
	cancel   context.CancelFunc

	OnInvitation func(inv Invitation, subject string)
	OnStatus     func(text string)
	OnSettings   func(s config.Mail)
	OnError      func(text string)
}

func New() *Monitor { return &Monitor{} }

func (m *Monitor) SetSettings(s config.Mail) {
	m.mu.Lock()
	m.settings = s
	m.mu.Unlock()
}

func (m *Monitor) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

func (m *Monitor) Start() {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	if !m.settings.Valid() {
		m.mu.Unlock()
		if m.OnError != nil {
			m.OnError("Почта не настроена: укажите сервер, логин и пароль приложения")
		}
		return
	}
	m.running = true
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	user := m.settings.User
	m.mu.Unlock()

	logger.Infof(src, "Мониторинг почты запущен (%s, каждые %.0f с)", user, pollInterval.Seconds())
	if m.OnStatus != nil {
		m.OnStatus("Мониторинг активен")
	}
	go func() {
		m.check(ctx)
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.check(ctx)
			}
		}
	}()
}

func (m *Monitor) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	cancel := m.cancel
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	logger.Infof(src, "Мониторинг почты остановлен")
	if m.OnStatus != nil {
		m.OnStatus("Мониторинг выключен")
	}
}

func (m *Monitor) CheckNow() {
	m.mu.Lock()
	running := m.running
	m.mu.Unlock()
	if running {
		go m.check(context.Background())
	}
}

// Test проверяет не только вход, но и чтение папки: раньше диалог рапортовал
// об успехе, а мониторинг потом падал на SELECT.
func (m *Monitor) Test(s config.Mail) (config.Mail, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	err := probe(ctx, s)
	if err != nil && s.AutoSecurity() && brokenProtocol(err) {
		alt := altSettings(s)
		logger.Warnf(src, "%v — пробую %s:%d (%s)", err, alt.Host, alt.Port, modeName(alt.Mode()))
		if err2 := probe(ctx, alt); err2 == nil {
			return alt, nil
		}
	}
	return s, err
}

func probe(ctx context.Context, s config.Mail) error {
	c, err := dial(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Login(s.User, s.Password).Wait(); err != nil {
		return fmt.Errorf("неверный логин или пароль приложения (%s)", cleanErr(err))
	}
	if err := selectInbox(c); err != nil {
		return err
	}
	_ = c.Logout().Wait()
	return nil
}

func modeName(mode string) string {
	if mode == config.MailStartTLS {
		return "STARTTLS"
	}
	return "SSL/TLS"
}

func dialMode(ctx context.Context, addr, mode string) (*imapclient.Client, error) {
	type res struct {
		c   *imapclient.Client
		err error
	}
	ch := make(chan res, 1)
	go func() {
		var c *imapclient.Client
		var err error
		if mode == config.MailStartTLS {
			c, err = imapclient.DialStartTLS(addr, nil)
		} else {
			c, err = imapclient.DialTLS(addr, nil)
		}
		ch <- res{c, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		return r.c, r.err
	}
}

// Порт 993 говорит по TLS сразу, 143 — открытым текстом с переходом на TLS
// командой STARTTLS. Если способ не выбран руками, пробуем оба: сервер вроде
// imap.mirea.ru отвечает только на второй.
func dial(ctx context.Context, s config.Mail) (*imapclient.Client, error) {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	modes := []string{s.Mode()}
	if s.AutoSecurity() {
		if modes[0] == config.MailSSL {
			modes = append(modes, config.MailStartTLS)
		} else {
			modes = append(modes, config.MailSSL)
		}
	}
	var last error
	for i, mode := range modes {
		c, err := dialMode(ctx, addr, mode)
		if err == nil {
			if i > 0 {
				logger.Infof(src, "%s подключён по %s", s.Host, modeName(mode))
			}
			return c, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, fmt.Errorf("почтовый сервер %s не ответил вовремя", s.Host)
		}
		logger.Debugf(src, "%s по %s: %s", addr, modeName(mode), cleanErr(err))
	}
	hint := ""
	if s.AutoSecurity() {
		hint = ". Проверьте порт: 993 для SSL/TLS, 143 для STARTTLS"
	}
	return nil, fmt.Errorf("не удалось подключиться к %s по %s: %s%s",
		s.Host, modeName(s.Mode()), cleanErr(last), hint)
}

// Обычно входящие лежат в INBOX, но некоторые серверы прячут их за префиксом
// или под локализованным именем — тогда ищем папку в списке, а заодно кладём
// весь список в журнал, чтобы было по чему разбираться.
func selectInbox(c *imapclient.Client) error {
	_, first := c.Select("INBOX", nil).Wait()
	if first == nil {
		return nil
	}
	boxes, err := c.List("", "*", nil).Collect()
	if err != nil {
		return fmt.Errorf("не удалось открыть папку «Входящие»: %s", cleanErr(first))
	}
	names := make([]string, 0, len(boxes))
	for _, b := range boxes {
		names = append(names, b.Mailbox)
	}
	logger.Debugf(src, "Папки на сервере: %s", strings.Join(names, ", "))
	for _, b := range boxes {
		low := strings.ToLower(b.Mailbox)
		if slices.Contains(b.Attrs, imap.MailboxAttrNoSelect) {
			continue
		}
		if low != "inbox" && !strings.HasSuffix(low, "inbox") && !strings.Contains(low, "входящ") {
			continue
		}
		if _, err := c.Select(b.Mailbox, nil).Wait(); err == nil {
			logger.Infof(src, "Входящие открыты как «%s»", b.Mailbox)
			return nil
		}
	}
	return fmt.Errorf("не удалось открыть папку «Входящие»: %s", cleanErr(first))
}

func cleanErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "imap:"); i >= 0 {
		s = strings.TrimSpace(s[i+5:])
	}
	return s
}

func (m *Monitor) check(ctx context.Context) {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return
	}
	m.busy = true
	s := m.settings
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.busy = false
		m.mu.Unlock()
	}()

	err := m.fetchNew(ctx, s)
	// Часть серверов (например, imap.mirea.ru на 143) ломает разбор ответов
	// посреди сессии. Если способ подключения не выбран руками, пробуем вторую
	// пару «порт + шифрование» и запоминаем ту, что заработала.
	if err != nil && ctx.Err() == nil && s.AutoSecurity() && brokenProtocol(err) {
		alt := altSettings(s)
		logger.Warnf(src, "%v — пробую %s:%d (%s)", err, alt.Host, alt.Port, modeName(alt.Mode()))
		if err2 := m.fetchNew(ctx, alt); err2 == nil {
			m.remember(alt)
			err = nil
		} else if ctx.Err() == nil {
			logger.Warnf(src, "%v", err2)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		logger.Warnf(src, "%v", err)
		if m.OnError != nil {
			m.OnError(err.Error())
		}
		return
	}
	if m.OnStatus != nil {
		m.OnStatus("Проверено " + time.Now().Format("15:04"))
	}
}

// brokenProtocol отличает «сервер отвечает не по IMAP» от нормальных ошибок
// вроде неверного пароля: во втором случае перебирать порты бессмысленно.
func brokenProtocol(err error) bool {
	t := strings.ToLower(err.Error())
	for _, mark := range []string{"unknown tag", "response-tagged", "unexpected", "malformed", "handshake", "eof", "не удалось подключиться"} {
		if strings.Contains(t, mark) {
			return true
		}
	}
	return false
}

func altSettings(s config.Mail) config.Mail {
	alt := s
	if s.Mode() == config.MailStartTLS {
		alt.Security, alt.Port = config.MailSSL, 993
	} else {
		alt.Security, alt.Port = config.MailStartTLS, 143
	}
	return alt
}

func (m *Monitor) remember(s config.Mail) {
	logger.Infof(src, "Почта работает через %s:%d (%s) — запомнил эти настройки", s.Host, s.Port, modeName(s.Mode()))
	config.Get().SetMailSettings(s)
	m.SetSettings(s)
	if m.OnSettings != nil {
		m.OnSettings(s)
	}
}

func (m *Monitor) fetchNew(ctx context.Context, s config.Mail) error {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	c, err := dial(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Login(s.User, s.Password).Wait(); err != nil {
		return fmt.Errorf("неверный логин или пароль приложения (%s)", cleanErr(err))
	}
	if err := selectInbox(c); err != nil {
		return err
	}

	lastUID := config.Get().MailLastUID()
	criteria := &imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{{Key: "From", Value: s.SenderFilter()}},
	}
	if lastUID == 0 {
		criteria.Since = time.Now().AddDate(0, 0, -7)
	} else {
		criteria.UID = []imap.UIDSet{{imap.UIDRange{Start: imap.UID(lastUID + 1), Stop: 0}}}
	}
	data, err := c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return fmt.Errorf("поиск писем завершился ошибкой: %s", cleanErr(err))
	}
	var uids []imap.UID
	for _, u := range data.AllUIDs() {
		if uint32(u) > lastUID {
			uids = append(uids, u)
		}
	}
	if len(uids) == 0 {
		_ = c.Logout().Wait()
		return nil
	}
	logger.Infof(src, "Новых писем от «%s»: %d", s.SenderFilter(), len(uids))

	section := &imap.FetchItemBodySection{Peek: true}
	cmd := c.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}})
	maxUID := lastUID
	for {
		msg := cmd.Next()
		if msg == nil {
			break
		}
		buf, err := msg.Collect()
		if err != nil {
			continue
		}
		if uint32(buf.UID) > maxUID {
			maxUID = uint32(buf.UID)
		}
		raw := buf.FindBodySection(section)
		if len(raw) == 0 {
			continue
		}
		parsed := ParseMessage(raw)
		inv := ExtractInvitation(parsed)
		if inv.Valid() {
			logger.Infof(src, "Распознано приглашение: %s — %s", inv.When.Format("02.01.2006 15:04"), inv.URL)
			if m.OnInvitation != nil {
				m.OnInvitation(inv, parsed.Subject)
			}
		} else {
			logger.Warnf(src, "В письме «%s» не найдена ссылка на встречу", parsed.Subject)
		}
	}
	_ = cmd.Close()
	if maxUID > lastUID {
		config.Get().SetMailLastUID(maxUID)
	}
	_ = c.Logout().Wait()
	return nil
}
