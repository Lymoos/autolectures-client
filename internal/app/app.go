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
	"github.com/Lymoos/autolectures/client/internal/sdo"
	"github.com/Lymoos/autolectures/client/internal/session"
	"github.com/Lymoos/autolectures/client/internal/state"
	"github.com/Lymoos/autolectures/client/internal/update"
	"github.com/Lymoos/autolectures/client/internal/webview"
	"github.com/Lymoos/autolectures/client/internal/win"
)

const (
	src          = "Окно"
	escoLoginURL = "https://attendance.mirea.ru/"
	// СДО пускает по той же сессии, что и ЕСКО. Пока клиент только проверяет
	// доступ: дальше отсюда будут забираться курсы и расписание лекций.
	sdoURL = "https://online-edu.mirea.ru/"
	// На сколько вперёд смотрим, подбирая занятия без ссылок.
	sdoSearchHorizon = 48 * time.Hour
	chromeUA         = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

	adminLogin = "lymoos"

	menuOpen   = 1
	menuToggle = 2
	menuQuit   = 3
)

type Assets struct {
	IndexHTML, CSS, JS string
	Scripts            []string
	Deko               string // картинка для «проспал лекцию», data:image/...
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
	loginSystem   string
	escoOK        bool
	escoName      string
	sdoOK         bool
	sdoName       string
	sdoCourses    []sdo.Course
	sdoScanFor    schedule.Entry
	search        sdoSearch
	sdoReport     []string
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
	updating      bool
	bootUpdate    bool
	updNotified   string
	needNickname  bool
	micChecked    bool
	stageWasShown bool
	tab           int
	mailStatus    string
	idleHint      string
	tgLinked      bool
	tgUser        string
	updProgress   int
	updNotes      string
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

	a := &App{opt: opt, cfg: cfg, updProgress: -1, bootUpdate: true}
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

	css := strings.Replace(assets.CSS, "/*DEKO*/", assets.Deko, 1)
	html := strings.Replace(assets.IndexHTML, "/*CSS*/", css, 1)
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
	a.scheduleSdoCheck(30 * time.Second)
	go func() {
		for range time.Tick(10 * time.Minute) {
			a.scheduleEscoCheck(0)
		}
	}()
	go func() {
		for range time.Tick(3 * time.Hour) {
			a.scheduleSdoCheck(0)
		}
	}()
	go func() {
		for range time.Tick(sdoSearchEvery) {
			a.dispatch(a.sdoTick)
		}
	}()
	go func() {
		for range time.Tick(10 * time.Minute) {
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
	// Сторож на случай, если почта или веб-сокет закрываются слишком долго:
	// процесс должен освободить файл, иначе обновление ждёт его выхода.
	watchdog := time.AfterFunc(4*time.Second, func() {
		logger.Warnf("Приложение", "Службы не остановились за 4 с — выхожу принудительно")
		os.Exit(0)
	})
	a.engine.Stop()
	a.mail.Stop()
	a.hub.Stop()
	watchdog.Stop()
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
			a.needNickname = false
			a.micChecked = false
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
		if status != proto.TokenSuccess && status != proto.TokenRetry {
			go a.scheduler.OnAttendanceFailed()
		}
		a.emitState()
	}
	a.engine.OnEscoStatus = func(ok bool, name string) {
		a.escoOK, a.escoName = ok, name
		if ok {
			a.idleHint = ""
		}
		a.emitState()
	}
	a.engine.OnSdoStatus = func(ok bool, name string, courses int) {
		a.sdoOK, a.sdoName = ok, name
		a.emitState()
		if ok {
			// Вход есть — сразу забираем список курсов, дальше по нему пойдёт
			// поиск ссылок для занятий из расписания.
			a.trySdo(2*time.Second, 6, a.engine.ScanSdoCourses)
		}
	}
	a.engine.OnSdoCourses = func(courses []sdo.Course) {
		a.sdoCourses = courses
		for _, c := range courses {
			logger.Debugf("СДО", "Курс: %s → %s", c.Title, c.URL)
		}
		a.planSdoSearch()
		a.emitState()
	}
	a.engine.OnSdoActivities = func(items []sdo.WebinarActivity, info sdo.PageInfo) { a.onSdoActivities(items, info) }
	a.engine.OnSdoWebinars = func(rows []sdo.Webinar) { a.onSdoWebinars(rows) }
	a.engine.OnSdoJoin = func(url string) { a.onSdoJoin(url) }
	a.engine.OnSdoLinks = func(course string, links []sdo.Link) {
		for _, l := range links {
			if sdo.IsMeetingLink(l.URL) {
				logger.Debugf("СДО", "«%s» → %s (%s)", l.Title, l.URL, l.Section)
			}
		}
		want := a.sdoScanFor
		if want.ID == "" {
			return
		}
		// Дата из подписи или раздела — главный признак, что ссылка именно на
		// это занятие, а не запись прошлой недели.
		for i := range links {
			links[i].When = sdo.ParseWhen(links[i].Title+" "+links[i].Section, want.Start)
		}
		// Ссылку пока не прикрепляем: сначала надо убедиться на живых курсах,
		// что кандидат выбирается верно. Прикрепление — следующий шаг.
		if best, ok := sdo.PickLink(want.Title, want.Start, links); ok {
			logger.Infof("СДО", "Кандидат для «%s» (%s): %s — «%s» из раздела «%s»",
				want.Title, want.Start.Format("02.01 15:04"), best.URL, best.Title, best.Section)
		} else {
			logger.Infof("СДО", "В курсе «%s» ссылок на встречу для «%s» не нашлось", course, want.Title)
		}
	}
	a.engine.OnLoggedIn = func(system, name string) {
		if !a.loginActive {
			return
		}
		a.loginActive, a.loginSystem = false, ""
		a.idleHint = ""
		if system == "sdo" {
			a.sdoOK, a.sdoName = true, name
		} else {
			a.escoOK, a.escoName = true, name
		}
		a.engine.EndLogin()
		a.layout()
		a.emitState()
		if system == "sdo" {
			a.trySdo(2*time.Second, 6, a.engine.ScanSdoCourses)
		} else {
			a.scheduleSdoCheck(3 * time.Second)
		}
	}
	a.engine.OnNicknameRequired = func() {
		if a.cfg.Nickname() != "" {
			return
		}
		a.needNickname = true
		logger.Warnf(src, "Форма входа просит имя участника — спрашиваю его в окне приложения")
		a.emitState()
	}
	a.engine.OnMedia = func(action, kind, mic string, asked int) {
		switch action {
		case "denied":
			logger.Warnf(src, "Трансляция запросила «%s» — доступ запрещён клиентом", kind)
		case "check":
			if asked > 0 {
				logger.Warnf(src, "Проверка устройств: попыток захвата %d, все отклонены (разрешение: %s)", asked, mic)
			} else if !a.micChecked {
				a.micChecked = true
				logger.Infof(src, "Проверка устройств: микрофон и камера недоступны странице (разрешение: %s)", mic)
			}
		}
	}
	a.engine.OnLectureOver = func(reason string, now, peak int) {
		title := a.engine.CurrentTitle()
		logger.Infof(src, "Отключаюсь от «%s»: %s", title, reason)
		a.idleHint = "Лекция «" + title + "» завершилась — " + reason
		a.notifier.Notify(proto.EventLectureStopped, "Лекция «"+title+"» закончилась: "+reason,
			map[string]any{"title": title, "participants": now, "peak": peak})
		a.engine.Stop()
		a.emitState()
	}
	a.engine.OnParticipants = func(n int) {
		logger.Debugf(src, "Участников на трансляции: %d", n)
		a.emitState()
	}
	a.engine.OnDiag = func(items []map[string]string, people []string, url string, frames int) {
		logger.Infof("Диагностика", "Страница %s, вложенных фреймов %d, кнопок и вкладок %d", url, frames, len(items))
		for _, it := range items {
			logger.Debugf("Диагностика", "%s | текст «%s» | aria «%s» | class «%s»",
				it["tag"], it["text"], it["aria"], it["cls"])
		}
		if len(people) == 0 {
			logger.Infof("Диагностика", "Ни одного элемента со словом «участники» не нашлось")
		}
		for _, p := range people {
			logger.Infof("Диагностика", "Похоже на счётчик: «%s»", p)
		}
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
	a.account.OnSettings = func() { a.dispatch(a.emitState); go a.loadSdoPreset(false) }
	a.account.OnSyncError = func(err string) { logger.Warnf("Синхронизация", "%s", err) }

	a.cfg.OnChange(func(key string) {
		switch key {
		case "nickname", "group", "mail_monitoring", "volume", "transparent_window":
			a.account.PushSettings()
		}
		a.dispatch(func() {
			switch key {
			case "nickname":
				a.engine.SetNickname(a.cfg.Nickname())
			case "group":
				go a.scheduler.Refresh()
				go a.loadSdoPreset(false)
			case "volume":
				a.engine.SetVolume(a.cfg.Volume())
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
	// Пропущенную пару отмечаем на сервере: из этого собирается общая сводка
	// по группе. Пишем только сам факт — кто это, сервер знает по токену.
	a.scheduler.OnMissed = func(e schedule.Entry) {
		if !a.account.Authorized() {
			logger.Debugf(src, "Пропуск «%s» не отправлен: нет аккаунта", e.Title)
			return
		}
		go func() {
			r := a.apiC.Post(proto.ApiWallMiss, map[string]any{
				"lesson_id":      e.ID,
				"group":          a.cfg.Group(),
				"subject":        e.Title,
				"start":          e.StartISO,
				"end":            e.EndISO,
				"seconds_inside": e.SecondsInside,
				"marked":         e.Status == proto.LinkMarked,
			})
			if r.OK {
				logger.Infof(src, "Пропуск пары «%s» отмечен на сервере", e.Title)
			} else {
				logger.Debugf(src, "Не удалось отправить пропуск «%s»: %s", e.Title, r.Err)
			}
		}()
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
	a.mail.OnSettings = func(config.Mail) { a.dispatch(a.emitState) }
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
			if a.updNotified != v {
				a.updNotified = v
				logger.Infof(src, "Доступна версия %s", v)
				a.notifier.Notify(proto.EventUpdateAvailable, "Доступно обновление клиента "+v+". "+truncate(notes, 200), map[string]any{"version": v})
			}
			a.emitState()
			// Сама ставится только версия, найденная при запуске. Дальше, пока
			// человек работает, клиент лишь показывает кнопку «Обновить».
			if a.bootUpdate {
				a.autoUpdate(v)
			}
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
	a.updater.OnRestart = func() {
		a.dispatch(func() {
			a.quitting = true
			a.wnd.Hide() // окно уходит сразу, дальше только выход процесса
			a.wnd.Quit()
		})
	}
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
	a.wnd.OnTray = a.toggleWindow
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
}

func (a *App) toggleWindow() {
	if a.wnd.Visible() {
		a.wnd.Hide()
		a.eco.SetWindowVisible(false)
		logger.Debugf(src, "Окно свёрнуто в трей, клиент продолжает работу")
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
	a.bootUpdate = false
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
	stage := a.stageShown()
	a.stage.SetBounds(x, y, max32(w, 1), max32(h, 1), stage)
	// Чёрное окно вместо трансляции — вопрос раскладки, поэтому её смену
	// записываем: видно, какого размера окно и почему оно скрыто.
	if stage != a.stageWasShown {
		a.stageWasShown = stage
		logger.Debugf(src, "Окно трансляции %s: %dx%d в (%d,%d), эко=%v (%s), вкладка=%d",
			map[bool]string{true: "показано", false: "скрыто"}[stage], w, h, x, y,
			a.eco.LowPower(), a.eco.Reason(), a.tab)
	}
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

// planSdoSearch сопоставляет ближайшие занятия без ссылок с курсами СДО. Пока
// это только план: он показывает, где именно клиент будет искать ссылку, и
// служит основой для постоянного автопоиска.
// presetState — что показать в окне курса: чей пресет лежит и сколько ссылок.
func (a *App) presetState() map[string]any {
	group, count := a.cfg.PresetInfo()
	return map[string]any{"group": group, "count": count, "own": len(a.cfg.SdoLinks())}
}

func (a *App) planSdoSearch() {
	if len(a.sdoCourses) == 0 {
		return
	}
	pending := a.scheduler.Pending(0)
	soon, matched := 0, 0
	deadline := time.Now().Add(sdoSearchHorizon)
	for _, e := range pending {
		if e.Start.Before(deadline) {
			soon++
		}
		course, score := sdo.PickCourse(e.Title, a.sdoCourses)
		if course.ID == "" {
			logger.Debugf("СДО", "Курс для «%s» не найден (лучшее совпадение %.0f%%)", e.Title, score*100)
			continue
		}
		matched++
		logger.Debugf("СДО", "«%s» %s → курс «%s» (%.0f%%)",
			e.Title, e.Start.Format("02.01 15:04"), course.Title, score*100)
	}
	logger.Infof("СДО", "Курсов %d, занятий без ссылки %d (ближайшие сутки-двое: %d), курс подобран для %d",
		len(a.sdoCourses), len(pending), soon, matched)
	a.scanNextCourse(pending)
}

// scanNextCourse заходит на страницу курса ближайшего занятия без ссылки.
// Пока это разведка: клиент показывает, какой кандидат нашёлся, но сам ссылку
// не подставляет.
func (a *App) scanNextCourse(pending []schedule.Entry) {
	for _, e := range pending {
		course, _ := sdo.PickCourse(e.Title, a.sdoCourses)
		if course.URL == "" {
			continue
		}
		a.sdoScanFor = e
		logger.Infof("СДО", "Смотрю курс «%s» ради занятия «%s»", course.Title, e.Title)
		a.trySdo(2*time.Second, 6, func(string) bool { return a.engine.ScanSdoCourse(course.Title, course.URL) })
		return
	}
}

// Скрытая вкладка одна на всех, поэтому проверка СДО может застать её за
// проверкой ЕСКО. Тогда пробуем ещё раз, а не молча пропускаем до следующего
// круга — иначе список курсов не соберётся до вечера.
func (a *App) scheduleSdoCheck(after time.Duration) { a.trySdo(after, 6, a.engine.CheckSdo) }

func (a *App) trySdo(after time.Duration, attempts int, run func(string) bool) {
	time.AfterFunc(after, func() {
		a.dispatch(func() {
			if a.quitting || a.loginActive || a.engine.Active() {
				return
			}
			if !run(sdoURL) && attempts > 1 {
				a.trySdo(20*time.Second, attempts-1, run)
			}
		})
	})
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
	if a.bootUpdate && a.updater.Available() {
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
	h("watch", func(*Call) (any, error) { a.eco.SetManual(false); return nil, nil })
	h("eco", func(c *Call) (any, error) { a.eco.SetManual(c.Bool("on")); return nil, nil })
	h("window", func(c *Call) (any, error) {
		switch c.Str("cmd") {
		case "drag":
			a.wnd.BeginDrag()
		case "minimize":
			a.wnd.Minimize()
		case "maximize":
			a.wnd.ToggleMaximize()
		case "tray":
			a.toggleWindow()
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
		return nil, nil
	})

	h("mailTest", func(c *Call) (any, error) {
		c.Async()
		s := config.Mail{Host: c.Str("host"), Port: c.Int("port"), User: c.Str("user"),
			Password: c.Str("password"), Sender: strings.TrimSpace(c.Str("sender")),
			Security: strings.TrimSpace(c.Str("security"))}
		if s.Port == 0 {
			s.Port = 993
		}
		go func() {
			// Test может вернуть исправленные настройки: порт и шифрование
			// подбираются, если заданная пара не заработала.
			ok, err := a.mail.Test(s)
			if err == nil {
				a.dispatch(func() {
					a.cfg.SetMailSettings(ok)
					a.mail.SetSettings(ok)
					logger.Infof("Почта", "Ящик %s подключён (%s:%d, %s), разбираю письма от «%s»",
						ok.User, ok.Host, ok.Port, ok.Mode(), ok.SenderFilter())
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

	// Вход в Пульс и в СДО — разные системы с разными логинами, поэтому и
	// кнопки разные: одна для отметок по QR, другая для ссылок на лекции.
	h("authBegin", func(c *Call) (any, error) {
		system := c.Str("system")
		url := escoLoginURL
		if system == "sdo" {
			url = sdoURL
		} else {
			system = "pulse"
		}
		a.loginActive, a.loginSystem = true, system
		a.engine.BeginLogin(url, system)
		a.layout()
		a.emitState()
		return nil, nil
	})
	h("authEnd", func(*Call) (any, error) {
		system := a.loginSystem
		a.loginActive, a.loginSystem = false, ""
		a.engine.EndLogin()
		a.layout()
		a.emitState()
		if system == "sdo" {
			logger.Infof(src, "Окно входа в СДО закрыто, проверяю состояние входа")
			a.scheduleSdoCheck(2 * time.Second)
		} else {
			logger.Infof(src, "Окно входа в Пульс закрыто, проверяю состояние входа")
			a.scheduleEscoCheck(2 * time.Second)
		}
		return nil, nil
	})

	h("setNickname", func(c *Call) (any, error) {
		n := strings.TrimSpace(c.Str("nickname"))
		if n == "" {
			return nil, errors.New("укажите имя, которое увидят на трансляции")
		}
		a.cfg.SetNickname(n)
		a.needNickname = false
		logger.Infof(src, "Имя участника задано: %s", n)
		a.emitState()
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
	h("pageDiag", func(*Call) (any, error) {
		if !a.isAdmin() {
			return nil, errors.New("действие доступно только администратору")
		}
		if !a.engine.DumpPage() {
			return nil, errors.New("сначала подключитесь к трансляции")
		}
		logger.Infof("Диагностика", "Снимаю срез страницы трансляции — смотрите журнал (уровень «Отладка»)")
		return nil, nil
	})
	h("sdoPresetSave", func(c *Call) (any, error) {
		if !a.isAdmin() {
			return nil, errors.New("пресеты сохраняет только администратор")
		}
		c.Async()
		group, links := a.cfg.Group(), a.cfg.SdoLinks()
		go func() { c.Reply(nil, a.saveSdoPreset(group, links)) }()
		return nil, nil
	})
	h("sdoPresetDelete", func(c *Call) (any, error) {
		if !a.isAdmin() {
			return nil, errors.New("пресеты удаляет только администратор")
		}
		c.Async()
		group := a.cfg.Group()
		go func() { c.Reply(nil, a.deleteSdoPreset(group)) }()
		return nil, nil
	})
	h("sdoPresetLoad", func(c *Call) (any, error) {
		c.Async()
		go func() { c.Reply(nil, a.loadSdoPreset(true)) }()
		return nil, nil
	})
	h("wall", func(c *Call) (any, error) {
		c.Async()
		period := c.Str("period")
		go func() { c.Reply(a.loadWall(period)) }()
		return nil, nil
	})
	h("sdoTest", func(c *Call) (any, error) {
		if !a.isAdmin() {
			return nil, errors.New("проверка поиска доступна только администратору")
		}
		return nil, a.TestSdoSearch(c.Str("id"))
	})
	h("setSdoLink", func(c *Call) (any, error) {
		title, u := c.Str("title"), strings.TrimSpace(c.Str("url"))
		if title == "" {
			return nil, errors.New("не указан предмет")
		}
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return nil, errors.New("ссылка должна начинаться с http:// или https://")
		}
		a.cfg.SetSdoLink(sdo.SubjectKey(title), u)
		if u == "" {
			logger.Infof("СДО", "Курс для «%s» убран", title)
		} else {
			logger.Infof("СДО", "Курс для «%s»: %s", title, u)
		}
		a.emitSchedule()
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

// Пресет — общий на группу набор ссылок «предмет → курс в СДО». Собирает его
// администратор, остальным он приезжает с сервера и работает как значение по
// умолчанию: своя вписанная ссылка всегда важнее.
func (a *App) loadSdoPreset(loud bool) error {
	if !a.account.Authorized() {
		if loud {
			return errors.New("пресеты доступны только с аккаунтом")
		}
		return nil
	}
	group := a.cfg.Group()
	if group == "" {
		if loud {
			return errors.New("сначала укажите группу на вкладке «Расписание»")
		}
		return nil
	}
	r := a.apiC.GetQuery(proto.ApiSdoPreset, map[string]string{"group": group})
	if !r.OK {
		if r.Status == 404 {
			a.dispatch(func() { a.cfg.SetSdoPreset(group, nil); a.emitSchedule() })
			if loud {
				return errors.New("для группы " + group + " пресета пока нет")
			}
			return nil
		}
		if loud {
			return errors.New(r.Err)
		}
		return nil
	}
	links := map[string]string{}
	if m, ok := r.Body["links"].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				links[k] = s
			}
		}
	}
	a.dispatch(func() {
		a.cfg.SetSdoPreset(group, links)
		logger.Infof("СДО", "Пресет группы %s получен: ссылок %d", group, len(links))
		a.emitSchedule()
		a.emitState()
	})
	return nil
}

func (a *App) saveSdoPreset(group string, links map[string]string) error {
	if group == "" {
		return errors.New("сначала укажите группу на вкладке «Расписание»")
	}
	if len(links) == 0 {
		return errors.New("нет ни одной своей ссылки на курс — сохранять нечего")
	}
	r := a.apiC.Put(proto.ApiSdoPreset, map[string]any{"group": group, "links": links})
	if !r.OK {
		return errors.New(r.Err)
	}
	a.dispatch(func() {
		a.cfg.SetSdoPreset(group, links)
		logger.Infof("СДО", "Пресет группы %s сохранён: ссылок %d", group, len(links))
		a.emitState()
	})
	return nil
}

func (a *App) deleteSdoPreset(group string) error {
	if group == "" {
		return errors.New("группа не указана")
	}
	r := a.apiC.DeleteQuery(proto.ApiSdoPreset, map[string]string{"group": group})
	if !r.OK && r.Status != 404 {
		return errors.New(r.Err)
	}
	a.dispatch(func() {
		a.cfg.SetSdoPreset(group, nil)
		logger.Infof("СДО", "Пресет группы %s удалён", group)
		a.emitSchedule()
		a.emitState()
	})
	return nil
}

// Сводка пропусков по своей группе. В общую выдачу сервер включает только тех,
// кто сам разрешил участие (настройка wall_public), остальные копятся молча.
func (a *App) loadWall(period string) (any, error) {
	if !a.account.Authorized() {
		return nil, errors.New("сводка доступна только с аккаунтом")
	}
	group := a.cfg.Group()
	if group == "" {
		return nil, errors.New("сначала укажите группу на вкладке «Расписание»")
	}
	if period == "" {
		period = "month"
	}
	r := a.apiC.GetQuery(proto.ApiWall, map[string]string{"group": group, "period": period})
	if !r.OK {
		return nil, errors.New(r.Err)
	}
	return r.Body, nil
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
			"participants": a.engine.Participants(),
		},
		"stageShown": a.stageShown(),
		"eco":        map[string]any{"lowPower": a.eco.LowPower(), "manual": a.eco.Manual(), "reason": a.eco.Reason()},
		"mail": map[string]any{"configured": m.Valid(), "host": m.Host, "port": m.Port, "user": m.User,
			"sender": m.SenderFilter(), "security": m.Security, "monitoring": a.cfg.MailMonitoring(),
			"status": a.mailStatus},
		"telegram": map[string]any{"linked": a.tgLinked, "username": a.tgUser},
		"esco": map[string]any{"ok": a.escoOK, "name": a.escoName,
			"loginActive": a.loginActive && a.loginSystem != "sdo"},
		"sdo": map[string]any{"ok": a.sdoOK, "name": a.sdoName, "courses": len(a.sdoCourses),
			"loginActive": a.loginActive && a.loginSystem == "sdo"},
		"attendance": map[string]any{"status": a.attendStatus, "text": a.attendText},
		"volume":     a.cfg.Volume(),
		"update":     map[string]any{"available": a.updater.Available(), "version": a.updater.Latest(), "notes": a.updNotes, "progress": a.updProgress},
		"settings": map[string]any{
			"nickname": a.cfg.Nickname(), "group": a.cfg.Group(), "server": a.cfg.ServerURL(),
		},
		"sdoTest":        map[string]any{"running": a.search.busy && a.search.test, "lines": a.sdoReport},
		"sdoPreset":      a.presetState(),
		"needNickname":   a.needNickname,
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
	entries := a.scheduler.Entries()
	courses := make(map[string]string, len(entries))
	sources := make(map[string]string, len(entries))
	for _, e := range entries {
		key := sdo.SubjectKey(e.Title)
		if u := a.cfg.SdoLink(key); u != "" {
			courses[e.ID] = u
			sources[e.ID] = a.cfg.SdoLinkSource(key)
		}
	}
	a.br.Emit("schedule", map[string]any{
		"entries": entries, "stats": a.scheduler.Stats(), "status": a.scheduler.Status(),
		"activeId": a.scheduler.ActiveID(), "group": a.cfg.Group(),
		"sdo": courses, "sdoFrom": sources,
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
