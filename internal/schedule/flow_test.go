package schedule

import (
	"os"
	"testing"
	"time"

	"github.com/Lymoos/autolectures/client/internal/mailmon"
	"github.com/Lymoos/autolectures/client/internal/proto"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "autolectures-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("LOCALAPPDATA", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func newTestScheduler() *Scheduler { return &Scheduler{} }

type events struct {
	started []string
	waits   []string
	asks    []string
	stopped int
}

func (s *Scheduler) collect(ev *events) {
	s.OnAutoStart = func(url, title string) { ev.started = append(ev.started, title+" -> "+url) }
	s.OnAutoStop = func() { ev.stopped++ }
	s.OnLinkWait = func(title string) { ev.waits = append(ev.waits, title) }
	s.OnLinkMissing = func(title string, _ time.Time) { ev.asks = append(ev.asks, title) }
}

func lesson(title string, start time.Time, url string) Entry {
	return Entry{ID: "sch:" + title, Title: title, Start: start, End: start.Add(90 * time.Minute),
		Location: "Дистанционно (СДО)", Source: "schedule", Status: proto.LinkPending, URL: url}
}

func TestFlowLinkKnown(t *testing.T) {
	s, ev := newTestScheduler(), &events{}
	s.collect(ev)
	start := time.Now().Add(-30 * time.Second)
	s.entries = []Entry{lesson("ЛК Сети", start, "https://my.mts-link.ru/j/1")}
	s.armed = true

	s.tick()
	if len(ev.started) != 1 {
		t.Fatalf("ожидался автозапуск, получено %v", ev.started)
	}
	if len(ev.asks) != 0 {
		t.Errorf("лишний запрос ссылки: %v", ev.asks)
	}
}

func TestFlowLinkMissing(t *testing.T) {
	s, ev := newTestScheduler(), &events{}
	s.collect(ev)
	start := time.Now().Add(-1 * time.Minute)
	s.entries = []Entry{lesson("ЛК Базы данных", start, "")}
	s.armed = true

	s.tick()
	if len(ev.waits) != 1 {
		t.Fatalf("ожидалось ожидание ссылки, получено %v", ev.waits)
	}
	s.tick()
	if len(ev.waits) != 1 || len(ev.asks) != 0 {
		t.Fatalf("до 15 минут просить нельзя: ожидания=%v запросы=%v", ev.waits, ev.asks)
	}

	s.entries[0].Start = time.Now().Add(-linkWait - time.Minute)
	s.tick()
	if len(ev.asks) != 1 {
		t.Fatalf("ожидался запрос ссылки, получено %v", ev.asks)
	}
	s.tick()
	s.tick()
	if len(ev.asks) != 1 {
		t.Errorf("запрос должен быть один, получено %v", ev.asks)
	}
	if len(ev.started) != 0 {
		t.Errorf("подключаться не к чему: %v", ev.started)
	}
}

func TestFlowLinkArrivesByMail(t *testing.T) {
	s, ev := newTestScheduler(), &events{}
	s.collect(ev)
	start := time.Now().Add(-2 * time.Minute)
	s.entries = []Entry{lesson("ЛК Квантовые сети", start, "")}
	s.armed = true

	s.tick()
	if len(ev.waits) != 1 {
		t.Fatalf("ожидание не началось: %v", ev.waits)
	}
	s.AddInvitation(mailmon.Invitation{URL: "https://my.mts-link.ru/j/42", When: start, Title: "Лекция"}, "тема")
	s.tick()
	if len(ev.started) != 1 {
		t.Fatalf("ожидался автозапуск по ссылке из письма, получено %v", ev.started)
	}
	if len(ev.asks) != 0 {
		t.Errorf("запрос ссылки не нужен: %v", ev.asks)
	}
}

func TestFlowManualLinkAfterAsk(t *testing.T) {
	s, ev := newTestScheduler(), &events{}
	s.collect(ev)
	start := time.Now().Add(-linkWait - 2*time.Minute)
	s.entries = []Entry{lesson("ЛК Отказоустойчивость", start, "")}
	s.armed = true

	s.tick()
	s.tick()
	if len(ev.asks) != 1 {
		t.Fatalf("ожидался запрос ссылки, получено %v", ev.asks)
	}
	s.AttachManualURL("https://my.mts-link.ru/j/77", "")
	s.tick()
	if len(ev.started) != 1 {
		t.Fatalf("ожидался автозапуск по присланной ссылке, получено %v", ev.started)
	}
}

func TestFlowDisarmed(t *testing.T) {
	s, ev := newTestScheduler(), &events{}
	s.collect(ev)
	s.entries = []Entry{lesson("ЛК Сети", time.Now().Add(-linkWait-time.Minute), "")}
	s.armed = false

	s.tick()
	s.tick()
	if len(ev.waits) != 0 || len(ev.asks) != 0 || len(ev.started) != 0 {
		t.Errorf("без активной сессии событий быть не должно: %v %v %v", ev.waits, ev.asks, ev.started)
	}
}
