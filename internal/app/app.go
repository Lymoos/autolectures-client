//go:build windows

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Lymoos/autolectures/client/internal/account"
	"github.com/Lymoos/autolectures/client/internal/api"
	"github.com/Lymoos/autolectures/client/internal/config"
	"github.com/Lymoos/autolectures/client/internal/hub"
	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/mailmon"
	"github.com/Lymoos/autolectures/client/internal/notify"
	"github.com/Lymoos/autolectures/client/internal/proto"
	"github.com/Lymoos/autolectures/client/internal/schedule"
	"github.com/Lymoos/autolectures/client/internal/session"
	"github.com/Lymoos/autolectures/client/internal/state"
	"github.com/Lymoos/autolectures/client/internal/update"
	"github.com/Lymoos/autolectures/client/internal/webview"
	"github.com/Lymoos/autolectures/client/internal/win"
)

const (
	src          = "Окно"
	escoLoginURL = "https://attendance.mirea.ru/"
	chromeUA     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

	adminLogin = "lymoos"

	menuOpen   = 1
	menuToggle = 2
	menuQuit   = 3
	hotkeyID   = 0xA17E
)

type Assets struct {
	IndexHTML, CSS, JS string
	Scripts            []string
}

type Options struct {
	Version string
	Smoke   bool
}

type App struct {
	opt   Options
	cfg   *config.Config
	wnd   *win.Window
	ui    *webview.View
	stage *webview.View
	auth  *webview.View
	br    *Bridge

	apiC      *api.Client
	hub       *hub.Hub
	notifier  *notify.Notifier
	engine    *session.Engine
	account   *account.Manager
	scheduler *schedule.Scheduler
	mail      *mailmon.Monitor
	updater   *update.Updater
	eco       *Eco

	globalSession bool
	loginActive   bool
	escoOK        bool
	escoName      string
	marked        bool
	attendStatus  string
	attendText    string
	quitting      bool
	uiReady       bool
	transparent   bool
	preview       struct {
		x, y, w, h float64
		dpr        float64
	}
	updating    bool
	tab         int
	mailStatus  string
	idleHint    string
	tgLinked    bool
	tgUser      string
	updProgress int
	updNotes    string
}

func (a *App) dispatch(fn func()) { a.wnd.Dispatch(fn) }

