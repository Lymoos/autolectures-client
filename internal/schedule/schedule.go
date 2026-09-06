package schedule

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Lymoos/autolectures/client/internal/config"
	"github.com/Lymoos/autolectures/client/internal/ical"
	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/mailmon"
	"github.com/Lymoos/autolectures/client/internal/proto"
)

const (
	src          = "Расписание"
	horizonDays  = 14
	matchTolMin  = 25
	lessonLength = 90 * time.Minute
	linkWait     = 15 * time.Minute
)

type Entry struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Start         time.Time `json:"-"`
	End           time.Time `json:"-"`
	StartISO      string    `json:"start"`
	EndISO        string    `json:"end"`
	Location      string    `json:"location"`
	Teacher       string    `json:"teacher"`
	URL           string    `json:"url"`
	Source        string    `json:"source"`
	Status        string    `json:"status"`
	SecondsInside int64     `json:"seconds_inside"`
	MarkedAt      string    `json:"marked_at"`
	LinkAsked     bool      `json:"link_asked,omitempty"`
	autoStarted   bool
	linkWaiting   bool
}

func (e *Entry) sync() {
	e.StartISO = e.Start.Format("2006-01-02T15:04:05")
	e.EndISO = e.End.Format("2006-01-02T15:04:05")
}

func parseISO(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05", time.RFC3339, "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

func fromRaw(raw json.RawMessage) (Entry, bool) {
	var e Entry
	if json.Unmarshal(raw, &e) != nil || e.ID == "" {
		return e, false
	}
	e.Start, e.End = parseISO(e.StartISO), parseISO(e.EndISO)
	if e.Source == "" {
		e.Source = "cloud"
	}
	if e.Status == "" {
		e.Status = proto.LinkPending
	}
	return e, true
}

type Stats struct {
	WeekLessons   int   `json:"week_lessons"`
	Attended      int   `json:"attended"`
	Marked        int   `json:"marked"`
	SecondsInside int64 `json:"seconds_inside"`
}

type Scheduler struct {
	http *http.Client

	mu            sync.Mutex
	entries       []Entry
	status        string
	activeID      string
	armed         bool
	sessionActive bool
	refreshing    bool
	lastRefresh   time.Time
	lastGroup     string

	OnChanged     func()
	OnStatus      func(text string)
	OnLinks       func([]json.RawMessage)
	OnAutoStart   func(url, title string)
	OnAutoStop    func()
	OnLinkWait    func(title string)
	OnLinkMissing func(title string, start time.Time)
}

func New(h *http.Client) *Scheduler {
	s := &Scheduler{http: h}
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for range t.C {
			s.tick()
		}
	}()
	return s
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:10]
}

func (s *Scheduler) Load() {
	s.mu.Lock()
	s.entries = nil
	dropped := 0
	cutoff := time.Now().AddDate(0, 0, -30)
	for _, raw := range config.Get().Links() {
		e, ok := fromRaw(raw)
		if !ok || (!e.End.IsZero() && !e.End.After(cutoff)) {
			continue
		}
		if !keepEntry(e) {
			dropped++
			continue
		}
		s.entries = append(s.entries, e)
	}
	s.sortLocked()
	s.mu.Unlock()
	if dropped > 0 {
		logger.Infof(src, "Из сохранённого списка убрано %d записей: очные пары и пометки недель", dropped)
		s.persist()
		return
	}
	s.changed()
}

func (s *Scheduler) sortLocked() {
	sort.SliceStable(s.entries, func(i, j int) bool { return s.entries[i].Start.Before(s.entries[j].Start) })
}

func (s *Scheduler) snapshotLocked() []json.RawMessage {
	out := make([]json.RawMessage, 0, len(s.entries))
	for i := range s.entries {
		s.entries[i].sync()
		if raw, err := json.Marshal(s.entries[i]); err == nil {
			out = append(out, raw)
		}
	}
	return out
}

func (s *Scheduler) persist() {
	s.mu.Lock()
	snap := s.snapshotLocked()
	s.mu.Unlock()
	config.Get().SetLinks(snap)
	s.changed()
	if s.OnLinks != nil {
		s.OnLinks(snap)
	}
}

func (s *Scheduler) changed() {
	if s.OnChanged != nil {
		s.OnChanged()
	}
}

func (s *Scheduler) setStatus(text string) {
	s.mu.Lock()
	s.status = text
	s.mu.Unlock()
	if s.OnStatus != nil {
		s.OnStatus(text)
	}
}

