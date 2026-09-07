//go:build windows

package app

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/schedule"
	"github.com/Lymoos/autolectures/client/internal/sdo"
)

// Поиск ссылки в СДО повторяет путь студента: страница курса → элемент
// «Вебинары» → таблица занятий → кнопка «Подключиться» в строке своей группы.
// Запускается по расписанию, пока пара идёт и ссылки ещё нет.

const (
	sdoSearchEvery  = time.Minute
	sdoSearchBefore = 5 * time.Minute // начинаем искать чуть раньше звонка
	// Ссылка обычно появляется в первые минуты пары, поэтому сначала смотрим
	// часто, а дальше реже — чтобы зря не дёргать СДО всю пару.
	sdoSearchHot   = 10 * time.Minute
	sdoRetryHot    = time.Minute
	sdoRetryNormal = 3 * time.Minute
)

// retryDelay — пауза между попытками поиска для конкретного занятия.
func retryDelay(lessonStart time.Time) time.Duration {
	if time.Since(lessonStart) <= sdoSearchHot {
		return sdoRetryHot
	}
	return sdoRetryNormal
}

type sdoSearch struct {
	entryID string
	title   string
	start   time.Time
	course  string
	queue   []sdo.WebinarActivity
	busy    bool
	test    bool // проверка вручную: ссылку не подставляем, только отчитываемся
	lastTry time.Time
}

// report копит ход проверки, чтобы показать его в окне целиком.
func (a *App) report(format string, v ...any) {
	if !a.search.test {
		return
	}
	a.sdoReport = append(a.sdoReport, fmt.Sprintf(format, v...))
	a.emitState()
}

// TestSdoSearch — админская проверка: прогоняет тот же поиск для выбранного
// занятия, не дожидаясь начала пары и ничего не подключая.
func (a *App) TestSdoSearch(entryID string) error {
	if a.search.busy {
		return errors.New("поиск уже идёт, подождите")
	}
	if a.engine.Active() || a.loginActive {
		return errors.New("сначала отключитесь от текущей трансляции")
	}
	var target schedule.Entry
	for _, e := range a.scheduler.Entries() {
		if e.ID == entryID {
			target = e
			break
		}
	}
	if target.ID == "" {
		return errors.New("занятие не найдено")
	}
	course := a.cfg.SdoLink(sdo.SubjectKey(target.Title))
	if course == "" {
		return errors.New("для предмета не указана страница курса в СДО")
	}
	a.sdoReport = nil
	a.search = sdoSearch{entryID: target.ID, title: target.Title, start: target.Start,
		course: course, busy: true, test: true, lastTry: time.Now()}
	a.report("Проверяю «%s» на %s", target.Title, target.Start.Format("02.01 15:04"))
	a.report("Курс: %s", course)
	a.startScan(course, 8)
	a.emitState()
	return nil
}

// startScan ждёт освобождения скрытой вкладки: её могут занимать проверки ЕСКО
// и СДО, а проверку админ запускает руками и ждать ответа должен недолго.
func (a *App) startScan(course string, attempts int) {
	if a.engine.ScanSdoActivities(course) {
		return
	}
	if attempts <= 1 {
		a.finishSdoSearch("", "скрытая вкладка занята другой проверкой")
		return
	}
	a.report("Вкладка занята другой проверкой, жду…")
	time.AfterFunc(4*time.Second, func() {
		a.dispatch(func() {
			if a.search.busy {
				a.startScan(course, attempts-1)
			}
		})
	})
}

// sdoTick вызывается раз в минуту: выбирает идущее занятие без ссылки и
// запускает для него поиск.
func (a *App) sdoTick() {
	if a.search.busy || !a.globalSession || a.engine.Active() || a.loginActive || a.quitting {
		return
	}
	e, course, ok := a.nextSdoTarget()
	if !ok {
		return
	}
	if time.Since(a.search.lastTry) < retryDelay(e.Start) {
		return
	}
	a.search = sdoSearch{entryID: e.ID, title: e.Title, start: e.Start, course: course, busy: true, lastTry: time.Now()}
	logger.Infof("СДО", "Ищу ссылку на «%s» (%s), курс: %s", e.Title, e.Start.Format("15:04"), course)
	if !a.engine.ScanSdoActivities(course) {
		a.search.busy = false
	}
}

// nextSdoTarget — идущее сейчас занятие без ссылки, для которого указан курс СДО.
func (a *App) nextSdoTarget() (schedule.Entry, string, bool) {
	now := time.Now()
	for _, e := range a.scheduler.Pending(0) {
		if now.Before(e.Start.Add(-sdoSearchBefore)) || now.After(e.End) {
			continue
		}
		if course := a.cfg.SdoLink(sdo.SubjectKey(e.Title)); course != "" {
			return e, course, true
		}
		logger.Debugf("СДО", "Для «%s» не указана страница курса — искать негде", e.Title)
	}
	return schedule.Entry{}, "", false
}

