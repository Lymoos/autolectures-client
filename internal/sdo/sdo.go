// Package sdo — разбор СДО МИРЭА как источника ссылок на лекции.
//
// Задача делится на три части: узнать список курсов студента, найти на странице
// курса ссылки на встречи и понять, какому занятию из расписания эта ссылка
// принадлежит. Здесь лежат только чистые функции и типы: работа с браузером —
// в session, обход по расписанию — в приложении.
package sdo

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Course — курс из СДО.
type Course struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Link — найденная на странице курса ссылка на встречу.
type Link struct {
	URL     string    `json:"url"`
	Title   string    `json:"title"`   // подпись ссылки или название элемента курса
	Section string    `json:"section"` // раздел курса, где она лежит
	Course  string    `json:"course"`
	When    time.Time `json:"-"`
}

var meetingHosts = []string{
	"mts-link.ru", "mts-link.com", "webinar.ru", "my.webinar.ru",
	"telemost.yandex.ru", "zoom.us", "teams.microsoft.com", "meet.google.com",
	"jazz.sber.ru", "salutejazz.ru", "vkvideo.ru", "vk.com/video",
}

// IsMeetingLink отсеивает обычные ссылки курса: файлы, форумы, задания.
func IsMeetingLink(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	for _, h := range meetingHosts {
		if host == h || strings.HasSuffix(host, "."+h) || strings.Contains(host+u.Path, h) {
			return true
		}
	}
	return false
}

// Типы занятий и служебные пометки в названиях — для сравнения они только шум.
var titleNoise = []string{
	"лк", "пр", "лр", "лаб", "лабораторная", "лабораторные", "практика", "практики",
	"практическое", "практические", "лекция", "лекции", "семинар", "семинары",
	"занятие", "занятия", "курс", "дисциплина", "экзамен", "зачет", "консультация",
}

var stopWords = map[string]bool{
	"и": true, "в": true, "во": true, "на": true, "по": true, "для": true, "с": true,
	"со": true, "к": true, "о": true, "об": true, "из": true, "the": true, "of": true,
}

