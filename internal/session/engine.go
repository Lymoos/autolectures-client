//go:build windows

package session

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Lymoos/autolectures/client/internal/hub"
	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/notify"
	"github.com/Lymoos/autolectures/client/internal/proto"
	"github.com/Lymoos/autolectures/client/internal/sdo"
	"github.com/Lymoos/autolectures/client/internal/state"
	"github.com/Lymoos/autolectures/client/internal/webview"
)

const src = "Сессия"

var allowedDomains = []string{"attendance.mirea.ru", "pulse.mirea.ru", "token.internal"}

const probeScript = `
(function () {
  try {
    var text = (document.body && document.body.innerText) ? document.body.innerText : '';
    var lower = text.toLowerCase();
    var r = 'NONE';
    if (lower.indexOf('успешно') !== -1 || lower.indexOf('отметились') !== -1) r = 'SUCCESS';
    else {
      var nodes = document.querySelectorAll('a, button');
      for (var i = 0; i < nodes.length; i++) {
        var t = (nodes[i].innerText || '').trim().toLowerCase();
        if (t === 'войти' || t.indexOf('войти') === 0) {
          var b = nodes[i].getBoundingClientRect();
          if (b.width > 0 && b.height > 0) { r = 'NEEDS_AUTH'; break; }
        }
      }
    }
    chrome.webview.postMessage(JSON.stringify({ type: 'probe', result: r }));
  } catch (e) { chrome.webview.postMessage(JSON.stringify({ type: 'probe', result: 'NONE' })); }
})();`

const escoProbeScript = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  try {
    var vis = function (el) {
      var b = el.getBoundingClientRect();
      if (b.width <= 0 || b.height <= 0) return false;
      var st = getComputedStyle(el);
      return st.visibility !== 'hidden' && st.display !== 'none' && st.opacity !== '0';
    };
    // 1. имя: самый правый элемент в шапке, ниже могут быть преподаватели
    var nameRe = /^[А-ЯЁ][а-яё-]{1,30}\s+(?:[А-ЯЁ]\.\s?[А-ЯЁ]?\.?|[А-ЯЁ][а-яё]{1,20})$/;
    var name = '', nameX = -1;
    var nodes = document.querySelectorAll('a, button, span, div, p, li');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (el.children.length > 2) continue;                 // только «листья»
      var t = (el.innerText || '').replace(/\s+/g, ' ').trim();
      if (t.length < 4 || t.length > 40 || !nameRe.test(t)) continue;
      if (!vis(el)) continue;
      var r = el.getBoundingClientRect();
      if (r.top > 150) continue;                            // не шапка
      if (r.left > nameX) { name = t; nameX = r.left; }
    }
    // 2. Кнопка входа: если она на экране, значит сессии нет.
    var login = false;
    var clickable = document.querySelectorAll('a, button, [role="button"]');
    for (var j = 0; j < clickable.length && !login; j++) {
      var s = (clickable[j].innerText || '').replace(/\s+/g, ' ').trim().toLowerCase();
      if (s === 'войти' || s === 'вход' || s === 'sign in' || s === 'log in') login = vis(clickable[j]);
    }
    post({ type: 'esco', host: location.hostname,
           result: name && !login ? 'IN' : (login ? 'OUT' : 'UNKNOWN'), name: name });
  } catch (e) { post({ type: 'esco', host: location.hostname, result: 'UNKNOWN', name: '' }); }
})();`

// СДО МИРЭА живёт за тем же входом, что и ЕСКО, поэтому куки уже есть в общем
// профиле браузера. Пока только определяем, пускает ли нас система, и сколько
// курсов видно — на этом потом построится разбор лекций из СДО.
const sdoProbeScript = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  try {
    // Moodle сам помечает страницу: userloggedin или notloggedin. Это надёжнее
    // поиска кнопки «Вход» — она бывает и на страницах вошедшего пользователя.
    var cls = ' ' + (document.body.className || '') + ' ';
    var loggedIn = / userloggedin /.test(cls);
    var loggedOut = / notloggedin /.test(cls);

    var name = '';
    var menu = document.querySelector('[data-region="user-menu"] .usertext, .usermenu .usertext, .userbutton .usertext, [data-region="user-menu"]');
    if (menu) name = (menu.innerText || '').replace(/\s+/g, ' ').trim().slice(0, 60);
    if (!name) {
      var m = /Вы зашли под именем\s+([^(]{3,60})/i.exec(document.body.innerText || '');
      if (m) name = m[1].split(String.fromCharCode(10))[0].trim();
    }
    var courses = document.querySelectorAll('.course-card, .coursebox, [data-region="course-content"], .card.dashboard-card').length;

    // Тема РТУ МИРЭА не ставит класс userloggedin, поэтому опираемся ещё на
    // меню пользователя, ссылки на курсы и текст страницы.
    var menuAny = document.querySelector('[data-region="user-menu"], .usermenu, #user-menu-toggle, .userinitials, .avatar, .userpicture');
    var hints = /мои курсы|my courses|личный кабинет|dashboard|выход|log out/i.test(document.body.innerText || '');
    var courseLinks = document.querySelectorAll('a[href*="/course/view.php"]').length;
    var loginForm = !!document.querySelector('form#login, form.login, input[name="password"]');

    var result = 'UNKNOWN';
    if (loggedOut || loginForm) result = 'OUT';
    else if (loggedIn || menuAny || courses > 0 || courseLinks > 0 || hints || name) result = 'IN';

    post({ type: 'sdo', host: location.hostname, result: result, name: name, courses: courses, cls: cls.trim().slice(0, 120) });
  } catch (e) { post({ type: 'sdo', host: location.hostname, result: 'UNKNOWN', name: '', courses: 0 }); }
})();`