func (s *Scheduler) Status() string { s.mu.Lock(); defer s.mu.Unlock(); return s.status }

func (s *Scheduler) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	for i := range out {
		out[i].sync()
	}
	return out
}

func (s *Scheduler) ActiveID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.activeID }

func (s *Scheduler) Refresh() { s.refresh(false) }

func (s *Scheduler) RefreshNow() { s.refresh(true) }

func (s *Scheduler) refresh(force bool) {
	group := config.Get().Group()
	if group == "" {
		s.setStatus("Группа не указана: расписание МИРЭА не обновляется")
		return
	}
	s.mu.Lock()
	// Сменили группу — перечитываем сразу, минутная пауза тут только мешает.
	if group != s.lastGroup {
		force = true
	}
	if s.refreshing || (!force && time.Since(s.lastRefresh) < time.Minute) {
		s.mu.Unlock()
		return
	}
	s.refreshing = true
	s.lastGroup = group
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.refreshing = false
		s.lastRefresh = time.Now()
		s.mu.Unlock()
	}()
	s.setStatus("Загрузка расписания " + group + "…")

	req, _ := http.NewRequest(http.MethodGet, "https://schedule-of.mirea.ru/schedule/api/search?match="+url.QueryEscape(group), nil)
	req.Header.Set("User-Agent", "Autolectures")
	resp, err := s.http.Do(req)
	if err != nil {
		s.setStatus("Сервер расписания недоступен: " + err.Error())
		logger.Warnf(src, "%s", s.Status())
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	resp.Body.Close()

	var root struct {
		Data []struct {
			ID             int    `json:"id"`
			TargetTitle    string `json:"targetTitle"`
			FullTitle      string `json:"fullTitle"`
			ScheduleTarget int    `json:"scheduleTarget"`
			ICalLink       string `json:"iCalLink"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &root)
	// Поиск отдаёт всё, что похоже на запрос, поэтому берём точное совпадение;
	// единственный найденный вариант считаем тем самым, а из нескольких
	// неточных выбирать за пользователя нельзя.
	icalURL := ""
	var titles []string
	for _, d := range root.Data {
		title := d.TargetTitle
		if title == "" {
			title = d.FullTitle
		}
		titles = append(titles, title)
		if !strings.EqualFold(strings.TrimSpace(title), group) {
			continue
		}
		icalURL = icalLink(d.ICalLink, d.ScheduleTarget, d.ID)
		break
	}
	if icalURL == "" && len(root.Data) == 1 {
		icalURL = icalLink(root.Data[0].ICalLink, root.Data[0].ScheduleTarget, root.Data[0].ID)
	}
	if icalURL == "" {
		if len(titles) > 0 {
			s.setStatus("Уточните группу: подходит " + strings.Join(titles[:min(len(titles), 4)], ", "))
		} else {
			s.setStatus("Группа " + group + " не найдена в расписании МИРЭА")
		}
		logger.Warnf(src, "%s", s.Status())
		return
	}

	req2, _ := http.NewRequest(http.MethodGet, icalURL, nil)
	req2.Header.Set("User-Agent", "Autolectures")
	resp2, err := s.http.Do(req2)
	if err != nil {
		s.setStatus("Не удалось загрузить iCal: " + err.Error())
		return
	}
	ics, _ := io.ReadAll(io.LimitReader(resp2.Body, 8<<20))
	resp2.Body.Close()

	now := time.Now()
	lessons := ical.Parse(string(ics), now.AddDate(0, 0, -1), now.AddDate(0, 0, horizonDays))
	online := 0
	for _, l := range lessons {
		if IsOnline(l) {
			online++
		}
	}
	stale := s.merge(lessons)
	status := fmt.Sprintf("Обновлено в %s: %d дистанционных из %d занятий на %d дн.",
		time.Now().Format("15:04:05"), online, len(lessons), horizonDays)
	if stale > 0 {
		status += fmt.Sprintf(", убрано устаревших: %d", stale)
	}
	s.setStatus(status)
	logger.Infof(src, "%s", status)
}

func icalLink(link string, target, id int) string {
	if link != "" {
		return link
	}
	if target == 0 {
		target = 1
	}
	return fmt.Sprintf("https://schedule-of.mirea.ru/schedule/api/ical/%d/%d", target, id)
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

var distantRe = regexp.MustCompile(`(?i)дистанц|онлайн|online|вебинар|webinar|mts-link|мтс.?линк|zoom|teams|(^|[^а-яёa-z])сдо([^а-яёa-z]|$)`)

var weekMarkerRe = regexp.MustCompile(`(?i)^\s*\d+\s*недел`)

func isLesson(title string, start, end time.Time) bool {
	if title == "" || weekMarkerRe.MatchString(title) {
		return false
	}
	d := end.Sub(start)
	return d > 0 && d < 20*time.Hour
}

func IsOnline(l ical.Lesson) bool {
	return distantRe.MatchString(l.Location) || distantRe.MatchString(l.Description) || distantRe.MatchString(l.Title)
}

func keepLesson(l ical.Lesson) bool { return isLesson(l.Title, l.Start, l.End) && IsOnline(l) }

func keepEntry(e Entry) bool {
	if !isLesson(e.Title, e.Start, e.End) {
		return false
	}
	if e.Source != "schedule" {
		return true
	}
	return distantRe.MatchString(e.Location) || distantRe.MatchString(e.Teacher) || distantRe.MatchString(e.Title)
}

func lessonID(l ical.Lesson) string {
	return "sch:" + shortHash(l.Start.Format(time.RFC3339)+"|"+l.Title)
}

func (s *Scheduler) merge(lessons []ical.Lesson) int {
	s.mu.Lock()
	skipped := 0
	kept := s.entries[:0]
	for _, e := range s.entries {
		if keepEntry(e) {
			kept = append(kept, e)
		} else {
			skipped++
		}
	}
	s.entries = kept

	// Занятия, которых больше нет в выгрузке (сменилась группа, пару перенесли
	// или отменили), должны уходить из списка. То, где уже накопилась история,
	// оставляем — иначе поедет статистика.
	fresh := make(map[string]bool, len(lessons))
	for _, l := range lessons {
		if keepLesson(l) {
			fresh[lessonID(l)] = true
		}
	}
	now := time.Now()
	from, to := now.AddDate(0, 0, -1), now.AddDate(0, 0, horizonDays)
	stale := 0
	alive := s.entries[:0]
	for _, e := range s.entries {
		outdated := e.Source == "schedule" && !fresh[e.ID] &&
			!e.Start.Before(from) && !e.Start.After(to) &&
			e.ID != s.activeID && e.SecondsInside == 0 && e.Status != proto.LinkMarked
		if outdated {
			stale++
			continue
		}
		alive = append(alive, e)
	}
	s.entries = alive

	for _, l := range lessons {
		if !keepLesson(l) {
			skipped++
			continue
		}
		id := lessonID(l)
		found := false
		for i := range s.entries {
			if s.entries[i].ID == id {
				s.entries[i].Title, s.entries[i].End = l.Title, l.End
				s.entries[i].Location, s.entries[i].Teacher = l.Location, firstLine(l.Description)
				found = true
				break
			}
		}
		if found {
			continue
		}
		e := Entry{ID: id, Title: l.Title, Start: l.Start, End: l.End, Location: l.Location,
			Teacher: firstLine(l.Description), Source: "schedule", Status: proto.LinkPending}
		for i := range s.entries {
			o := &s.entries[i]
			if o.Source == "schedule" || o.URL == "" {
				continue
			}
			if d := o.Start.Sub(l.Start); d > -matchTolMin*time.Minute && d < matchTolMin*time.Minute {
				e.URL, e.Source, e.Status, e.SecondsInside, e.MarkedAt = o.URL, o.Source, o.Status, o.SecondsInside, o.MarkedAt
				s.entries = append(s.entries[:i], s.entries[i+1:]...)
				break
			}
		}
		s.entries = append(s.entries, e)
	}
	s.sortLocked()
	s.mu.Unlock()
	if skipped > 0 {
		logger.Infof(src, "Отброшено записей: %d (очные пары и пометки недель) — в списке только дистанционные занятия", skipped)
	}
	if stale > 0 {
		logger.Infof(src, "Убрано занятий, которых больше нет в расписании: %d", stale)
	}
	s.persist()
	return stale
}

func (s *Scheduler) findByURLLocked(u string) *Entry {
	if u == "" {
		return nil
	}
	for i := range s.entries {
		if s.entries[i].URL == u {
			return &s.entries[i]
		}
	}
	return nil
}

func (s *Scheduler) currentLessonLocked() *Entry {
	now := time.Now()
	for i := range s.entries {
		e := &s.entries[i]
		if !now.Before(e.Start.Add(-15*time.Minute)) && !now.After(e.End.Add(15*time.Minute)) {
			return e
		}
	}
	return nil
}

func (s *Scheduler) AddInvitation(inv mailmon.Invitation, subject string) {
	s.mu.Lock()
	if e := s.findByURLLocked(inv.URL); e != nil {
		if !inv.When.IsZero() && e.Source != "schedule" {
			e.Start = inv.When
		}
		s.mu.Unlock()
		s.persist()
		return
	}
	var best *Entry
	bestDiff := time.Duration(matchTolMin)*time.Minute + 1
	if !inv.When.IsZero() {
		for i := range s.entries {
			e := &s.entries[i]
			if e.Source != "schedule" && e.URL != "" {
				continue
			}
			d := e.Start.Sub(inv.When)
			if d < 0 {
				d = -d
			}
			if d < bestDiff {
				best, bestDiff = e, d
			}
		}
	}
	if best != nil {
		best.URL, best.Source = inv.URL, "email"
		logger.Infof(src, "Ссылка из письма привязана к занятию «%s»", best.Title)
		s.mu.Unlock()
		s.persist()
		return
	}
	title := inv.Title
	if title == "" {
		title = subject
	}
	start := inv.When
	if start.IsZero() {
		start = time.Now()
	}
	s.entries = append(s.entries, Entry{ID: "mail:" + shortHash(inv.URL), Title: title, Start: start,
		End: start.Add(lessonLength), URL: inv.URL, Source: "email", Status: proto.LinkPending})
	s.sortLocked()
	logger.Infof(src, "Добавлена лекция из письма: %s", title)
	s.mu.Unlock()
	s.persist()
}

func (s *Scheduler) AttachManualURL(u, title string) {
	s.mu.Lock()
	if s.findByURLLocked(u) != nil {
		s.mu.Unlock()
		return
	}
	if l := s.currentLessonLocked(); l != nil && l.URL == "" {
		l.URL, l.Source = u, "manual"
		s.mu.Unlock()
		s.persist()
		return
	}
	if title == "" {
		title = "Лекция по ссылке"
	}
	now := time.Now()
	s.entries = append(s.entries, Entry{ID: "manual:" + shortHash(u), Title: title, Start: now,
		End: now.Add(lessonLength), URL: u, Source: "manual", Status: proto.LinkPending})
	s.sortLocked()
	s.mu.Unlock()
	s.persist()
}

func (s *Scheduler) ApplyRemote(links []json.RawMessage) {
	s.mu.Lock()
	changed := false
	cutoff := time.Now().AddDate(0, 0, -30)
	for _, raw := range links {
		r, ok := fromRaw(raw)
		if !ok {
			continue
		}
		if !keepEntry(r) || (!r.End.IsZero() && r.End.Before(cutoff)) {
			continue
		}
		var local *Entry
		for i := range s.entries {
			if s.entries[i].ID == r.ID {
				local = &s.entries[i]
				break
			}
		}
		if local == nil {
			s.entries = append(s.entries, r)
			changed = true
			continue
		}
		if local.URL == "" && r.URL != "" {
			local.URL = r.URL
			changed = true
		}
		if r.Status == proto.LinkMarked && local.Status != r.Status {
			local.Status, local.MarkedAt = r.Status, r.MarkedAt
			changed = true
		}
		if r.SecondsInside > local.SecondsInside {
			local.SecondsInside = r.SecondsInside
			changed = true
		}
	}
	if changed {
		s.sortLocked()
		snap := s.snapshotLocked()
		s.mu.Unlock()
		config.Get().SetLinks(snap)
		s.changed()
		return
	}
	s.mu.Unlock()
}

// Remove убирает одно занятие из списка; занятия из официального расписания
// вернутся при следующем обновлении, добавленные вручную — нет.
func (s *Scheduler) Remove(id string) int {
	if id == "" {
		return 0
	}
	s.mu.Lock()
	kept := s.entries[:0]
	removed := 0
	for _, e := range s.entries {
		if e.ID == id && e.ID != s.activeID {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	s.mu.Unlock()
	if removed == 0 {
		return 0
	}
	s.persist()
	return removed
}

func (s *Scheduler) SetArmed(v bool) { s.mu.Lock(); s.armed = v; s.mu.Unlock() }

func (s *Scheduler) SetSessionActive(v bool) { s.mu.Lock(); s.sessionActive = v; s.mu.Unlock() }

func (s *Scheduler) OnSessionStarted(u, title string) {
	s.mu.Lock()
	s.sessionActive = true
	e := s.findByURLLocked(u)
	s.mu.Unlock()
	if e == nil {
		s.AttachManualURL(u, title)
		s.mu.Lock()
		e = s.findByURLLocked(u)
		s.mu.Unlock()
		if e == nil {
			return
		}
	}
	s.mu.Lock()
	s.activeID = e.ID
	if e.Status != proto.LinkMarked {
		e.Status = proto.LinkLive
	}
	s.mu.Unlock()
	s.persist()
}

func (s *Scheduler) OnSessionStopped() {
	s.mu.Lock()
	s.sessionActive = false
	for i := range s.entries {
		if s.entries[i].ID == s.activeID && s.entries[i].Status == proto.LinkLive {
			s.entries[i].Status = proto.LinkDone
		}
	}
	s.activeID = ""
	s.mu.Unlock()
	s.persist()
}

func (s *Scheduler) OnAttendanceMarked() {
	s.mu.Lock()
	for i := range s.entries {
		if s.entries[i].ID == s.activeID {
			s.entries[i].Status = proto.LinkMarked
			s.entries[i].MarkedAt = time.Now().Format("2006-01-02T15:04:05")
		}
	}
	s.mu.Unlock()
	s.persist()
}

func (s *Scheduler) tick() {
	now := time.Now()
	var autoStart, askLink, waitLink *Entry
	autoStop, changed, persistNeeded := false, false, false

	s.mu.Lock()
	for i := range s.entries {
		e := &s.entries[i]
		if e.ID == s.activeID && s.sessionActive {
			e.SecondsInside++
			changed = true
			if e.SecondsInside%60 == 0 {
				persistNeeded = true
			}
			if s.armed && e.Source == "schedule" && e.autoStarted && now.After(e.End.Add(3*time.Minute)) {
				logger.Infof(src, "Занятие «%s» завершено по расписанию — отключаюсь", e.Title)
				e.autoStarted = false
				autoStop = true
			}
			continue
		}
		if e.Status == proto.LinkPending && !e.End.IsZero() && now.After(e.End.Add(10*time.Minute)) {
			e.Status = proto.LinkMissed
			persistNeeded = true
		}
		if s.armed && !s.sessionActive && autoStart == nil && e.URL != "" && !e.autoStarted &&
			e.Status == proto.LinkPending && !now.Before(e.Start.Add(-time.Minute)) && now.Before(e.End) {
			e.autoStarted = true
			cp := *e
			autoStart = &cp
			continue
		}
		if s.armed && e.Source == "schedule" && e.URL == "" && e.Status == proto.LinkPending && now.Before(e.End) {
			switch {
			case !e.linkWaiting && !now.Before(e.Start):
				e.linkWaiting = true
				cp := *e
				waitLink = &cp
			case !e.LinkAsked && askLink == nil && !s.sessionActive && now.After(e.Start.Add(linkWait)):
				e.LinkAsked = true
				persistNeeded = true
				cp := *e
				askLink = &cp
			}
		}
	}
	s.mu.Unlock()

	if autoStart != nil {
		logger.Infof(src, "Автозапуск лекции «%s»", autoStart.Title)
		if s.OnAutoStart != nil {
			s.OnAutoStart(autoStart.URL, autoStart.Title)
		}
	}
	if waitLink != nil {
		logger.Infof(src, "Занятие «%s» началось, ссылки пока нет — жду письмо %.0f мин", waitLink.Title, linkWait.Minutes())
		if s.OnLinkWait != nil {
			s.OnLinkWait(waitLink.Title)
		}
	}
	if askLink != nil {
		logger.Warnf(src, "Ссылка на «%s» не пришла за %.0f мин — прошу прислать вручную", askLink.Title, linkWait.Minutes())
		if s.OnLinkMissing != nil {
			s.OnLinkMissing(askLink.Title, askLink.Start)
		}
	}
	if autoStop && s.OnAutoStop != nil {
		s.OnAutoStop()
	}
	if persistNeeded {
		s.persist()
	} else if changed {
		s.changed()
	}
}

func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st Stats
	today := time.Now()
	weekStart := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, time.Local)
	weekEnd := weekStart.AddDate(0, 0, 7)
	for _, e := range s.entries {
		if !e.Start.Before(weekStart) && e.Start.Before(weekEnd) {
			st.WeekLessons++
		}
		if e.SecondsInside > 0 {
			st.Attended++
		}
		if e.Status == proto.LinkMarked {
			st.Marked++
		}
		st.SecondsInside += e.SecondsInside
	}
	return st
}
