package engine

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"uziparser/internal/fetch"
	"uziparser/internal/geo"
	"uziparser/internal/sources"
	"uziparser/internal/store"
)

// session — одна сессия парсинга.
type session struct {
	e      *Engine
	ctx    context.Context
	opts   Options
	cities []*geo.City
	sid    int64
	srcs   []sources.Source
	http   fetch.Fetcher

	bmu          sync.Mutex
	browser      fetch.Fetcher
	closeBrowser func()
	browserErr   error

	amu     sync.Mutex
	docs    map[string]*agg // ключ: ФИО|город
	skipped map[string]bool
	dirty   map[string]bool
}

func (s *session) run() {
	e := s.e
	e.mu.Lock()
	done := e.done
	e.mu.Unlock()
	defer close(done)

	s.http = e.NewHTTP()
	s.dirty = map[string]bool{}

	stopFlush := make(chan struct{})
	flushed := make(chan struct{})
	go func() { // периодическая запись в БД пачками
		defer close(flushed)
		t := time.NewTicker(700 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stopFlush:
				return
			case <-t.C:
				s.flush()
			}
		}
	}()

	var wg sync.WaitGroup
	for _, src := range s.srcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runSource(src)
		}()
	}
	wg.Wait()
	close(stopFlush)
	<-flushed
	s.flush()

	s.bmu.Lock()
	if s.closeBrowser != nil {
		s.closeBrowser()
	}
	s.bmu.Unlock()

	s.amu.Lock()
	found, skipped := len(s.docs), len(s.skipped)
	s.amu.Unlock()

	state := StateDone
	if s.ctx.Err() != nil {
		state = StateStopped
	}
	_ = e.store.FinishSession(s.sid, found, skipped, state)

	var exportPath, msg string
	if found > 0 {
		path, n, err := e.Export(s.sid)
		if err != nil {
			msg = "Ошибка выгрузки в Excel: " + err.Error()
		} else {
			exportPath = path
			msg = "Готово: " + strconv.Itoa(n) + " актуальных врачей записано в БД и Excel"
		}
	} else {
		msg = "Актуальные врачи не найдены. Проверьте доступ к сайтам (VPN выключен?) и журнал."
	}

	e.update(func(st *Status) {
		st.Running = false
		st.State = state
		st.FinishedAt = time.Now()
		st.Found, st.Skipped = found, skipped
		st.ExportPath = exportPath
		st.Message = msg
		if state == StateStopped {
			st.Message = "Остановлено. " + msg
		}
		e.logf("%s (найдено %d, отсеяно %d, за %s)", st.Message, found, skipped, time.Since(st.StartedAt).Round(time.Second))
	})
	e.mu.Lock()
	e.cancel = nil
	e.mu.Unlock()
}

// getBrowser лениво запускает общий для всех источников Chrome.
func (s *session) getBrowser() (fetch.Fetcher, error) {
	s.bmu.Lock()
	defer s.bmu.Unlock()
	if s.browser != nil || s.browserErr != nil {
		return s.browser, s.browserErr
	}
	if s.ctx.Err() != nil {
		return nil, s.ctx.Err()
	}
	e := s.e
	e.log("Запускаю Google Chrome (%s)…", map[bool]string{true: "окно видно", false: "фоновый режим"}[s.opts.ShowBrowser])
	b, closeFn, err := e.NewBrowser(fetch.BrowserOptions{
		Headless:   !s.opts.ShowBrowser,
		Tabs:       e.cfg.BrowserTabs,
		ProfileDir: e.cfg.ProfileDir,
		OnCaptcha: func(active bool, url string) {
			e.update(func(st *Status) {
				if !st.Running || st.State == StateStopping {
					return
				}
				if active {
					st.State = StateCaptcha
					st.Message = "Сайт просит капчу: решите её в окне Chrome, парсинг продолжится сам"
					e.logf("Капча на %s — жду решения", url)
				} else {
					st.State = StateRunning
					st.Message = ""
				}
			})
		},
	})
	if err != nil {
		s.browserErr = err
		e.log("Браузер не запущен: %v", err)
		return nil, err
	}
	s.browser, s.closeBrowser = b, closeFn
	return b, nil
}