func (a *App) onSdoActivities(items []sdo.WebinarActivity, info sdo.PageInfo) {
	if !a.search.busy {
		return
	}
	a.report("Страница: «%s»", info.Page)
	if info.LoginForm {
		a.report("Открылась форма входа — нажмите «МИРЭА СДО» слева и войдите")
		a.finishSdoSearch("", "нет входа в СДО")
		return
	}
	if !info.Logged {
		// Признаки входа у разных тем Moodle разные, поэтому это только замечание:
		// если страница курса открылась, поиск продолжается.
		a.report("Признаков входа не видно — продолжаю, посмотрим что на странице")
	}
	a.report("Элементов курса на странице: %d, похожих на вебинар: %d", info.Mods, len(items))
	if len(items) == 0 && len(info.Sample) > 0 {
		a.report("Что видно на странице: %s", strings.Join(info.Sample, "; "))
	}
	a.search.queue = sdo.PickActivities(items, a.cfg.Group(), a.search.title, a.search.start)
	if pair := sdo.PairNumber(a.search.start); pair > 0 {
		a.report("Ищу: %s, %d-я пара, группа %s", sdo.WeekdayName(a.search.start), pair, a.cfg.Group())
	}
	a.report("Подходят: %d", len(a.search.queue))
	if len(a.search.queue) == 0 {
		for _, it := range items {
			a.report("  мимо «%s» — %s", it.Title, sdo.Reject(it, a.cfg.Group(), a.search.start))
		}
		a.finishSdoSearch("", "на странице курса нет вебинаров для нашего потока")
		return
	}
	a.nextSdoActivity()
}

func (a *App) nextSdoActivity() {
	if len(a.search.queue) == 0 {
		a.finishSdoSearch("", "подходящей строки не нашлось")
		return
	}
	next := a.search.queue[0]
	a.search.queue = a.search.queue[1:]
	logger.Debugf("СДО", "Смотрю «%s»", next.Title)
	a.report("Открываю «%s»", next.Title)
	if !a.engine.ScanSdoWebinars(next.URL) {
		a.finishSdoSearch("", "вкладка занята")
	}
}

func (a *App) onSdoWebinars(rows []sdo.Webinar) {
	if !a.search.busy {
		return
	}
	for i := range rows {
		rows[i].Start = sdo.ParseWhen(rows[i].StartRaw, a.search.start)
		rows[i].End = sdo.ParseWhen(rows[i].EndRaw, a.search.start)
		if len(rows[i].Groups) == 0 {
			rows[i].Groups = sdo.Groups(rows[i].GroupsRaw)
		}
		logger.Debugf("СДО", "Строка: «%s» %s группы %s кнопка «%s»",
			rows[i].Title, rows[i].StartRaw, strings.Join(rows[i].Groups, " "), rows[i].Action)
	}
	a.report("Строк в таблице: %d", len(rows))
	for _, r := range rows {
		mark := "—"
		if sdo.IsJoinAction(r.Action) {
			mark = "вход"
		}
		if !sdo.HasGroup(r.Groups, a.cfg.Group()) {
			mark = "чужая группа"
		}
		a.report("  %s | %s | «%s» → %s", r.StartRaw, strings.Join(r.Groups, " "), r.Action, mark)
	}
	row, ok := sdo.PickWebinar(rows, a.cfg.Group(), a.search.start)
	if !ok {
		a.report("Подходящей строки здесь нет")
		a.nextSdoActivity()
		return
	}
	a.report("Выбрана строка «%s» (%s)", row.Title, row.StartRaw)
	logger.Infof("СДО", "Подходящий вебинар: «%s» (%s)", row.Title, row.StartRaw)
	if sdo.IsMeetingLink(row.URL) {
		a.finishSdoSearch(row.URL, "")
		return
	}
	if !a.engine.JoinSdoWebinar(row.Index) {
		a.finishSdoSearch("", "не удалось нажать «Подключиться»")
	}
}

func (a *App) onSdoJoin(url string) {
	if !a.search.busy {
		return
	}
	if sdo.IsMeetingLink(url) {
		a.finishSdoSearch(url, "")
		return
	}
	a.nextSdoActivity()
}

// finishSdoSearch закрывает попытку: найденную ссылку прикрепляем к занятию —
// дальше планировщик сам подключится к трансляции.
func (a *App) finishSdoSearch(url, reason string) {
	s := a.search
	a.search = sdoSearch{lastTry: time.Now()}
	if !s.busy {
		return
	}
	if s.test {
		a.search.test = true // чтобы последняя строка отчёта дошла до окна
		if url != "" {
			a.report("Готово: ссылка найдена — %s", url)
			logger.Infof("СДО", "Проверка «%s»: ссылка найдена %s", s.title, url)
		} else {
			a.report("Ссылка не найдена: %s", reason)
			logger.Infof("СДО", "Проверка «%s»: %s", s.title, reason)
		}
		a.search = sdoSearch{lastTry: time.Now()}
		a.emitState()
		return
	}
	if url == "" {
		logger.Infof("СДО", "Ссылка на «%s» пока не появилась: %s", s.title, reason)
		return
	}
	logger.Infof("СДО", "Ссылка на «%s» найдена: %s", s.title, url)
	a.scheduler.AttachFoundURL(s.entryID, url, "sdo")
	a.cfg.SetLastURL(url)
	a.idleHint = "Ссылка на «" + s.title + "» найдена в СДО — подключаюсь"
	a.emitState()
	a.emitSchedule()
}
