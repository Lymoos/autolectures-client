package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

func (l Level) Name() string {
	switch l {
	case Debug:
		return "ОТЛАДКА"
	case Warn:
		return "ПРЕДУПР"
	case Error:
		return "ОШИБКА"
	default:
		return "ИНФО"
	}
}

type Entry struct {
	Level   Level     `json:"level"`
	Time    time.Time `json:"-"`
	Clock   string    `json:"time"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
}

const maxHistory = 2000

var (
	mu       sync.Mutex
	history  []Entry
	filePath string
	sinks    []func(Entry)
)

func Init(dataDir string) {
	dir := filepath.Join(dataDir, "logs")
	_ = os.MkdirAll(dir, 0o755)
	mu.Lock()
	filePath = filepath.Join(dir, "app.log")
	mu.Unlock()
}

func Subscribe(fn func(Entry)) {
	mu.Lock()
	sinks = append(sinks, fn)
	mu.Unlock()
}

func Log(level Level, source, message string) {
	now := time.Now()
	e := Entry{Level: level, Time: now, Clock: now.Format("15:04:05"), Source: source, Message: message}
	mu.Lock()
	history = append(history, e)
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}
	writeFile(e)
	subs := make([]func(Entry), len(sinks))
	copy(subs, sinks)
	mu.Unlock()
	for _, fn := range subs {
		fn(e)
	}
}

func writeFile(e Entry) {
	if filePath == "" {
		return
	}
	if st, err := os.Stat(filePath); err == nil && st.Size() > 5*1024*1024 {
		_ = os.Remove(filePath + ".1")
		_ = os.Rename(filePath, filePath+".1")
	}
	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s | %s | %s | %s\n", e.Time.Format(time.RFC3339), e.Level.Name(), e.Source, e.Message)
}

func Debugf(src, format string, a ...any) { Log(Debug, src, fmt.Sprintf(format, a...)) }
func Infof(src, format string, a ...any)  { Log(Info, src, fmt.Sprintf(format, a...)) }
func Warnf(src, format string, a ...any)  { Log(Warn, src, fmt.Sprintf(format, a...)) }
func Errorf(src, format string, a ...any) { Log(Error, src, fmt.Sprintf(format, a...)) }

func History() []Entry {
	mu.Lock()
	defer mu.Unlock()
	return append([]Entry(nil), history...)
}

func HistoryText() string {
	var b []byte
	for _, e := range History() {
		b = fmt.Appendf(b, "[%s] %s | %s | %s\n", e.Clock, e.Level.Name(), e.Source, e.Message)
	}
	return string(b)
}

