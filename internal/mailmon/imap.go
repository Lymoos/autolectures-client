package mailmon

import (
	"context"
	"fmt"
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


func (m *Monitor) Test(s config.Mail) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	c, err := dial(ctx, s)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Login(s.User, s.Password).Wait(); err != nil {
		return fmt.Errorf("неверный логин или пароль приложения (%s)", cleanErr(err))
	}
	_ = c.Logout().Wait()
	return nil
}

func dial(ctx context.Context, s config.Mail) (*imapclient.Client, error) {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	type res struct {
		c   *imapclient.Client
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := imapclient.DialTLS(addr, nil)
		ch <- res{c, err}
	}()
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("почтовый сервер %s не ответил вовремя", s.Host)
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("не удалось подключиться к %s: %s", s.Host, cleanErr(r.err))
		}
		return r.c, nil
	}
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

	if err := m.fetchNew(ctx, s); err != nil {
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
	if _, err := c.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("не удалось открыть папку «Входящие»")
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

