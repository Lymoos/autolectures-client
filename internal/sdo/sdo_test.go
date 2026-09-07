package sdo

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestIsMeetingLink(t *testing.T) {
	yes := []string{
		"https://my.mts-link.ru/j/12345678/9999",
		"https://events.webinar.ru/12/34",
		"https://telemost.yandex.ru/j/123",
		"https://us02web.zoom.us/j/8888",
	}
	for _, u := range yes {
		if !IsMeetingLink(u) {
			t.Errorf("ссылка на встречу не распознана: %s", u)
		}
	}
	no := []string{
		"https://online-edu.mirea.ru/mod/resource/view.php?id=42",
		"https://online-edu.mirea.ru/course/view.php?id=7",
		"https://example.com/lecture.pdf",
		"не ссылка",
	}
	for _, u := range no {
		if IsMeetingLink(u) {
			t.Errorf("обычная ссылка принята за встречу: %s", u)
		}
	}
}

func TestNormalizeDropsNoise(t *testing.T) {
	got := Normalize("ЛК Управление информационно-технологической инфраструктурой (2026, ИВБО-21-23)")
	want := []string{"управление", "информационно", "технологической", "инфраструктурой"}
	if len(got) != len(want) {
		t.Fatalf("получено %v, ожидалось %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("получено %v, ожидалось %v", got, want)
		}
	}
}

func TestPickCourse(t *testing.T) {
	courses := []Course{
		{ID: "1", Title: "Отказоустойчивые вычислительные системы"},
		{ID: "2", Title: "Управление информационно-технологической инфраструктурой предприятия"},
		{ID: "3", Title: "Магистральные и корпоративные квантовые сети"},
	}
	cases := map[string]string{
		"ЛК Управление информационно-технологической инфраструктурой": "2",
		"ПР Отказоустойчивые вычислительные системы":                  "1",
		"ЛК Магистральные и корпоративные квантовые сети":             "3",
	}
	for lesson, want := range cases {
		got, score := PickCourse(lesson, courses)
		if got.ID != want {
			t.Errorf("«%s» → курс %q (%.2f), ожидался %s", lesson, got.ID, score, want)
		}
	}
	if c, s := PickCourse("ЛК Основы биохимии растений", courses); c.ID != "" {
		t.Errorf("посторонний предмет не должен совпасть, получено %q (%.2f)", c.Title, s)
	}
}

func TestPickLinkPrefersNearestTime(t *testing.T) {
	start := time.Date(2026, 9, 9, 18, 0, 0, 0, time.Local)
	links := []Link{
		{URL: "https://my.mts-link.ru/j/1", Title: "Запись прошлой недели", When: start.AddDate(0, 0, -7)},
		{URL: "https://my.mts-link.ru/j/2", Title: "Лекция 09.09", When: start.Add(10 * time.Minute)},
		{URL: "https://online-edu.mirea.ru/mod/page/view.php?id=9", Title: "Материалы"},
	}
	got, ok := PickLink("ЛК Управление проектами", start, links)
	if !ok || got.URL != "https://my.mts-link.ru/j/2" {
		t.Fatalf("выбрана ссылка %q (найдена: %v)", got.URL, ok)
	}
	if _, ok := PickLink("ЛК Управление проектами", start, links[2:]); ok {
		t.Fatal("ссылка на материалы курса не должна считаться встречей")
	}
}

func TestParseWhen(t *testing.T) {
	near := time.Date(2026, 9, 8, 18, 0, 0, 0, time.Local)
	cases := map[string]string{
		"Ссылка на лекцию 09.09":   "2026-09-09 00:00",
		"Неделя 3, 9 сентября":     "2026-09-09 00:00",
		"Лекция 16.09 в 18:00":     "2026-09-16 18:00",
		"Занятие 30 декабря":       "2026-12-30 00:00",
		"Материалы курса без даты": "",
		"Запись лекции 01.02.2025": "2025-02-01 00:00",
	}
	for text, want := range cases {
		got := ParseWhen(text, near)
		if want == "" {
			if !got.IsZero() {
				t.Errorf("«%s»: ожидалось «без даты», получено %s", text, got)
			}
			continue
		}
		if got.Format("2006-01-02 15:04") != want {
			t.Errorf("«%s»: получено %s, ожидалось %s", text, got.Format("2006-01-02 15:04"), want)
		}
	}
}

