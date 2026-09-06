package schedule

import (
	"testing"
	"time"

	"github.com/Lymoos/autolectures/client/internal/ical"
)

func TestIsOnline(t *testing.T) {
	cases := []struct {
		loc, desc, title string
		want             bool
	}{
		{"Дистанционно (СДО)", "Преподаватель: Даева Софья Георгиевна", "ЛК Управление ИТ-проектами", true},
		{"СДО", "", "ПР Базы данных", true},
		{"А-315 (В-78)", "Преподаватель: Иванов И.И.", "ЛК Физика", false},
		{"", "", "ПР Математический анализ", false},
		{"Онлайн", "", "Семинар", true},
		{"Дистанционно", "", "", true},
		{"Актовый зал", "ссылка: https:
	}
	for _, c := range cases {
		got := IsOnline(ical.Lesson{Location: c.loc, Description: c.desc, Title: c.title})
		if got != c.want {
			t.Errorf("IsOnline(loc=%q title=%q) = %v, ожидалось %v", c.loc, c.title, got, c.want)
		}
	}
}

func TestIsLesson(t *testing.T) {
	base := time.Date(2026, 9, 8, 9, 0, 0, 0, time.Local)
	cases := []struct {
		title      string
		start, end time.Time
		want       bool
	}{
		{"ЛК Управление ИТ-проектами", base, base.Add(90 * time.Minute), true},
		{"1 неделя", base.Truncate(24 * time.Hour), base.Truncate(24 * time.Hour), false},
		{"2 неделя", base.Truncate(24 * time.Hour), base.Truncate(24 * time.Hour).Add(24 * time.Hour), false},
		{"10 неделя", base, base.Add(90 * time.Minute), false},
		{"", base, base.Add(90 * time.Minute), false},
	}
	for _, c := range cases {
		if got := isLesson(c.title, c.start, c.end); got != c.want {
			t.Errorf("isLesson(%q, %v) = %v, ожидалось %v", c.title, c.end.Sub(c.start), got, c.want)
		}
	}
}

func TestKeepEntry(t *testing.T) {
	base := time.Date(2026, 9, 8, 9, 0, 0, 0, time.Local)
	end := base.Add(90 * time.Minute)
	cases := []struct {
		name string
		e    Entry
		want bool
	}{
		{"дистанционная пара", Entry{Title: "ЛК ИТ-проекты", Start: base, End: end, Location: "Дистанционно (СДО)", Source: "schedule"}, true},
		{"военная кафедра", Entry{Title: "ДОП Военная кафедра", Start: base, End: end, Location: "ВУЦ (У-7/1)", Teacher: "ИББО-10-23", Source: "schedule"}, false},
		{"очная практика", Entry{Title: "ПР Системы передачи данных", Start: base, End: end, Location: "Г-304 (В-78)", Teacher: "Преподаватель: Деревлев Глеб Сергеевич", Source: "schedule"}, false},
		{"пометка недели", Entry{Title: "1 неделя", Start: base.Truncate(24 * time.Hour), End: base.Truncate(24 * time.Hour), Source: "schedule"}, false},
		{"пометка недели вручную", Entry{Title: "1 неделя", Start: base.Truncate(24 * time.Hour), End: base.Truncate(24 * time.Hour), Source: "manual"}, false},
		{"ссылка из почты", Entry{Title: "Лекция №1", Start: base, End: end, Source: "email"}, true},
	}
	for _, c := range cases {
		if got := keepEntry(c.e); got != c.want {
			t.Errorf("keepEntry(%s) = %v, ожидалось %v", c.name, got, c.want)
		}
	}
}

