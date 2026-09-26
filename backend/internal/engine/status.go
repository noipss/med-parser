package engine

import (
	"fmt"
	"time"
)

// Состояния сессии.
const (
	StateIdle     = "idle"
	StateRunning  = "running"
	StateCaptcha  = "captcha"
	StateStopping = "stopping"
	StateDone     = "done"
	StateStopped  = "stopped"
	StateError    = "error"
)

// SourceStat — прогресс по источнику.
type SourceStat struct {
	Title  string `json:"title"`
	Mode   string `json:"mode"`  // http | browser
	State  string `json:"state"` // ожидание | работает | готово | пропущен: …
	Found  int    `json:"found"`
	Pages  int    `json:"pages"`
	Errors int    `json:"errors"`
}

// DoctorView — строка для ленты «последние найденные».
type DoctorView struct {
	FIO       string `json:"fio"`
	Position  string `json:"position"`
	Sphere    string `json:"sphere"`
	Workplace string `json:"workplace"`
	City      string `json:"city"`
	Source    string `json:"source"`
}

// LogLine — строка журнала.
type LogLine struct {
	Time string `json:"time"`
	Text string `json:"text"`
}

// Status — снимок состояния для UI.
type Status struct {
	Running    bool                   `json:"running"`
	State      string                 `json:"state"`
	Message    string                 `json:"message"`
	SessionID  int64                  `json:"sessionId"`
	Target     string                 `json:"target"`
	TargetName string                 `json:"targetName"`
	Mode       string                 `json:"mode"`
	StartedAt  time.Time              `json:"startedAt"`
	FinishedAt time.Time              `json:"finishedAt"`
	ElapsedSec float64                `json:"elapsedSec"`
	Cities     int                    `json:"cities"`
	Found      int                    `json:"found"`
	Skipped    int                    `json:"skipped"`
	Pages      int                    `json:"pages"`
	PagesTotal int                    `json:"pagesTotal"`
	Errors     int                    `json:"errors"`
	PerMinute  float64                `json:"perMinute"`
	Sources    map[string]*SourceStat `json:"sources"`
	Recent     []DoctorView           `json:"recent"`
	Logs       []LogLine              `json:"logs"`
	ExportPath string                 `json:"exportPath"`
	DBPath     string                 `json:"dbPath"`
}

const (
	maxRecent = 40
	maxLogs   = 200
)

// Status возвращает копию текущего состояния.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.st
	end := time.Now()
	if !s.Running && !s.FinishedAt.IsZero() {
		end = s.FinishedAt
	}
	if !s.StartedAt.IsZero() {
		s.ElapsedSec = end.Sub(s.StartedAt).Seconds()
		if s.ElapsedSec > 1 {
			s.PerMinute = float64(s.Found) / s.ElapsedSec * 60
		}
	}
	s.Sources = make(map[string]*SourceStat, len(e.st.Sources))
	for k, v := range e.st.Sources {
		c := *v
		s.Sources[k] = &c
	}
	s.Recent = append([]DoctorView(nil), e.st.Recent...)
	s.Logs = append([]LogLine(nil), e.st.Logs...)
	return s
}

// logf пишет в журнал. Вызывать под e.mu.
func (e *Engine) logf(format string, a ...any) {
	e.st.Logs = append(e.st.Logs, LogLine{Time: time.Now().Format("15:04:05"), Text: fmt.Sprintf(format, a...)})
	if n := len(e.st.Logs); n > maxLogs {
		e.st.Logs = e.st.Logs[n-maxLogs:]
	}
}

// update выполняет f под блокировкой.
func (e *Engine) update(f func(st *Status)) {
	e.mu.Lock()
	f(&e.st)
	e.mu.Unlock()
}

// log — потокобезопасная запись в журнал.
func (e *Engine) log(format string, a ...any) {
	e.mu.Lock()
	e.logf(format, a...)
	e.mu.Unlock()
}
