// Package engine — сессия парсинга: параллельная загрузка, актуальность, дедупликация, запись в БД.
package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"uziparser/internal/fetch"
	"uziparser/internal/geo"
	"uziparser/internal/sources"
	"uziparser/internal/store"
)

// Options — параметры запуска сессии.
type Options struct {
	Target      string   `json:"target"`      // city:<id> | region:<имя>
	Mode        string   `json:"mode"`        // auto (HTTP → браузер при блокировке) | browser
	ShowBrowser bool     `json:"showBrowser"` // окно Chrome видно (можно решить капчу)
	Sources     []string `json:"sources"`     // пусто — все основные
	WithFD      bool     `json:"withFD"`      // + врачи функциональной диагностики (УЗДГ, ЭхоКГ)
}

// Config — настройки движка.
type Config struct {
	OutDir      string // куда класть выгрузки
	ProfileDir  string // профиль Chrome
	BrowserTabs int
	MaxPages    int // предохранитель на город
}

// Engine управляет одной активной сессией.
type Engine struct {
	cat     *geo.Catalog
	store   *store.Store
	sources []sources.Source
	cfg     Config

	// Для тестов: подмена загрузчиков.
	NewHTTP    func() fetch.Fetcher
	NewBrowser func(fetch.BrowserOptions) (fetch.Fetcher, func(), error)

	mu     sync.Mutex
	st     Status
	cancel context.CancelFunc
	done   chan struct{}
}

// New создаёт движок.
func New(cat *geo.Catalog, st *store.Store, srcs []sources.Source, cfg Config) *Engine {
	if cfg.BrowserTabs <= 0 {
		cfg.BrowserTabs = 4
	}
	if cfg.MaxPages <= 0 {
		cfg.MaxPages = 400
	}
	e := &Engine{cat: cat, store: st, sources: srcs, cfg: cfg}
	e.NewHTTP = func() fetch.Fetcher { return fetch.NewHTTP(25 * time.Second) }
	e.NewBrowser = func(o fetch.BrowserOptions) (fetch.Fetcher, func(), error) {
		b, err := fetch.NewBrowser(o)
		if err != nil {
			return nil, nil, err
		}
		return b, b.Close, nil
	}
	e.st = Status{State: StateIdle, DBPath: st.Path, Sources: map[string]*SourceStat{}}
	return e
}

// Sources — список источников (для UI).
func (e *Engine) Sources() []sources.Source { return e.sources }

// Start запускает сессию в фоне.
func (e *Engine) Start(opts Options) error {
	cities, targetName, err := e.cat.Resolve(opts.Target)
	if err != nil {
		return err
	}
	if opts.Mode == "" {
		opts.Mode = "auto"
	}
	srcs := e.pickSources(opts.Sources, opts.WithFD)
	if len(srcs) == 0 {
		return errors.New("не выбран ни один источник")
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st.Running {
		return errors.New("сессия уже идёт")
	}
	started := time.Now()
	sid, err := e.store.NewSession(opts.Target, started)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.done = make(chan struct{})
	e.st = Status{
		Running: true, State: StateRunning, SessionID: sid, Target: opts.Target, TargetName: targetName,
		Mode: opts.Mode, StartedAt: started, Cities: len(cities), DBPath: e.store.Path,
		Sources: map[string]*SourceStat{},
	}
	for _, s := range srcs {
		e.st.Sources[s.Name()] = &SourceStat{Title: s.Title(), Mode: "http", State: "ожидание"}
		if opts.Mode == "browser" {
			e.st.Sources[s.Name()].Mode = "browser"
		}
	}
	e.logf("Старт: %s (%d гор.), источники: %s, режим: %s", targetName, len(cities), sourceTitles(srcs), opts.Mode)

	s := &session{e: e, ctx: ctx, opts: opts, cities: cities, sid: sid, srcs: srcs,
		docs: map[string]*agg{}, skipped: map[string]bool{}}
	go s.run()
	return nil
}

// Stop останавливает сессию; собранное сохраняется и выгружается.
func (e *Engine) Stop() {
	e.mu.Lock()
	cancel, running := e.cancel, e.st.Running
	if running && e.st.State != StateStopping {
		e.st.State = StateStopping
		e.st.Message = "Останавливаю, сохраняю результат…"
	}
	e.mu.Unlock()
	if running && cancel != nil {
		cancel()
	}
}

// Wait ждёт завершения текущей сессии (для тестов и graceful shutdown).
func (e *Engine) Wait() {
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Export выгружает сессию (0 — последняя) в Excel.
func (e *Engine) Export(sessionID int64) (string, int, error) {
	si, err := e.store.Session(sessionID)
	if err != nil {
		return "", 0, err
	}
	name := si.Target
	if _, n, err := e.cat.Resolve(si.Target); err == nil {
		name = n
	}
	if r := []rune(safeName(name)); len(r) > 60 { // длинные составные цели
		name = string(r[:60])
	}
	dir := filepath.Join(e.cfg.OutDir, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	path := filepath.Join(dir, fmt.Sprintf("УЗИ_%s_%s_s%d.xlsx", safeName(name), si.StartedAt.Format("2006-01-02_15-04"), si.ID))
	n, err := e.store.ExportXLSX(si.ID, path)
	return path, n, err
}

var unsafeRe = regexp.MustCompile(`[^\p{L}\p{N}_-]+`)

func safeName(s string) string { return strings.Trim(unsafeRe.ReplaceAllString(s, "_"), "_") }

func (e *Engine) pickSources(names []string, withFD bool) []sources.Source {
	var out []sources.Source
	for _, s := range e.sources {
		if s.Extra() && !withFD {
			continue
		}
		if len(names) == 0 {
			out = append(out, s)
			continue
		}
		for _, n := range names {
			if s.Name() == n || s.Site() == n {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

func sourceTitles(srcs []sources.Source) string {
	var t []string
	for _, s := range srcs {
		t = append(t, s.Title())
	}
	return strings.Join(t, ", ")
}