func Run(assets Assets, opt Options) int {
	cfg := config.Get()
	logger.Init(cfg.DataDir())
	logger.Infof("Приложение", "Запуск Автолекций %s", opt.Version)
	logger.Infof("Приложение", "Каталог данных: %s", cfg.DataDir())

	if !acquireSingleInstance() {
		return 0
	}

	a := &App{opt: opt, cfg: cfg, updProgress: -1}
	a.transparent = cfg.TransparentWindow()

	webview.ChromiumFlags("--disable-background-timer-throttling --disable-renderer-backgrounding " +
		"--disable-backgrounding-occluded-windows --disable-features=IntensiveWakeUpThrottling,CalculateNativeWinOcclusion " +
		"--autoplay-policy=no-user-gesture-required")
	win.EnablePerMonitorDPI()

	a.wnd = win.New("Автолекции", 1280, 780, appIconPNG())
	acrylic := a.wnd.ApplyChrome(a.transparent)
	if a.transparent && !acrylic {
		a.transparent = false
	}
	logger.Debugf(src, "%s", map[bool]string{true: "Включён системный эффект Acrylic", false: "Окно непрозрачное"}[a.transparent])

	if a.transparent {
		// Пока идёт подготовка, окно непрозрачное: по стеклу текст не читается.
		a.wnd.ApplyChrome(false)
	}
	a.splash("Подготовка рабочего окружения", -1)
	a.wnd.Show()

	profile := filepath.Join(cfg.DataDir(), "profile")
	var err error
	if a.ui, err = a.newView(webview.Options{DataDir: profile, Transparent: a.transparent, Kiosk: true}); err != nil {
		logger.Errorf(src, "%v", err)
		return 1
	}
	if a.stage, err = a.newView(webview.Options{DataDir: profile, DenyMedia: true, Kiosk: true}); err != nil {
		logger.Errorf(src, "%v", err)
		return 1
	}
	if a.auth, err = a.newView(webview.Options{DataDir: profile, DenyMedia: true, Kiosk: true}); err != nil {
		logger.Errorf(src, "%v", err)
		return 1
	}
	// До готовности интерфейса виден только экран подготовки.
	a.ui.SetVisible(false)
	a.stage.SetUserAgent(chromeUA)
	a.auth.SetUserAgent(chromeUA)
	a.stage.SetVisible(false)
	a.auth.SetVisible(false)

	a.splash("Запуск интерфейса", -1)
	a.createServices(assets)
	a.wireWindow()
	a.wireBridge()
	a.wireServices()

	a.br = newBridge(a.ui, a.dispatch)
	a.registerHandlers()
	logger.Subscribe(func(e logger.Entry) { a.br.EmitAsync("log", e) })
	for _, e := range logger.History() {
		a.br.Emit("log", e)
	}

	html := strings.Replace(assets.IndexHTML, "/*CSS*/", assets.CSS, 1)
	html = strings.Replace(html, "/*JS*/", assets.JS, 1)
	a.ui.NavigateHTML(html)
	a.layout()
	// Если интерфейс почему-то не отзовётся, экран подготовки не должен висеть вечно.
	time.AfterFunc(12*time.Second, func() { a.dispatch(a.hideSplash) })

	a.scheduler.Load()
	go a.account.RestoreSession()
	if cfg.Group() != "" {
		go a.scheduler.Refresh()
	}
	go a.updater.Check()
	a.scheduleEscoCheck(6 * time.Second)
	go func() {
		for range time.Tick(10 * time.Minute) {
			a.scheduleEscoCheck(0)
		}
	}()
	go func() {
		for range time.Tick(6 * time.Hour) {
			a.updater.Check()
		}
	}()

	if opt.Smoke {
		time.AfterFunc(8*time.Second, func() {
			a.dispatch(func() {
				logger.Infof("Приложение", "Проверочный запуск: интерфейс %s", map[bool]string{true: "готов", false: "НЕ ОТВЕТИЛ"}[a.uiReady])
				a.quitting = true
				a.wnd.Quit()
			})
		})
	}

	a.wnd.Run()
	a.shutdown()
	if opt.Smoke && !a.uiReady {
		return 1
	}
	return 0
}

func (a *App) shutdown() {
	a.engine.Stop()
	a.mail.Stop()
	a.hub.Stop()
	logger.Infof("Приложение", "Завершение работы")
}

func (a *App) createServices(assets Assets) {
	a.apiC = api.New(a.opt.Version)
	a.hub = hub.New(a.opt.Version)
	a.notifier = notify.New(a.hub)
	a.account = account.New(a.apiC)
	a.scheduler = schedule.New(a.apiC.HTTP())
	a.mail = mailmon.New()
	a.updater = update.New(a.apiC.HTTP(), a.opt.Version, a.cfg.DataDir())
	a.eco = newEco()
	a.eco.SetEnabled(a.cfg.EcoMode())
	a.engine = session.New(a.stage, a.auth, a.hub, a.notifier, strings.Join(assets.Scripts, "\n"), a.dispatch)
	a.engine.SetNickname(a.cfg.Nickname())
	a.engine.SetVolume(a.cfg.Volume())
	a.mail.SetSettings(a.cfg.MailSettings())
}