// flush пишет изменённые записи в SQLite.
func (s *session) flush() {
	s.amu.Lock()
	if len(s.dirty) == 0 {
		s.amu.Unlock()
		return
	}
	recs := make([]store.Record, 0, len(s.dirty))
	for k := range s.dirty {
		recs = append(recs, s.docs[k].record(s.sid))
	}
	s.dirty = map[string]bool{}
	s.amu.Unlock()
	if err := s.e.store.Upsert(recs); err != nil {
		s.e.log("Ошибка записи в БД: %v", err)
	}
}

// add учитывает врачей со страницы: фильтр актуальности, дедупликация, слияние источников.
func (s *session) add(src sources.Source, city *geo.City, docs []sources.Doctor) {
	now := time.Now()
	var fresh []*agg
	actual := 0
	s.amu.Lock()
	for _, d := range docs {
		c := city
		if d.CitySlug != "" {
			if cc := s.e.cat.CityBySlug(src.Site(), d.CitySlug); cc != nil {
				c = cc
			}
		}
		key := normFIO(d.FIO) + "|" + c.ID
		if !d.Actual {
			if s.docs[key] == nil {
				s.skipped[key] = true
			}
			continue
		}
		actual++
		delete(s.skipped, key)
		a := s.docs[key]
		if a == nil {
			a = &agg{city: c}
			s.docs[key] = a
			fresh = append(fresh, a)
		}
		a.merge(d, src.Name(), now)
		s.dirty[key] = true
	}
	views := make([]DoctorView, 0, len(fresh))
	for i := len(fresh) - 1; i >= 0; i-- { // новые — сверху
		views = append(views, fresh[i].view())
	}
	found, skipped := len(s.docs), len(s.skipped)
	s.amu.Unlock()

	s.e.update(func(st *Status) {
		st.Found, st.Skipped = found, skipped
		st.Pages++
		if ss := st.Sources[src.Name()]; ss != nil {
			ss.Found += actual
			ss.Pages++
		}
		st.Recent = append(views, st.Recent...)
		if len(st.Recent) > maxRecent {
			st.Recent = st.Recent[:maxRecent]
		}
	})
}

// normFIO — ключ врача без учёта регистра, «ё» и порядка слов:
// «Иванов Иван Иванович» и «Иван Иванович Иванов» — один человек.
func normFIO(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "ё", "е"), "Ё", "е"))
	f := strings.Fields(strings.ReplaceAll(s, ".", " "))
	sort.Strings(f)
	return strings.Join(f, " ")
}

// agg — врач, собранный из одного или нескольких источников.
type agg struct {
	city                    *geo.City
	fio, position, exp      string
	specs, works, srcs, urs []string
	checked                 time.Time
}

func (a *agg) merge(d sources.Doctor, src string, now time.Time) {
	if len([]rune(d.FIO)) > len([]rune(a.fio)) { // полное ФИО лучше инициалов
		a.fio = d.FIO
	}
	if a.position == "" || (!sources.IsUZI(a.position) && sources.IsUZI(d.Position)) {
		a.position = d.Position
	}
	if len(d.Experience) > len(a.exp) {
		a.exp = d.Experience
	}
	a.specs = union(a.specs, d.Specialties...)
	a.works = union(a.works, d.Workplaces...)
	a.srcs = union(a.srcs, src)
	a.urs = union(a.urs, d.URL)
	a.checked = now
}

func (a *agg) record(sid int64) store.Record {
	return store.Record{
		FIO: a.fio, FIOKey: normFIO(a.fio), Position: a.position,
		Specialties: strings.Join(a.specs, "; "), Workplaces: strings.Join(a.works, "; "),
		Experience: a.exp, City: a.city.Name, Region: a.city.Region,
		Sources: strings.Join(a.srcs, ", "), URLs: strings.Join(a.urs, " "),
		CheckedAt: a.checked, SessionID: sid,
	}
}

func (a *agg) view() DoctorView {
	var other []string
	for _, s := range a.specs {
		if !strings.EqualFold(s, a.position) {
			other = append(other, s)
		}
	}
	w := ""
	if len(a.works) > 0 {
		w = a.works[0]
	}
	return DoctorView{FIO: a.fio, Position: a.position, Sphere: strings.Join(other, ", "), Workplace: w,
		City: a.city.Name, Source: strings.Join(a.srcs, ", ")}
}

func union(list []string, add ...string) []string {
	for _, v := range add {
		if v == "" {
			continue
		}
		dup := false
		for _, x := range list {
			if strings.EqualFold(x, v) {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, v)
		}
	}
	return list
}