func TestGroupsAndPick(t *testing.T) {
	title := "УИТП Лекция - вторник, 6 пара - Даева С.Г. - ИВБО-10-23 ИВБО-11-23 ИВБО-21-23"
	got := Groups(title)
	if len(got) != 3 || got[2] != "ИВБО-21-23" {
		t.Fatalf("группы разобраны неверно: %v", got)
	}
	if !HasGroup(got, "ИВБО-21-23") || HasGroup(got, "ИКБО-01-23") {
		t.Fatal("проверка принадлежности группе не работает")
	}
	if !HasGroup(nil, "ИВБО-21-23") {
		t.Fatal("пустой список групп означает «для всех»")
	}
}

func TestIsJoinAction(t *testing.T) {
	if !IsJoinAction("Подключиться") || !IsJoinAction("JOIN") {
		t.Fatal("кнопка входа не распознана")
	}
	if IsJoinAction("Смотреть запись") || IsJoinAction("Материалы") {
		t.Fatal("кнопка записи принята за вход")
	}
}

func TestPickWebinar(t *testing.T) {
	lesson := time.Date(2026, 9, 8, 18, 0, 0, 0, time.Local)
	rows := []Webinar{
		{Title: "прошлая неделя", Start: lesson.AddDate(0, 0, -7), Groups: []string{"ИВБО-21-23"}, Action: "Подключиться"},
		{Title: "чужой поток", Start: lesson.Add(2 * time.Minute), Groups: []string{"ИКБО-10-23"}, Action: "Подключиться"},
		{Title: "запись сегодняшней", Start: lesson, Groups: []string{"ИВБО-21-23"}, Action: "Смотреть запись"},
		{Title: "наша", Start: lesson.Add(-4 * time.Minute), Groups: []string{"ИВБО-20-23", "ИВБО-21-23"}, Action: "Подключиться", URL: "https://my.mts-link.ru/j/1"},
	}
	got, ok := PickWebinar(rows, "ИВБО-21-23", lesson)
	if !ok || got.Title != "наша" {
		t.Fatalf("выбрано «%s» (найдено: %v)", got.Title, ok)
	}
	if _, ok := PickWebinar(rows[:3], "ИВБО-21-23", lesson); ok {
		t.Fatal("без кнопки «Подключиться» подключаться нельзя")
	}
}

func TestPickActivitiesFiltersForeignStreams(t *testing.T) {
	// вторник, 6 пара — 18:00
	lesson := time.Date(2026, 9, 8, 18, 0, 0, 0, time.Local)
	items := []WebinarActivity{
		{Title: "УИТП Лекция - среда, 6 пара - Исаева И.А. - ИКБО-20-23 ИКБО-21-23"},
		{Title: "УИТП Лекция - вторник, 6 пара - Жигалов К.Ю. - ИНБО-10-23 ИНБО-11-23"},
		{Title: "УИТП Лекция - вторник, 3 пара - Даева С.Г. - ИВБО-20-23 ИВБО-21-23"},
		{Title: "УИТП Лекция - вторник, 6 пара - Даева С.Г. - ИВБО-20-23 ИВБО-21-23 ИВБО-22-23"},
		{Title: "Лекции осенний семестр 26-27"},
	}
	got := PickActivities(items, "ИВБО-21-23", "ЛК Управление информационно-технологическими проектами", lesson)
	if len(got) != 2 {
		t.Fatalf("ожидались наш поток и общий элемент, получено %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Title, "Даева") || !strings.Contains(got[0].Title, "вторник, 6 пара") {
		t.Fatalf("первым должен идти наш поток, получено «%s»", got[0].Title)
	}
}

func TestPairAndWeekday(t *testing.T) {
	cases := map[string]int{"09:00": 1, "10:40": 2, "12:40": 3, "14:20": 4, "16:20": 5, "18:00": 6, "19:40": 7, "13:15": 0}
	for hm, want := range cases {
		var h, m int
		if _, err := fmt.Sscanf(hm, "%d:%d", &h, &m); err != nil {
			t.Fatal(err)
		}
		got := PairNumber(time.Date(2026, 9, 8, h, m, 0, 0, time.Local))
		if got != want {
			t.Errorf("%s → пара %d, ожидалось %d", hm, got, want)
		}
	}
	if d := WeekdayName(time.Date(2026, 9, 8, 18, 0, 0, 0, time.Local)); d != "вторник" {
		t.Errorf("день недели: %s", d)
	}
}