// diagScript снимает срез страницы трансляции: что за кнопки и вкладки на ней
// есть, какие числа рядом со словом «участники». По этому срезу настраиваются
// селекторы поиска — вслепую они не подбираются.
const diagScript = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  function text(el) { return ((el && (el.innerText || el.textContent)) || '').replace(/\s+/g, ' ').trim(); }
  function deep(sel) {
    var out = [];
    (function walk(root) {
      try { out.push.apply(out, root.querySelectorAll(sel)); } catch (e) { return; }
      var all = root.querySelectorAll('*');
      for (var i = 0; i < all.length; i++) if (all[i].shadowRoot) walk(all[i].shadowRoot);
    })(document);
    return out;
  }
  try {
    var out = [], seen = {};
    var nodes = deep('button, [role="button"], [role="tab"], a, li, div[class*="tab" i], span[class*="count" i], [class*="badge" i], [aria-label]');
    for (var i = 0; i < nodes.length && out.length < 60; i++) {
      var el = nodes[i];
      var b = el.getBoundingClientRect();
      if (b.width <= 0 || b.height <= 0) continue;
      var t = text(el);
      var aria = el.getAttribute('aria-label') || el.getAttribute('title') || '';
      if (t.length > 60) t = t.slice(0, 60);
      var cls = (typeof el.className === 'string' ? el.className : '').slice(0, 60);
      var key = t + '|' + aria + '|' + cls;
      if (!t && !aria) continue;
      if (seen[key]) continue;
      seen[key] = 1;
      out.push({ tag: el.tagName.toLowerCase(), text: t, aria: aria, cls: cls });
    }
    // отдельно всё, где рядом со словом «участник» есть число
    var people = [];
    var all = deep('*');
    for (var j = 0; j < all.length && people.length < 15; j++) {
      var el2 = all[j];
      if (el2.children && el2.children.length > 3) continue;
      var t2 = text(el2);
      if (!t2 || t2.length > 60) continue;
      if (!/участник|participant|в эфире|online|зрител/i.test(t2)) continue;
      people.push(t2);
    }
    post({ type: 'diag', items: out, people: people, url: location.href, frames: document.querySelectorAll('iframe').length });
  } catch (e) { post({ type: 'diag', items: [], people: [], url: String(e), frames: 0 }); }
})();`

type Engine struct {
	view     *webview.View
	auth     *webview.View
	hub      *hub.Hub
	notifier *notify.Notifier
	sm       *state.Machine
	dispatch func(func())

	mu             sync.Mutex
	currentURL     string
	currentTitle   string
	startedAt      time.Time
	attendanceDone bool
	joined         bool
	lastValidation time.Time
	nickname       string
	people         int
	peakPeople     int
	lowStreak      int
	overFired      bool
	scanEnabled    bool
	antiAfk        bool
	volume         int
	joinTimer      *time.Timer
	statusTicker   *time.Ticker

	validating    bool
	validateURL   string
	validateTimer *time.Timer
	pollTicker    *time.Ticker
	authMode      string

	sdoTimer    *time.Timer
	loginSystem string
	expectHost  string
	sdoScript   string
	sdoCourse   string
	escoTimer   *time.Timer
	loginPoll   *time.Ticker
	escoName    string
	escoKnown   bool
	escoOK      bool
	sdoName     string
	sdoKnown    bool
	sdoOK       bool

	OnState            func(prev, cur state.Engine)
	OnStarted          func(url, title string)
	OnStopped          func(url string, seconds int64)
	OnTitle            func(title string)
	OnMarked           func(url string)
	OnPresence         func()
	OnParticipants     func(count int)
	OnMedia            func(action, kind, mic string, asked int)
	OnSdoStatus        func(ok bool, name string, courses int)
	OnLectureOver      func(reason string, now, peak int)
	OnSdoCourses       func(courses []sdo.Course)
	OnSdoLinks         func(course string, links []sdo.Link)
	OnSdoActivities    func(items []sdo.WebinarActivity, info sdo.PageInfo)
	OnSdoWebinars      func(rows []sdo.Webinar)
	OnSdoJoin          func(url string)
	OnAuthRequired     func(url string)
	OnAttendance       func(status, text string)
	OnEscoStatus       func(ok bool, name string)
	OnLoggedIn         func(system, name string)
	OnNicknameRequired func()
	OnShutdown         func()
	OnError            func(msg string)
	OnPageLog          func(level, message string)
	OnDiag             func(items []map[string]string, people []string, url string, frames int)
}

func New(view, auth *webview.View, h *hub.Hub, n *notify.Notifier, scripts string, dispatch func(func())) *Engine {
	e := &Engine{view: view, auth: auth, hub: h, notifier: n, dispatch: dispatch, antiAfk: true, volume: 100}
	e.sm = state.New(func(prev, cur state.Engine) {
		logger.Infof(src, "Состояние: %s → %s", prev.Name(), cur.Name())
		if e.OnState != nil {
			e.OnState(prev, cur)
		}
		e.PublishStatus()
	})
	view.Init(scripts)
	view.OnMessage = e.onPageMessage
	view.OnNavigated = e.onNavigated
	auth.OnMessage = e.onAuthMessage
	auth.OnNavigated = e.onAuthNavigated

	e.statusTicker = time.NewTicker(60 * time.Second)
	go func() {
		for range e.statusTicker.C {
			e.dispatch(e.PublishStatus)
		}
	}()
	return e
}

func (e *Engine) State() state.Engine { return e.sm.State() }
func (e *Engine) Active() bool        { return e.sm.Active() }
func (e *Engine) CurrentURL() string  { e.mu.Lock(); defer e.mu.Unlock(); return e.currentURL }
func (e *Engine) CurrentTitle() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentTitle
}
func (e *Engine) AttendanceDone() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.attendanceDone }

func (e *Engine) Participants() int { e.mu.Lock(); defer e.mu.Unlock(); return e.people }

func (e *Engine) Seconds() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.sm.Active() || e.startedAt.IsZero() {
		return 0
	}
	return int64(time.Since(e.startedAt).Seconds())
}

func (e *Engine) pushState() {
	e.mu.Lock()
	st := map[string]any{"nickname": e.nickname, "scanEnabled": e.scanEnabled, "antiAfkEnabled": e.antiAfk, "volume": e.volume}
	e.mu.Unlock()
	raw, _ := json.Marshal(st)
	e.view.Eval("window.__AL && window.__AL.setState(" + string(raw) + ");")
}

func (e *Engine) SetNickname(n string) { e.mu.Lock(); e.nickname = n; e.mu.Unlock(); e.pushState() }
func (e *Engine) SetVolume(v int) {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	e.mu.Lock()
	e.volume = v
	e.mu.Unlock()
	e.pushState()
}
func (e *Engine) setScan(on bool) { e.mu.Lock(); e.scanEnabled = on; e.mu.Unlock(); e.pushState() }

func (e *Engine) Start(raw string) {
	u := strings.TrimSpace(raw)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		if e.OnError != nil {
			e.OnError("Ссылка на трансляцию должна начинаться с http:// или https://")
		}
		return
	}
	e.mu.Lock()
	same := e.currentURL == u
	e.mu.Unlock()
	if same && e.sm.State() != state.Idle {
		logger.Debugf(src, "Трансляция %s уже открывается — повторный запуск пропущен", u)
		return
	}
	if e.sm.Active() {
		e.Stop()
	}
	e.mu.Lock()
	e.people, e.peakPeople, e.lowStreak, e.overFired = 0, 0, 0, false
	e.attendanceDone, e.joined = false, false
	e.currentURL, e.currentTitle = u, "Трансляция"
	e.startedAt = time.Now()
	e.scanEnabled = false
	e.mu.Unlock()
	e.pushState()
	if !e.sm.Transition(state.Starting) {
		e.sm.Reset()
		e.sm.Transition(state.Starting)
	}
	logger.Infof(src, "Открываю трансляцию: %s", u)
	e.view.Navigate(u)
}

func (e *Engine) onNavigated(ok bool, status uint32) {
	e.pushState()
	if e.sm.State() != state.Starting {
		return
	}
	if !ok {
		if status == webview.ErrOperationCanceled || status == webview.ErrConnectionAborted {
			logger.Debugf(src, "Навигация отменена (код %d) — жду следующую", status)
			return
		}
		logger.Errorf(src, "Не удалось загрузить страницу трансляции (код %d)", status)
		e.notifier.Notify(proto.EventSessionError, "Не удалось открыть трансляцию: "+e.CurrentURL(), nil)
		e.sm.Reset()
		if e.OnError != nil {
			e.OnError("Страница трансляции не загрузилась")
		}
		return
	}
	e.sm.Transition(state.StreamActive)
	e.mu.Lock()
	if e.joinTimer != nil {
		e.joinTimer.Stop()
	}
	e.joinTimer = time.AfterFunc(25*time.Second, func() {
		e.dispatch(func() {
			e.mu.Lock()
			joined := e.joined
			e.mu.Unlock()
			if e.sm.Active() && !joined {
				logger.Warnf(src, "Элементы формы входа не найдены за 25 с — считаем, что уже в комнате")
				e.onJoined("")
			}
		})
	})
	e.mu.Unlock()
}

func (e *Engine) onJoined(title string) {
	e.mu.Lock()
	if e.joined || !e.sm.Active() {
		e.mu.Unlock()
		return
	}
	e.joined = true
	if e.joinTimer != nil {
		e.joinTimer.Stop()
	}
	if title != "" {
		e.currentTitle = title
	}
	u, t := e.currentURL, e.currentTitle
	e.mu.Unlock()

	if e.sm.State() == state.Starting {
		e.sm.Transition(state.StreamActive)
	}
	e.sm.Transition(state.Scanning)
	e.setScan(true)
	e.hub.SendStreamStarted(u, t)
	e.notifier.Notify(proto.EventLectureStarted, "Лекция началась: "+t, map[string]any{"url": u, "title": t})
	if e.OnStarted != nil {
		e.OnStarted(u, t)
	}
	if e.OnTitle != nil {
		e.OnTitle(t)
	}
}

func (e *Engine) Stop() {
	if !e.sm.Active() {
		return
	}
	e.mu.Lock()
	u, t := e.currentURL, e.currentTitle
	seconds := int64(0)
	if !e.startedAt.IsZero() {
		seconds = int64(time.Since(e.startedAt).Seconds())
	}
	if e.joinTimer != nil {
		e.joinTimer.Stop()
	}
	e.currentURL = ""
	e.mu.Unlock()

	e.cancelValidation()
	e.setScan(false)
	e.view.Navigate("about:blank")
	e.sm.Reset()
	e.hub.SendStreamStopped()
	e.notifier.Notify(proto.EventLectureStopped, "Вышли с лекции: "+t+" (в лекции "+strconv.FormatInt(seconds/60, 10)+" мин)",
		map[string]any{"url": u, "seconds": seconds})
	logger.Infof(src, "Сессия остановлена")
	if e.OnStopped != nil {
		e.OnStopped(u, seconds)
	}
}

func (e *Engine) ResumeScanning() {
	if e.sm.State() == state.ManualIntervention {
		e.sm.Transition(state.Scanning)
	}
}

// DumpPage просит страницу трансляции рассказать о себе — для настройки поиска
// участников и прочих элементов на живой лекции.
func (e *Engine) DumpPage() bool {
	if !e.sm.Active() {
		return false
	}
	e.view.Eval(diagScript)
	return true
}

func (e *Engine) PublishStatus() {
	browser := "READY"
	if e.sm.Active() {
		browser = "STREAM_ACTIVE"
	}
	e.hub.SendCurrentStatus(e.sm.State().Name(), browser)
}

func (e *Engine) HandleHubCommand(msg map[string]any) {
	switch msg["type"] {
	case proto.S2CStartSession:
		u, _ := msg["url"].(string)
		if u == "" {
			logger.Warnf(src, "START_SESSION без ссылки")
			return
		}
		logger.Infof(src, "Команда из Telegram: подключиться к лекции")
		e.Start(u)
	case proto.S2CStopSession:
		logger.Infof(src, "Команда из Telegram: отключиться от лекции")
		e.Stop()
	case proto.S2CGetStatus:
		e.PublishStatus()
	case proto.S2CShutdown:
		logger.Warnf(src, "Команда из Telegram: выключить клиент")
		if e.OnShutdown != nil {
			e.OnShutdown()
		}
	}
}

func (e *Engine) onPageMessage(text string) {
	var m map[string]any
	if json.Unmarshal([]byte(text), &m) != nil {
		return
	}
	switch m["type"] {
	case "ready":
		e.pushState()
	case "log":
		level, _ := m["level"].(string)
		msg, _ := m["message"].(string)
		if e.OnPageLog != nil {
			e.OnPageLog(level, msg)
		}
	case "joined":
		title, _ := m["title"].(string)
		e.onJoined(title)
	case "title":
		if t, _ := m["title"].(string); t != "" && e.sm.Active() {
			e.mu.Lock()
			e.currentTitle = t
			e.mu.Unlock()
			if e.OnTitle != nil {
				e.OnTitle(t)
			}
		}
	case "nicknameRequired":
		if e.OnNicknameRequired != nil {
			e.OnNicknameRequired()
		}
	case "presence":
		e.hub.SendHeartbeatAck()
		e.notifier.Notify(proto.EventPresenceConfirmed, "Присутствие подтверждено: "+e.CurrentTitle(),
			map[string]any{"url": e.CurrentURL()})
		if e.OnPresence != nil {
			e.OnPresence()
		}
	case "media":
		action, _ := m["action"].(string)
		kind, _ := m["kind"].(string)
		mic, _ := m["mic"].(string)
		asked, _ := m["asked"].(float64)
		if e.OnMedia != nil {
			e.OnMedia(action, kind, mic, int(asked))
		}
	case "participants":
		n, _ := m["count"].(float64)
		e.onParticipants(int(n))
	case "ended":
		text, _ := m["text"].(string)
		e.lectureOver("страница сообщила о завершении: "+text, e.Participants(), 0)
	case "diag":
		var items []map[string]string
		var people []string
		decode(m["items"], &items)
		decode(m["people"], &people)
		u, _ := m["url"].(string)
		frames, _ := m["frames"].(float64)
		if e.OnDiag != nil {
			e.OnDiag(items, people, u, int(frames))
		}
	case "qr":
		u, _ := m["url"].(string)
		e.onQR(u)
	}
}

func allowedDomain(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	return allowedHost(p.Hostname())
}

func allowedHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, d := range allowedDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Признаки того, что лекция кончилась: народ расходится. Ошибиться тут дорого,
// поэтому уходим только когда людей было заметно много, мы уже долго внутри,
// падение большое и подтвердилось не одним замером. Без отметки присутствия
// порог выше — досрочный выход стоил бы пропуска.
const (
	crowdMinPeak       = 8
	crowdMinInside     = 10 * time.Minute
	crowdDropMarked    = 0.35
	crowdDropUnmarked  = 0.50
	crowdConfirmations = 2
)

func (e *Engine) onParticipants(n int) {
	if n < 0 {
		return
	}
	e.mu.Lock()
	changed := n != e.people
	e.people = n
	peak, inside, marked, fired := e.peakPeople, time.Since(e.startedAt), e.attendanceDone, e.overFired
	if n > e.peakPeople {
		e.peakPeople, e.lowStreak = n, 0
		peak = n
	}
	e.mu.Unlock()

	if changed && e.OnParticipants != nil {
		e.OnParticipants(n)
	}
	if fired || n >= peak || peak < crowdMinPeak || inside < crowdMinInside || e.sm.State() != state.Scanning {
		return
	}
	limit := crowdDropUnmarked
	if marked {
		limit = crowdDropMarked
	}
	drop := float64(peak-n) / float64(peak)
	if drop < limit {
		e.mu.Lock()
		e.lowStreak = 0
		e.mu.Unlock()
		return
	}
	e.mu.Lock()
	e.lowStreak++
	streak := e.lowStreak
	e.mu.Unlock()
	logger.Debugf(src, "Участников стало %d из %d (−%.0f%%), замер %d из %d", n, peak, drop*100, streak, crowdConfirmations)
	if streak >= crowdConfirmations {
		e.lectureOver(fmt.Sprintf("участников осталось %d из %d (−%.0f%%)", n, peak, drop*100), n, peak)
	}
}

// lectureOver сообщает наружу один раз за сессию: решение отключаться принимает
// приложение, движок только фиксирует признак.
func (e *Engine) lectureOver(reason string, now, peak int) {
	e.mu.Lock()
	if e.overFired || !e.sm.Active() {
		e.mu.Unlock()
		return
	}
	e.overFired = true
	e.mu.Unlock()
	logger.Infof(src, "Похоже, лекция закончилась: %s", reason)
	if e.OnLectureOver != nil {
		e.OnLectureOver(reason, now, peak)
	}
}

func (e *Engine) onQR(u string) {
	st := e.sm.State()
	if st != state.Scanning && st != state.SelfHealing && st != state.ManualIntervention {
		return
	}
	if !allowedDomain(u) {
		logger.Debugf(src, "QR с посторонним доменом пропущен: %.50s", u)
		return
	}
	e.mu.Lock()
	if e.attendanceDone || e.validating || time.Since(e.lastValidation) < 4*time.Second {
		e.mu.Unlock()
		return
	}
	e.lastValidation = time.Now()
	e.mu.Unlock()
	logger.Infof(src, "Найден QR-код отметки: %s", u)
	e.validate(u)
}

func (e *Engine) validate(u string) {
	e.mu.Lock()
	if e.validating {
		e.mu.Unlock()
		return
	}
	e.validating, e.validateURL, e.authMode = true, u, "validate"
	e.validateTimer = time.AfterFunc(6*time.Second, func() {
		e.dispatch(func() {
			logger.Infof("Отметка", "Осечка: токен не принят, но сессия авторизована")
			e.finishValidation(proto.TokenRetry)
		})
	})
	e.pollTicker = time.NewTicker(500 * time.Millisecond)
	ticker := e.pollTicker
	e.mu.Unlock()
	logger.Infof("Отметка", "Проверяю ссылку в скрытой вкладке: %.60s", u)
	e.auth.SetVisible(false)
	e.auth.Navigate(u)
	go func() {
		for range ticker.C {
			e.dispatch(func() {
				e.mu.Lock()
				active := e.validating && e.authMode == "validate"
				e.mu.Unlock()
				if active {
					e.auth.Eval(probeScript)
				}
			})
		}
	}()
}

func (e *Engine) cancelValidation() {
	e.mu.Lock()
	if e.validateTimer != nil {
		e.validateTimer.Stop()
	}
	if e.pollTicker != nil {
		e.pollTicker.Stop()
	}
	e.validating = false
	if e.authMode == "validate" {
		e.authMode = ""
	}
	e.mu.Unlock()
}

func (e *Engine) onAuthNavigated(ok bool, status uint32) {
	e.mu.Lock()
	mode := e.authMode
	e.mu.Unlock()
	if status == webview.ErrOperationCanceled || status == webview.ErrConnectionAborted {
		return
	}
	if mode == "validate" && !ok {
		logger.Warnf("Отметка", "Страница отметки не загрузилась")
		e.finishValidation(proto.TokenTimeout)
	}
	if strings.HasPrefix(mode, "sdo-") && mode != "sdo-join" {
		e.mu.Lock()
		script := e.sdoScript
		e.mu.Unlock()
		if !ok {
			e.finishSdoScan(mode, nil)
			return
		}
		time.AfterFunc(2*time.Second, func() {
			e.dispatch(func() {
				e.mu.Lock()
				active := e.authMode == mode
				e.mu.Unlock()
				if active {
					e.auth.Eval(script)
				}
			})
		})
	}
	if mode == "sdo" {
		if !ok {
			e.finishSdoProbe(false, "", 0, true)
			return
		}
		time.AfterFunc(1500*time.Millisecond, func() {
			e.dispatch(func() {
				e.mu.Lock()
				active := e.authMode == "sdo"
				e.mu.Unlock()
				if active {
					e.auth.Eval(sdoProbeScript)
				}
			})
		})
	}
	if mode == "esco" {
		if !ok {
			e.finishEscoProbe(false, "", true)
			return
		}
		time.AfterFunc(1200*time.Millisecond, func() {
			e.dispatch(func() {
				e.mu.Lock()
				active := e.authMode == "esco"
				e.mu.Unlock()
				if active {
					e.auth.Eval(escoProbeScript)
				}
			})
		})
	}
}

func (e *Engine) onAuthMessage(text string) {
	var m map[string]any
	if json.Unmarshal([]byte(text), &m) != nil {
		return
	}
	if t, _ := m["type"].(string); strings.HasPrefix(t, "sdo") && t != "sdo" {
		mode := map[string]string{
			"sdoCourses":    "sdo-courses",
			"sdoLinks":      "sdo-links",
			"sdoActivities": "sdo-activities",
			"sdoWebinars":   "sdo-webinars",
			"sdoJoin":       "sdo-join",
		}[t]
		if mode != "" {
			e.finishSdoScan(mode, m)
		}
		return
	}
	if m["type"] == "sdo" {
		name, _ := m["name"].(string)
		courses, _ := m["courses"].(float64)
		if host, _ := m["host"].(string); !e.fromExpectedPage(host) {
			logger.Debugf("СДО", "Ответ пришёл со страницы %q — жду нужную", host)
			return
		}
		e.mu.Lock()
		loggingIn := e.authMode == "login" && e.loginSystem == "sdo"
		e.mu.Unlock()
		if loggingIn {
			if m["result"] == "IN" {
				e.mu.Lock()
				e.sdoKnown, e.sdoOK, e.sdoName = true, true, name
				e.mu.Unlock()
				logger.Infof("СДО", "Вход выполнен%s — закрываю окно авторизации", nameSuffix(name))
				if e.OnLoggedIn != nil {
					e.OnLoggedIn("sdo", name)
				}
			}
			return
		}
		switch m["result"] {
		case "IN":
			e.finishSdoProbe(true, name, int(courses), false)
		case "OUT":
			e.finishSdoProbe(false, "", 0, false)
		default:
			e.finishSdoProbe(false, "", 0, true)
		}
		return
	}
	if m["type"] == "esco" {
		name, _ := m["name"].(string)
		if host, _ := m["host"].(string); !allowedHost(host) {
			logger.Debugf("ЕСКО", "Ответ проверки пришёл с чужого домена %q — игнорирую", host)
			e.finishEscoProbe(false, "", true)
			return
		}
		e.mu.Lock()
		loggingIn := e.authMode == "login" && e.loginSystem != "sdo"
		e.mu.Unlock()
		if loggingIn {
			if m["result"] == "IN" {
				e.mu.Lock()
				e.escoKnown, e.escoOK, e.escoName = true, true, name
				e.mu.Unlock()
				logger.Infof("Пульс", "Вход выполнен: %s — закрываю окно авторизации", name)
				if e.OnLoggedIn != nil {
					e.OnLoggedIn("pulse", name)
				}
			}
			return
		}
		switch m["result"] {
		case "IN":
			e.finishEscoProbe(true, name, false)
		case "OUT":
			e.finishEscoProbe(false, "", false)
		default:
			e.finishEscoProbe(false, "", true)
		}
		return
	}
	if m["type"] != "probe" {
		return
	}
	e.mu.Lock()
	active := e.validating && e.authMode == "validate"
	e.mu.Unlock()
	if !active {
		return
	}
	switch m["result"] {
	case "SUCCESS":
		logger.Infof("Отметка", "Успешная отметка!")
		e.finishValidation(proto.TokenSuccess)
	case "NEEDS_AUTH":
		logger.Warnf("Отметка", "Требуется авторизация в ЕСКО (сессия не найдена)")
		e.finishValidation(proto.TokenNeedsAuth)
	}
}

func (e *Engine) finishValidation(status string) {
	e.mu.Lock()
	if !e.validating {
		e.mu.Unlock()
		return
	}
	u := e.validateURL
	e.mu.Unlock()
	e.cancelValidation()
	e.mu.Lock()
	e.lastValidation = time.Now()
	title, cur := e.currentTitle, e.currentURL
	e.mu.Unlock()

	switch status {
	case proto.TokenSuccess:
		e.mu.Lock()
		e.attendanceDone = true
		e.escoKnown, e.escoOK = true, true
		e.mu.Unlock()
		e.hub.SendTokenResult(status, "Успешная отметка!")
		e.notifier.Notify(proto.EventAttendanceMarked, "Отметка на лекции выполнена: "+title,
			map[string]any{"url": cur, "title": title})
		if e.sm.State() == state.ManualIntervention {
			e.sm.Transition(state.Scanning)
		}
		if e.OnMarked != nil {
			e.OnMarked(u)
		}
	case proto.TokenNeedsAuth:
		e.mu.Lock()
		e.escoKnown, e.escoOK, e.escoName = true, false, ""
		e.mu.Unlock()
		e.hub.SendTokenResult(status, u)
		e.hub.SendLiveURL(u)
		e.notifier.Notify(proto.EventAuthRequired, "Требуется авторизация в ЕСКО для отметки на лекции "+title,
			map[string]any{"url": u})
		if e.sm.State() == state.Scanning {
			e.sm.Transition(state.ManualIntervention)
		}
		if e.OnAuthRequired != nil {
			e.OnAuthRequired(u)
		}
	case proto.TokenRetry:
		e.hub.SendTokenResult(status, "Осечка, пробуем снова")
	case proto.TokenTimeout:
		e.hub.SendTokenResult(status, "Таймаут при проверке отметки. Повторите позже.")
	}

	if e.OnAttendance != nil {
		e.OnAttendance(status, attendanceText(status))
	}
}

func attendanceText(status string) string {
	switch status {
	case proto.TokenSuccess:
		return "Отметка засчитана"
	case proto.TokenNeedsAuth:
		return "Не авторизованы в ЕСКО — отметка не прошла"
	case proto.TokenRetry:
		return "Осечка: код не принят, пробуем ещё"
	case proto.TokenTimeout:
		return "Страница отметки не ответила"
	default:
		return "Ошибка отметки"
	}
}

func (e *Engine) CheckEsco(u string) {
	e.mu.Lock()
	if e.authMode != "" || e.validating {
		e.mu.Unlock()
		return
	}
	e.authMode = "esco"
	if e.escoTimer != nil {
		e.escoTimer.Stop()
	}
	e.escoTimer = time.AfterFunc(15*time.Second, func() {
		e.dispatch(func() { e.finishEscoProbe(false, "", true) })
	})
	e.mu.Unlock()

	e.auth.SetVisible(false)
	e.auth.Navigate(u)
}

// CheckSdo заходит в СДО в скрытой вкладке: сессия общая с ЕСКО, поэтому
// отдельный вход не нужен — если он выполнен, система пустит сразу.
func (e *Engine) CheckSdo(u string) bool {
	e.mu.Lock()
	if e.authMode != "" || e.validating {
		e.mu.Unlock()
		return false
	}
	e.authMode = "sdo"
	e.expectHost = hostOf(u)
	if e.sdoTimer != nil {
		e.sdoTimer.Stop()
	}
	// СДО отвечает небыстро, поэтому ждём подольше: вкладку у проверки в любой
	// момент может забрать поиск ссылки.
	e.sdoTimer = time.AfterFunc(25*time.Second, func() {
		e.dispatch(func() { e.finishSdoProbe(false, "", 0, true) })
	})
	e.mu.Unlock()

	e.auth.SetVisible(false)
	e.auth.Navigate(u)
	return true
}

// hostOf — хост адреса; по нему отличаем ответ нужной страницы от ответа
// about:blank, оставшегося от предыдущей навигации.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return strings.ToLower(u.Hostname())
	}
	return ""
}

// fromExpectedPage — пришёл ли ответ со страницы, которую мы открывали.
func (e *Engine) fromExpectedPage(host string) bool {
	e.mu.Lock()
	want := e.expectHost
	e.mu.Unlock()
	if want == "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(host), want)
}

func (e *Engine) finishSdoProbe(ok bool, name string, courses int, timeout bool) {
	e.mu.Lock()
	if e.authMode != "sdo" {
		e.mu.Unlock()
		return
	}
	e.authMode = ""
	if e.sdoTimer != nil {
		e.sdoTimer.Stop()
		e.sdoTimer = nil
	}
	e.mu.Unlock()

	e.auth.Navigate("about:blank")
	switch {
	case timeout:
		logger.Debugf("СДО", "Состояние входа определить не удалось: страница не ответила или не дала признаков")
	case ok:
		logger.Infof("СДО", "Вход выполнен%s, курсов на странице: %d", nameSuffix(name), courses)
	default:
		logger.Infof("СДО", "Вход не выполнен — сначала войдите в ЕСКО МИРЭА")
	}
	if !timeout && e.OnSdoStatus != nil {
		e.OnSdoStatus(ok, name, courses)
	}
}

func nameSuffix(name string) string {
	if name == "" {
		return ""
	}
	return " (" + name + ")"
}

func (e *Engine) EscoStatus() (known, ok bool, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.escoKnown, e.escoOK, e.escoName
}

func (e *Engine) finishEscoProbe(ok bool, name string, timeout bool) {
	e.mu.Lock()
	if e.authMode != "esco" {
		e.mu.Unlock()
		return
	}
	e.authMode = ""
	if e.escoTimer != nil {
		e.escoTimer.Stop()
		e.escoTimer = nil
	}
	if !timeout {
		e.escoKnown, e.escoOK, e.escoName = true, ok, name
	}
	known := e.escoKnown
	e.mu.Unlock()

	e.auth.Navigate("about:blank")
	switch {
	case timeout:
		logger.Debugf("ЕСКО", "Проверка входа не завершилась вовремя — статус прежний")
	case ok:
		logger.Infof("ЕСКО", "Вход выполнен: %s", name)
	default:
		logger.Infof("ЕСКО", "Вход не выполнен — на странице кнопка «Войти»")
	}
	if !timeout && known && e.OnEscoStatus != nil {
		e.OnEscoStatus(ok, name)
	}
}

// BeginLogin открывает страницу входа выбранной системы: "pulse" — ЕСКО
// (отметки по QR), "sdo" — СДО (ссылки на лекции). Логины у них разные,
// поэтому и кнопки разные.
func (e *Engine) BeginLogin(u, system string) {
	e.cancelValidation()
	e.mu.Lock()
	if e.escoTimer != nil {
		e.escoTimer.Stop()
		e.escoTimer = nil
	}
	if e.sdoTimer != nil {
		e.sdoTimer.Stop()
		e.sdoTimer = nil
	}
	e.loginSystem = system
	e.authMode = "login"
	e.expectHost = hostOf(u)
	if e.loginPoll != nil {
		e.loginPoll.Stop()
	}
	e.loginPoll = time.NewTicker(2 * time.Second)
	ticker := e.loginPoll
	e.mu.Unlock()
	go func() {
		for range ticker.C {
			e.dispatch(func() {
				e.mu.Lock()
				active := e.authMode == "login"
				e.mu.Unlock()
				if active {
					e.mu.Lock()
					script := escoProbeScript
					if e.loginSystem == "sdo" {
						script = sdoProbeScript
					}
					e.mu.Unlock()
					e.auth.Eval(script)
				}
			})
		}
	}()
	e.auth.Navigate(u)
}

// LoginSystem — какую систему сейчас показываем пользователю.
func (e *Engine) LoginSystem() string { e.mu.Lock(); defer e.mu.Unlock(); return e.loginSystem }

func (e *Engine) EndLogin() {
	e.mu.Lock()
	e.authMode = ""
	e.loginSystem = ""
	e.expectHost = ""
	if e.loginPoll != nil {
		e.loginPoll.Stop()
		e.loginPoll = nil
	}
	e.mu.Unlock()
	e.auth.Navigate("about:blank")
	e.ResumeScanning()
}

func (e *Engine) AuthView() *webview.View { return e.auth }