// Normalize приводит название предмета к сравнимому виду: без типа занятия,
// скобок, кода группы, года и знаков препинания.
func Normalize(title string) []string {
	t := strings.ToLower(title)
	t = strings.ReplaceAll(t, "ё", "е")
	if i := strings.IndexAny(t, "("); i > 0 {
		t = t[:i]
	}
	repl := strings.NewReplacer("«", " ", "»", " ", "\"", " ", ",", " ", ".", " ", ";", " ",
		":", " ", "-", " ", "—", " ", "–", " ", "/", " ", "\\", " ", "|", " ", "_", " ", "\n", " ")
	t = repl.Replace(t)

	var out []string
	for _, w := range strings.Fields(t) {
		if stopWords[w] || len(w) < 2 {
			continue
		}
		if isNoise(w) || isCode(w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func isNoise(w string) bool {
	for _, n := range titleNoise {
		if w == n {
			return true
		}
	}
	return false
}

// Коды групп (ИВБО-21-23), годы и прочие цифровые хвосты.
func isCode(w string) bool {
	digits, letters := 0, 0
	for _, r := range w {
		switch {
		case r >= '0' && r <= '9':
			digits++
		default:
			letters++
		}
	}
	return digits > 0 && (letters == 0 || digits >= letters)
}

// Score — насколько два названия похожи, от 0 до 1. Русские окончания
// отбрасываем сравнением по общему началу слова: «инфраструктурой» и
// «инфраструктура» должны считаться одним словом.
func Score(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	used := make([]bool, len(b))
	matched := 0
	for _, wa := range a {
		for j, wb := range b {
			if used[j] || !sameWord(wa, wb) {
				continue
			}
			used[j] = true
			matched++
			break
		}
	}
	return 2 * float64(matched) / float64(len(a)+len(b))
}

func sameWord(a, b string) bool {
	if a == b {
		return true
	}
	ra, rb := []rune(a), []rune(b)
	min := len(ra)
	if len(rb) < min {
		min = len(rb)
	}
	if min < 5 {
		return false
	}
	// достаточно совпадения основы: разница только в окончании
	for i := 0; i < min; i++ {
		if ra[i] != rb[i] {
			return false
		}
	}
	return true
}

// SubjectKey — ключ предмета для хранения ссылки на курс: «ЛК Управление ИТ»
// и «ПР Управление ИТ» ведут в один и тот же курс СДО.
func SubjectKey(title string) string {
	return strings.Join(Normalize(title), " ")
}

// MatchThreshold — ниже этого совпадение считается случайным.
const MatchThreshold = 0.5

// PickCourse выбирает курс, к которому относится занятие из расписания.
func PickCourse(lessonTitle string, courses []Course) (Course, float64) {
	want := Normalize(lessonTitle)
	best, bestScore := Course{}, 0.0
	for _, c := range courses {
		if s := Score(want, Normalize(c.Title)); s > bestScore {
			best, bestScore = c, s
		}
	}
	if bestScore < MatchThreshold {
		return Course{}, bestScore
	}
	return best, bestScore
}

// PickLink выбирает ссылку, больше всего похожую на нужное занятие: сначала по
// близости даты, затем по совпадению названия раздела или подписи.
func PickLink(lessonTitle string, start time.Time, links []Link) (Link, bool) {
	want := Normalize(lessonTitle)
	type scored struct {
		link  Link
		score float64
	}
	var list []scored
	for _, l := range links {
		if !IsMeetingLink(l.URL) {
			continue
		}
		s := Score(want, Normalize(l.Title+" "+l.Section))
		if !l.When.IsZero() && !start.IsZero() {
			switch d := l.When.Sub(start); {
			case d > -2*time.Hour && d < 2*time.Hour:
				s += 1.0
			case d > -24*time.Hour && d < 24*time.Hour:
				s += 0.4
			}
		}
		list = append(list, scored{l, s})
	}
	if len(list) == 0 {
		return Link{}, false
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	if list[0].score <= 0 {
		return Link{}, false
	}
	return list[0].link, true
}

var months = map[string]time.Month{
	"январ": time.January, "феврал": time.February, "март": time.March, "апрел": time.April,
	"мая": time.May, "май": time.May, "июн": time.June, "июл": time.July, "август": time.August,
	"сентябр": time.September, "октябр": time.October, "ноябр": time.November, "декабр": time.December,
}

var (
	reDate  = regexp.MustCompile(`(\d{1,2})[.\-/](\d{1,2})(?:[.\-/](\d{2,4}))?`)
	reWord  = regexp.MustCompile(`(?i)(\d{1,2})\s+([а-яё]{3,9})`)
	reClock = regexp.MustCompile(`(\d{1,2}):(\d{2})`)
)

// ParseWhen достаёт дату из подписи ссылки или названия раздела: «09.09»,
// «9 сентября», «Неделя 3, 9 сентября 18:00». Год берётся ближайший к занятию —
// в курсах его обычно не пишут.
func ParseWhen(text string, near time.Time) time.Time {
	if near.IsZero() {
		near = time.Now()
	}
	day, mon := 0, time.Month(0)
	if m := reDate.FindStringSubmatch(text); m != nil {
		d, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if d >= 1 && d <= 31 && mm >= 1 && mm <= 12 {
			day, mon = d, time.Month(mm)
			if m[3] != "" {
				y, _ := strconv.Atoi(m[3])
				if y < 100 {
					y += 2000
				}
				return withClock(time.Date(y, mon, day, 0, 0, 0, 0, time.Local), text)
			}
		}
	}
	if day == 0 {
		if m := reWord.FindStringSubmatch(strings.ToLower(strings.ReplaceAll(text, "ё", "е"))); m != nil {
			d, _ := strconv.Atoi(m[1])
			for prefix, month := range months {
				if strings.HasPrefix(m[2], strings.ReplaceAll(prefix, "ё", "е")) && d >= 1 && d <= 31 {
					day, mon = d, month
					break
				}
			}
		}
	}
	if day == 0 || mon == 0 {
		return time.Time{}
	}
	// без года выбираем ближайший к занятию: конец декабря и начало января
	best := time.Time{}
	for _, y := range []int{near.Year() - 1, near.Year(), near.Year() + 1} {
		t := time.Date(y, mon, day, 0, 0, 0, 0, time.Local)
		if best.IsZero() || absDur(t.Sub(near)) < absDur(best.Sub(near)) {
			best = t
		}
	}
	return withClock(best, text)
}

func withClock(day time.Time, text string) time.Time {
	if m := reClock.FindStringSubmatch(text); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		if h < 24 && mi < 60 {
			return time.Date(day.Year(), day.Month(), day.Day(), h, mi, 0, 0, time.Local)
		}
	}
	return day
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// Webinar — строка таблицы вебинаров внутри элемента курса: название, время,
// группы и кнопка действия. «Смотреть запись» нам не нужна — ждём «Подключиться».
type Webinar struct {
	Title     string    `json:"title"`
	Start     time.Time `json:"-"`
	End       time.Time `json:"-"`
	StartRaw  string    `json:"start"`
	EndRaw    string    `json:"end"`
	Groups    []string  `json:"-"`
	GroupsRaw string    `json:"groups"`
	Action    string    `json:"action"` // подпись кнопки
	URL       string    `json:"url"`    // адрес кнопки, если это ссылка
	Index     int       `json:"index"`  // номер строки — по нему кликаем кнопку
}

// Код группы всегда прописными и через дефисы: ИВБО-21-23. Регистр не смягчаем,
// иначе «семестр 26-27» превращается в группу «МЕСТР-26-27».
var reGroup = regexp.MustCompile(`([А-ЯЁ]{3,5}|[A-Z]{3,5})-(\d{2})-(\d{2})`)

// Groups достаёт коды групп из названия или ячейки таблицы.
func Groups(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reGroup.FindAllStringSubmatch(text, -1) {
		code := strings.ToUpper(m[1] + "-" + m[2] + "-" + m[3])
		code = strings.ReplaceAll(code, "Ё", "Е")
		if !seen[code] {
			seen[code] = true
			out = append(out, code)
		}
	}
	return out
}

// HasGroup — есть ли наша группа среди перечисленных. Пустой список считаем
// «для всех»: на некоторых курсах группы не проставлены.
func HasGroup(groups []string, group string) bool {
	want := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(group), "Ё", "Е"))
	if want == "" || len(groups) == 0 {
		return true
	}
	for _, g := range groups {
		if strings.ToUpper(strings.ReplaceAll(g, "Ё", "Е")) == want {
			return true
		}
	}
	return false
}

// Подключиться / Join, но не «Смотреть запись» и не «Материалы».
var (
	reJoin   = regexp.MustCompile(`(?i)подключит|присоедин|войти|join|enter|подключен`)
	reRecord = regexp.MustCompile(`(?i)запис|record|материал|презентац`)
)

// IsJoinAction отличает кнопку входа от кнопки записи.
func IsJoinAction(text string) bool {
	return reJoin.MatchString(text) && !reRecord.MatchString(text)
}

// Окно вокруг начала пары, в котором вебинар считается «этим самым».
const (
	WebinarEarly = 30 * time.Minute
	WebinarLate  = 90 * time.Minute
)

// PickWebinar выбирает строку для текущего занятия: наша группа, кнопка входа,
// время рядом с началом пары. Из подходящих берём ближайшую по времени.
func PickWebinar(rows []Webinar, group string, lessonStart time.Time) (Webinar, bool) {
	best, bestDiff, found := Webinar{}, time.Duration(1<<62), false
	for _, w := range rows {
		if !HasGroup(w.Groups, group) || !IsJoinAction(w.Action) {
			continue
		}
		diff := time.Duration(0)
		if !w.Start.IsZero() && !lessonStart.IsZero() {
			diff = w.Start.Sub(lessonStart)
			if diff < -WebinarEarly || diff > WebinarLate {
				continue
			}
			if diff < 0 {
				diff = -diff
			}
		}
		if !found || diff < bestDiff {
			best, bestDiff, found = w, diff, true
		}
	}
	return best, found
}

// PageInfo — что клиент увидел на странице курса. Нужен, чтобы в отчёте
// проверки было понятно, почему элементы не нашлись.
type PageInfo struct {
	Mods      int      `json:"mods"`      // сколько всего элементов курса на странице
	Sample    []string `json:"sample"`    // названия первых из них
	Page      string   `json:"page"`      // заголовок страницы
	Logged    bool     `json:"logged"`    // похоже, что вошли
	LoginForm bool     `json:"loginForm"` // перед нами форма входа — точно не вошли
}

// WebinarActivity — элемент курса со списком вебинаров. В названии часто стоят
// день, пара, преподаватель и группы: «УИТП Лекция - вторник, 6 пара - Даева С.Г. - ИВБО-21-23».
type WebinarActivity struct {
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Groups []string `json:"groups"`
}

// Расписание МИРЭА: начало пар. По ним название элемента («6 пара») сходится
// с занятием из расписания.
var pairStarts = []struct{ h, m int }{
	{9, 0}, {10, 40}, {12, 40}, {14, 20}, {16, 20}, {18, 0}, {19, 40},
}

// PairNumber — номер пары по времени начала, 0 если время не похоже ни на одну.
func PairNumber(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	for i, p := range pairStarts {
		slot := time.Date(t.Year(), t.Month(), t.Day(), p.h, p.m, 0, 0, t.Location())
		d := t.Sub(slot)
		if d > -20*time.Minute && d < 20*time.Minute {
			return i + 1
		}
	}
	return 0
}

var weekdays = []string{"воскресенье", "понедельник", "вторник", "среда", "четверг", "пятница", "суббота"}

// WeekdayName — «вторник» для даты занятия.
func WeekdayName(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return weekdays[int(t.Weekday())]
}

var rePair = regexp.MustCompile(`(?i)(\d)\s*пар`)

// dayFromTitle и pairFromTitle читают «вторник, 6 пара» из названия элемента.
func dayFromTitle(title string) string {
	low := strings.ToLower(strings.ReplaceAll(title, "ё", "е"))
	for _, d := range weekdays {
		base := strings.TrimSuffix(strings.TrimSuffix(d, "а"), "е")
		if strings.Contains(low, base) {
			return d
		}
	}
	return ""
}

func pairFromTitle(title string) int {
	if m := rePair.FindStringSubmatch(title); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n >= 1 && n <= len(pairStarts) {
			return n
		}
	}
	return 0
}

// Reject объясняет, почему элемент не подходит занятию. Пустая строка — подходит.
// Явное несовпадение считаем отказом: у одного предмета бывает десяток потоков,
// и лучше не найти ничего, чем подключиться к чужой лекции.
func Reject(it WebinarActivity, group string, start time.Time) string {
	groups := it.Groups
	if len(groups) == 0 {
		groups = Groups(it.Title)
	}
	if len(groups) > 0 && !HasGroup(groups, group) {
		return "другие группы: " + strings.Join(groups, " ")
	}
	if day := dayFromTitle(it.Title); day != "" {
		if lessonDay := WeekdayName(start); lessonDay != "" && day != lessonDay {
			return "другой день недели: " + day
		}
	}
	if pair := pairFromTitle(it.Title); pair > 0 {
		if lessonPair := PairNumber(start); lessonPair > 0 && pair != lessonPair {
			return fmt.Sprintf("другая пара: %d-я", pair)
		}
	}
	return ""
}

// PickActivities отбирает элементы курса, относящиеся именно к нашему занятию.
// В названии обычно стоят день недели, номер пары, преподаватель и группы —
// всё это сверяем с расписанием: у одного предмета бывает десяток потоков.
func PickActivities(items []WebinarActivity, group, lessonTitle string, start time.Time) []WebinarActivity {
	want := Normalize(lessonTitle)
	lessonDay, lessonPair := WeekdayName(start), PairNumber(start)
	type scored struct {
		item  WebinarActivity
		score float64
	}
	var list []scored
	for _, it := range items {
		if Reject(it, group, start) != "" {
			continue
		}
		groups := it.Groups
		if len(groups) == 0 {
			groups = Groups(it.Title)
		}
		day, pair := dayFromTitle(it.Title), pairFromTitle(it.Title)
		s := Score(want, Normalize(it.Title))
		if len(groups) > 0 {
			s += 3
		}
		if day != "" && day == lessonDay {
			s += 2
		}
		if pair > 0 && pair == lessonPair {
			s += 2
		}
		list = append(list, scored{it, s})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := make([]WebinarActivity, 0, len(list))
	for _, s := range list {
		out = append(out, s.item)
	}
	return out
}