func (a *App) wireServices() {
	a.engine.OnState = func(_, cur state.Engine) {
		active := cur != state.Idle
		a.scheduler.SetSessionActive(active)
		a.eco.SetSessionActive(active)
		if cur == state.ManualIntervention {
			a.escoOK = false
		}
		if !active {
			a.marked = false
		}
		a.emitState()
	}
	a.engine.OnStarted = func(u, t string) { go a.scheduler.OnSessionStarted(u, t); a.emitState() }
	a.engine.OnStopped = func(string, int64) { go a.scheduler.OnSessionStopped(); a.marked = false; a.emitState() }
	a.engine.OnTitle = func(string) { a.emitState() }
	a.engine.OnMarked = func(string) { a.marked = true; a.escoOK = true; go a.scheduler.OnAttendanceMarked(); a.emitState() }
	a.engine.OnAuthRequired = func(string) {
		a.escoOK = false
		a.idleHint = "Для отметки требуется вход в ЕСКО — нажмите «ЕСКО МИРЭА» слева"
		a.emitState()
	}
	a.engine.OnAttendance = func(status, text string) {
		a.attendStatus, a.attendText = status, text
		a.emitState()
	}
	a.engine.OnEscoStatus = func(ok bool, name string) {
		a.escoOK, a.escoName = ok, name
		if ok {
			a.idleHint = ""
		}
		a.emitState()
	}
	a.engine.OnEscoLoggedIn = func(name string) {
		if !a.loginActive {
			return
		}
		a.loginActive = false
		a.escoOK, a.escoName = true, name
		a.idleHint = ""
		a.engine.EndLogin()
		a.layout()
		a.emitState()
	}
	a.engine.OnNicknameRequired = func() {
		logger.Warnf(src, "Укажите имя участника в параметрах (Аккаунт → Параметры)")
	}
	a.engine.OnError = func(msg string) { a.idleHint = msg; a.emitState() }
	a.engine.OnShutdown = func() { a.quitting = true; a.wnd.Quit() }
	a.engine.OnPageLog = func(level, msg string) {
		lv := logger.Info
		switch level {
		case "debug":
			lv = logger.Debug
		case "warn":
			lv = logger.Warn
		case "error":
			lv = logger.Error
		}
		logger.Log(lv, "Страница", msg)
	}

	a.eco.OnChange = func(bool) { a.layout(); a.emitState() }

	a.hub.OnConnection = func(online bool) {
		a.dispatch(func() {
			if online {
				a.engine.PublishStatus()
				go a.refreshTelegram()
			}
			a.emitState()
		})
	}
	a.hub.OnCommand = func(msg map[string]any) { a.dispatch(func() { a.engine.HandleHubCommand(msg) }) }
	a.hub.OnSettings = func(s map[string]any) { a.dispatch(func() { a.cfg.ApplySynced(s); a.emitState() }) }
	a.hub.OnAuthFailed = func(string) { a.dispatch(a.emitState) }

	a.account.OnChanged = func() {
		a.dispatch(func() {
			if !a.account.Authorized() {
				a.tgLinked, a.tgUser = false, ""
				if a.globalSession {
					a.hub.Stop()
				}
			} else if a.globalSession {
				a.hub.Stop()
				a.hub.Start()
			}
			go a.refreshTelegram()
			a.emitState()
		})
	}
	a.account.OnLinksPulled = func(links []json.RawMessage) { a.scheduler.ApplyRemote(links) }
	a.account.OnSettings = func() { a.dispatch(a.emitState) }
	a.account.OnSyncError = func(err string) { logger.Warnf("Синхронизация", "%s", err) }

	a.cfg.OnChange(func(key string) {
		switch key {
		case "nickname", "group", "mail_monitoring", "volume", "boss_key", "transparent_window":
			a.account.PushSettings()
		}
		a.dispatch(func() {
			switch key {
			case "nickname":
				a.engine.SetNickname(a.cfg.Nickname())
			case "group":
				go a.scheduler.Refresh()
			case "volume":
				a.engine.SetVolume(a.cfg.Volume())
			case "boss_key":
				a.registerHotKey()
			}
			a.emitState()
		})
	})

	a.scheduler.OnChanged = func() { a.dispatch(a.emitSchedule) }
	a.scheduler.OnStatus = func(string) { a.dispatch(a.emitSchedule) }
	a.scheduler.OnLinks = func(links []json.RawMessage) { go a.account.PushLinks(links) }
	a.scheduler.OnAutoStart = func(u, t string) {
		a.dispatch(func() {
			logger.Infof(src, "Планировщик: подключение к «%s»", t)
			a.engine.Start(u)
		})
	}
	a.scheduler.OnAutoStop = func() { a.dispatch(a.engine.Stop) }
	a.scheduler.OnLinkWait = func(title string) {
		a.dispatch(func() {
			a.idleHint = "Лекция «" + title + "» началась — жду ссылку из почты"
			a.emitState()
		})
		a.mail.CheckNow()
	}
	a.scheduler.OnLinkMissing = func(title string, start time.Time) {
		a.dispatch(func() {
			a.idleHint = "Ссылка на «" + title + "» не пришла — вставьте её слева или отправьте боту"
			a.emitState()
		})
		a.notifier.Notify(proto.EventLinkMissing, title,
			map[string]any{"title": title, "start": start.Format(time.RFC3339)})
	}

	a.mail.OnStatus = func(t string) { a.dispatch(func() { a.mailStatus = t; a.emitState() }) }
	a.mail.OnError = func(t string) { a.dispatch(func() { a.mailStatus = "Ошибка: " + t; a.emitState() }) }
	a.mail.OnInvitation = func(inv mailmon.Invitation, subject string) {
		a.scheduler.AddInvitation(inv, subject)
		title := inv.Title
		if title == "" {
			title = subject
		}
		if strings.TrimSpace(title) == "" {
			title = "Лекция"
		}
		a.notifier.Notify(proto.EventEmailParsed, title,
			map[string]any{"url": inv.URL, "title": title, "when": inv.When.Format(time.RFC3339)})
	}

	a.updater.OnFound = func(v, notes string) {
		a.dispatch(func() {
			a.updNotes = notes
			a.notifier.Notify(proto.EventUpdateAvailable, "Доступно обновление клиента "+v+". "+truncate(notes, 200), map[string]any{"version": v})
			a.emitState()
			a.autoUpdate(v)
		})
	}
	a.updater.OnProgress = func(p int) {
		a.dispatch(func() {
			a.updProgress = p
			if a.updating {
				a.splash("Обновление до "+a.updater.Latest(), p)
			}
			a.emitState()
		})
	}
	a.updater.OnFailed = func(msg string) {
		a.dispatch(func() {
			a.updProgress = -1
			if a.updating {
				a.updating = false
				a.hideSplash()
				a.idleHint = "Обновление не установилось: " + msg + ". Попробуйте кнопку «Обновить»"
			}
			a.emitState()
		})
	}
	a.updater.OnRestart = func() { a.dispatch(func() { a.quitting = true; a.wnd.Quit() }) }
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (a *App) refreshTelegram() {
	linked, user := a.account.TelegramStatus()
	a.dispatch(func() { a.tgLinked, a.tgUser = linked, user; a.emitState() })
}

func (a *App) wireWindow() {
	a.wnd.OnResize = func(int32, int32) { a.layout() }
	a.wnd.OnDpi = func(int) { a.layout() }
	a.wnd.OnMinimize = func(min bool) { a.eco.SetWindowVisible(!min) }
	a.wnd.OnClose = func() bool {
		a.quitting = true
		return true
	}
	a.wnd.OnHotKey = func(int) { a.toggleBossKey() }
	a.wnd.OnTray = a.toggleBossKey
	a.wnd.OnMenu = func(id int) {
		switch id {
		case menuOpen:
			a.restore()
		case menuToggle:
			if a.globalSession {
				a.stopGlobalSession()
			} else {
				a.startGlobalSession(a.cfg.LastURL())
			}
		case menuQuit:
			a.quitting = true
			a.wnd.Quit()
		}
	}
	a.wnd.AddTray("Автолекции — работает в фоне", []struct {
		ID    int
		Title string
	}{{menuOpen, "Открыть окно"}, {menuToggle, "Запустить сессию"}, {0, ""}, {menuQuit, "Выход"}})
	a.registerHotKey()
}

func (a *App) registerHotKey() {
	win.UnregisterHotKey(a.wnd.HWnd, hotkeyID)
	seq := a.cfg.BossKey()
	mods, vk, ok := win.ParseHotKey(seq)
	if !ok {
		logger.Warnf("Горячая клавиша", "Комбинация %s не поддерживается", seq)
		return
	}
	if win.RegisterHotKey(a.wnd.HWnd, hotkeyID, mods, vk) {
		logger.Infof("Горячая клавиша", "Boss Key: %s", seq)
	} else {
		logger.Warnf("Горячая клавиша", "Не удалось зарегистрировать %s (занята другой программой?)", seq)
	}
}

func (a *App) toggleBossKey() {
	if a.wnd.Visible() {
		a.wnd.Hide()
		a.eco.SetWindowVisible(false)
		logger.Debugf(src, "Окно скрыто в трей (Boss Key)")
	} else {
		a.restore()
	}
}

func (a *App) restore() {
	a.wnd.Restore()
	a.eco.SetWindowVisible(true)
}

// Клиент обновляется сам: пользователь мог не заходить неделю, и ловить его
// кнопкой «Обновить» бессмысленно. Посреди лекции не лезем — только когда
// сессия не запущена.
func (a *App) autoUpdate(version string) {
	if a.opt.Smoke || a.updating || a.quitting || a.updProgress >= 0 {
		return
	}
	if a.globalSession || a.engine.Active() {
		logger.Infof(src, "Обновление %s поставится после окончания сессии", version)
		return
	}
	a.updating = true
	a.updProgress = 0
	logger.Infof(src, "Ставлю обновление %s автоматически", version)
	a.showSplash("Обновление до " + version)
	a.emitState()
	go a.updater.DownloadAndInstall()
}

func (a *App) showSplash(text string) {
	if !a.wnd.SplashActive() && a.transparent {
		a.wnd.ApplyChrome(false)
	}
	a.splash(text, 0)
	a.layout()
}

func (a *App) hideSplash() {
	if !a.wnd.SplashActive() {
		return
	}
	a.wnd.HideSplash()
	if a.transparent {
		a.wnd.ApplyChrome(true)
	}
	// Сначала стираем экран подготовки, пока окна WebView2 ещё скрыты: прозрачная
	// страница показывает поверхность родителя, и старые пиксели остались бы видны.
	a.wnd.Redraw()
	a.layout()
}

func (a *App) layout() {
	r := win.GetClientRect(a.wnd.HWnd)
	b := a.wnd.Border()
	shown := !a.wnd.SplashActive()
	a.ui.SetBounds(b, b, max32(r.Width()-2*b, 1), max32(r.Height()-2*b, 1), shown)

	x, y, w, h, haveRect := a.previewBounds(b)
	showLogin := shown && haveRect && a.loginActive
	a.auth.SetBounds(x, y, max32(w, 1), max32(h, 1), showLogin)
	a.stage.SetBounds(x, y, max32(w, 1), max32(h, 1), a.stageShown())
}

func (a *App) previewBounds(b int32) (x, y, w, h int32, ok bool) {
	p := a.preview
	dpr := p.dpr
	if dpr == 0 {
		dpr = float64(win.DpiForWindow(a.wnd.HWnd)) / 96
	}
	x, y = b+int32(p.x*dpr), b+int32(p.y*dpr)
	w, h = int32(p.w*dpr), int32(p.h*dpr)
	return x, y, w, h, w > 10 && h > 10 && a.tab == 0
}

func (a *App) scheduleEscoCheck(after time.Duration) {
	time.AfterFunc(after, func() {
		a.dispatch(func() {
			if a.quitting || a.loginActive {
				return
			}
			a.engine.CheckEsco(escoLoginURL)
		})
	})
}

func (a *App) stageShown() bool {
	if a.wnd.SplashActive() {
		return false
	}
	_, _, _, _, haveRect := a.previewBounds(a.wnd.Border())
	return haveRect && !a.loginActive && a.engine.Active() && !a.eco.LowPower()
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func (a *App) startGlobalSession(u string) {
	if a.globalSession {
		return
	}
	a.globalSession = true
	a.wnd.SetTrayItemTitle(menuToggle, "Остановить сессию")
	mode := "гостевой режим"
	if a.account.Authorized() {
		mode = "аккаунт " + a.account.Login()
		a.hub.Start()
	}
	logger.Infof(src, "Глобальная сессия запущена (%s)", mode)
	a.scheduler.SetArmed(true)
	go a.scheduler.Refresh()
	if a.cfg.MailMonitoring() && a.cfg.MailSettings().Valid() {
		a.mail.Start()
	}
	u = strings.TrimSpace(u)
	if u != "" {
		a.cfg.SetLastURL(u)
		a.scheduler.AttachManualURL(u, "")
		a.engine.Start(u)
	} else {
		a.idleHint = "Сессия активна: жду ссылку из расписания, почты или Telegram"
	}
	a.emitState()
}

func (a *App) stopGlobalSession() {
	if !a.globalSession {
		return
	}
	a.globalSession = false
	a.wnd.SetTrayItemTitle(menuToggle, "Запустить сессию")
	a.engine.Stop()
	a.mail.Stop()
	a.scheduler.SetArmed(false)
	a.hub.Stop()
	a.idleHint = ""
	logger.Infof(src, "Глобальная сессия остановлена")
	a.emitState()
	// Обновление, отложенное из-за лекции, ставим сразу после её конца.
	if a.updater.Available() {
		a.autoUpdate(a.updater.Latest())
	}
}

func (a *App) wireBridge() {}

func (a *App) registerHandlers() {
	h := a.br.Handle
	h("ready", func(*Call) (any, error) {
		a.uiReady = true
		a.hideSplash()
		a.emitState()
		a.emitSchedule()
		return nil, nil
	})
	h("getState", func(*Call) (any, error) { return a.snapshot(), nil })
	h("start", func(c *Call) (any, error) { a.startGlobalSession(c.Str("url")); return nil, nil })
	h("stop", func(*Call) (any, error) { a.stopGlobalSession(); return nil, nil })
	h("submitUrl", func(c *Call) (any, error) {
		u := c.Str("url")
		if u == "" {
			return nil, nil
		}
		a.cfg.SetLastURL(u)
		if !a.globalSession {
			a.startGlobalSession(u)
		} else {
			a.engine.Start(u)
		}
		return nil, nil
	})
	h("setUrl", func(c *Call) (any, error) { a.cfg.SetLastURL(c.Str("url")); return nil, nil })
	h("setVolume", func(c *Call) (any, error) {
		v := c.Int("volume")
		a.engine.SetVolume(v)
		a.cfg.SetVolume(v)
		return nil, nil
	})
	h("tab", func(c *Call) (any, error) {
		a.tab = c.Int("index")
		a.eco.SetPreviewTab(a.tab == 0)
		a.layout()
		return nil, nil
	})
	h("watch", func(*Call) (any, error) { a.eco.SetUserEntered(true); return nil, nil })
	h("window", func(c *Call) (any, error) {
		switch c.Str("cmd") {
		case "drag":
			a.wnd.BeginDrag()
		case "minimize":
			a.wnd.Minimize()
		case "maximize":
			a.wnd.ToggleMaximize()
		case "tray":
			a.toggleBossKey()
		case "close":
			a.wnd.Close()
		}
		return nil, nil
	})
	h("previewRect", func(c *Call) (any, error) {
		a.preview.x, a.preview.y = c.Float("x"), c.Float("y")
		a.preview.w, a.preview.h, a.preview.dpr = c.Float("w"), c.Float("h"), c.Float("dpr")
		a.layout()
		return nil, nil
	})
	h("overlay", func(c *Call) (any, error) { a.eco.SetOverlay(c.Bool("open")); return nil, nil })

	h("login", func(c *Call) (any, error) {
		c.Async()
		login, pass := c.Str("login"), c.Str("password")
		go func() { c.Reply(nil, a.account.SignIn(login, pass)) }()
		return nil, nil
	})
	h("register", func(c *Call) (any, error) {
		c.Async()
		login, pass := c.Str("login"), c.Str("password")
		go func() { c.Reply(nil, a.account.SignUp(login, pass)) }()
		return nil, nil
	})
	h("guest", func(*Call) (any, error) { go a.account.ContinueAsGuest(); return nil, nil })
	h("logout", func(*Call) (any, error) { go a.account.SignOut(); return nil, nil })
	h("saveSettings", func(c *Call) (any, error) {
		a.cfg.SetNickname(c.Str("nickname"))
		if s := c.Str("server"); s != "" {
			a.cfg.SetServerURL(s)
		}
		a.cfg.SetBossKey(c.Str("bossKey"))
		return nil, nil
	})

	h("mailTest", func(c *Call) (any, error) {
		c.Async()
		s := config.Mail{Host: c.Str("host"), Port: c.Int("port"), User: c.Str("user"),
			Password: c.Str("password"), Sender: strings.TrimSpace(c.Str("sender"))}
		if s.Port == 0 {
			s.Port = 993
		}
		go func() {
			err := a.mail.Test(s)
			if err == nil {
				a.dispatch(func() {
					a.cfg.SetMailSettings(s)
					a.mail.SetSettings(s)
					logger.Infof("Почта", "Ящик %s подключён, разбираю письма от «%s»", s.User, s.SenderFilter())
					if a.globalSession && a.cfg.MailMonitoring() {
						a.mail.Start()
					}
					a.emitState()
				})
			}
			c.Reply(nil, err)
		}()
		return nil, nil
	})
	h("mailDisconnect", func(*Call) (any, error) {
		a.mail.Stop()
		a.cfg.SetMailSettings(config.Mail{Host: "imap.mail.ru", Port: 993})
		a.cfg.SetMailMonitoring(false)
		a.cfg.SetMailLastUID(0)
		a.mail.SetSettings(config.Mail{})
		a.mailStatus = ""
		a.emitState()
		return nil, nil
	})
	h("mailToggle", func(c *Call) (any, error) {
		on := c.Bool("on")
		a.cfg.SetMailMonitoring(on)
		switch {
		case !a.globalSession:
			a.mailStatus = map[bool]string{true: "Запустится вместе с сессией («Старт»)", false: "Мониторинг выключен"}[on]
		case on:
			a.mail.Start()
		default:
			a.mail.Stop()
		}
		a.emitState()
		return nil, nil
	})

	h("telegramCode", func(c *Call) (any, error) {
		c.Async()
		go func() {
			code, link, err := a.account.TelegramCode()
			c.Reply(map[string]any{"code": code, "deepLink": link}, err)
		}()
		return nil, nil
	})
	h("telegramStatus", func(c *Call) (any, error) {
		c.Async()
		go func() {
			linked, user := a.account.TelegramStatus()
			a.dispatch(func() { a.tgLinked, a.tgUser = linked, user; a.emitState() })
			c.Reply(map[string]any{"linked": linked, "username": user}, nil)
		}()
		return nil, nil
	})

	h("escoBegin", func(*Call) (any, error) {
		a.loginActive = true
		a.engine.BeginLogin(escoLoginURL)
		a.layout()
		a.emitState()
		return nil, nil
	})
	h("escoEnd", func(*Call) (any, error) {
		a.loginActive = false
		a.engine.EndLogin()
		a.layout()
		logger.Infof(src, "Окно авторизации ЕСКО закрыто, проверяю состояние входа")
		a.emitState()
		a.scheduleEscoCheck(2 * time.Second)
		return nil, nil
	})

	h("setGroup", func(c *Call) (any, error) {
		g := c.Str("group")
		if g == a.cfg.Group() {
			return nil, nil
		}
		a.cfg.SetGroup(g)
		if a.cfg.Group() == "" {
			logger.Infof(src, "Группа очищена, расписание МИРЭА больше не обновляется")
		} else {
			logger.Infof(src, "Группа изменена на %s", a.cfg.Group())
		}
		return nil, nil
	})
	h("scheduleRefresh", func(*Call) (any, error) { go a.scheduler.RefreshNow(); return nil, nil })
	h("scheduleRemove", func(c *Call) (any, error) {
		if a.scheduler.Remove(c.Str("id")) > 0 {
			logger.Infof(src, "Занятие удалено из списка")
		}
		return nil, nil
	})
	h("connect", func(c *Call) (any, error) {
		u, t := c.Str("url"), c.Str("title")
		if !a.globalSession {
			a.startGlobalSession("")
		}
		a.scheduler.AttachManualURL(u, t)
		a.engine.Start(u)
		return nil, nil
	})
	h("openUrl", func(c *Call) (any, error) { win.OpenURL(c.Str("url")); return nil, nil })
	h("copyText", func(c *Call) (any, error) { win.SetClipboardText(a.wnd.HWnd, c.Str("text")); return nil, nil })
	h("copyLog", func(*Call) (any, error) {
		win.SetClipboardText(a.wnd.HWnd, logger.HistoryText())
		logger.Infof("Журнал", "Журнал скопирован в буфер обмена")
		return nil, nil
	})
	h("onboardingDone", func(*Call) (any, error) { a.cfg.SetOnboardingDone(true); return nil, nil })
	h("adminOnboarding", func(*Call) (any, error) {
		if !a.isAdmin() {
			return nil, errors.New("действие доступно только администратору")
		}
		a.cfg.SetOnboardingDone(false)
		logger.Infof(src, "Обучение запущено заново")
		a.emitState()
		return nil, nil
	})
	h("adminReset", func(*Call) (any, error) {
		if !a.isAdmin() {
			return nil, errors.New("действие доступно только администратору")
		}
		return nil, a.resetEverything()
	})
	h("update", func(*Call) (any, error) {
		if a.updating {
			return nil, nil
		}
		a.updating = true
		a.updProgress = 0
		a.showSplash("Обновление до " + a.updater.Latest())
		a.emitState()
		go a.updater.DownloadAndInstall()
		return nil, nil
	})
}

func (a *App) isAdmin() bool {
	return a.account.Authorized() && strings.EqualFold(a.account.Login(), adminLogin)
}

// resetEverything сносит конфиг и профиль браузера и поднимает клиент заново:
// удалять занятые файлы может только следующий процесс, поэтому чистка
// выполняется при старте с ключом --reset.
func (a *App) resetEverything() error {
	self, err := os.Executable()
	if err != nil {
		return errors.New("не удалось определить путь к приложению")
	}
	a.stopGlobalSession()
	a.engine.Stop()
	a.mail.Stop()
	cmd := exec.Command(self, "--reset", strconv.Itoa(os.Getpid()))
	cmd.Dir = filepath.Dir(self)
	if err := cmd.Start(); err != nil {
		return errors.New("не удалось перезапустить клиент: " + err.Error())
	}
	logger.Warnf(src, "Сброс данных: конфиг и профиль будут удалены, клиент перезапускается")
	a.quitting = true
	a.wnd.Quit()
	return nil
}

func (a *App) snapshot() map[string]any {
	m := a.cfg.MailSettings()
	return map[string]any{
		"version":       a.opt.Version,
		"transparent":   a.transparent,
		"admin":         a.isAdmin(),
		"account":       map[string]any{"guest": !a.account.Authorized(), "login": a.account.Login()},
		"connection":    map[string]any{"online": a.hub.Connected()},
		"globalSession": a.globalSession,
		"url":           a.cfg.LastURL(),
		"idleHint":      a.idleHint,
		"session": map[string]any{
			"active": a.engine.Active(), "state": a.engine.State().Title(), "title": a.engine.CurrentTitle(),
			"seconds": a.engine.Seconds(), "marked": a.marked, "url": a.engine.CurrentURL(),
		},
		"stageShown": a.stageShown(),
		"eco":        map[string]any{"lowPower": a.eco.LowPower(), "reason": a.eco.Reason()},
		"mail": map[string]any{"configured": m.Valid(), "host": m.Host, "port": m.Port, "user": m.User,
			"sender": m.SenderFilter(), "monitoring": a.cfg.MailMonitoring(), "status": a.mailStatus},
		"telegram":   map[string]any{"linked": a.tgLinked, "username": a.tgUser},
		"esco":       map[string]any{"ok": a.escoOK, "loginActive": a.loginActive, "name": a.escoName},
		"attendance": map[string]any{"status": a.attendStatus, "text": a.attendText},
		"volume":     a.cfg.Volume(),
		"update":     map[string]any{"available": a.updater.Available(), "version": a.updater.Latest(), "notes": a.updNotes, "progress": a.updProgress},
		"settings": map[string]any{
			"nickname": a.cfg.Nickname(), "group": a.cfg.Group(), "server": a.cfg.ServerURL(), "bossKey": a.cfg.BossKey(),
		},
		"onboardingDone": a.cfg.OnboardingDone(),
	}
}

func (a *App) emitState() {
	if a.br != nil {
		a.br.Emit("state", a.snapshot())
	}
}

func (a *App) emitSchedule() {
	if a.br == nil {
		return
	}
	a.br.Emit("schedule", map[string]any{
		"entries": a.scheduler.Entries(), "stats": a.scheduler.Stats(), "status": a.scheduler.Status(),
		"activeId": a.scheduler.ActiveID(), "group": a.cfg.Group(),
	})
}

var (
	user32      = windows.NewLazySystemDLL("user32.dll")
	pFindWindow = user32.NewProc("FindWindowW")
)

func acquireSingleInstance() bool {
	name, _ := windows.UTF16PtrFromString("Local\\AutolecturesSingleInstance")
	_, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		cls, _ := windows.UTF16PtrFromString("AutolecturesHost")
		hwnd, _, _ := pFindWindow.Call(uintptr(unsafe.Pointer(cls)), 0)
		if hwnd != 0 {
			win.ShowWindow(hwnd, win.SW_RESTORE)
			win.SetForegroundWindow(hwnd)
		}
		return false
	}
	return true
}

func appIconPNG() []byte {
	const n = 32
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	accent := color.RGBA{122, 162, 247, 255}
	dark := color.RGBA{24, 26, 31, 255}
	c := float64(n) / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)+.5-c, float64(y)+.5-c
			if dx*dx+dy*dy <= (c-1)*(c-1) {
				img.Set(x, y, accent)
			}
		}
	}
	for y := 10; y < 22; y++ {
		half := float64(y-16) * 0.5
		if half < 0 {
			half = -half
		}
		for x := 12; x < 22-int(half*2)+1; x++ {
			img.Set(x, y, dark)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func dataFile(name string) string { return filepath.Join(config.Get().DataDir(), name) }

var _ = dataFile
var _ = os.Getenv
