package engine

import (
	"context"
	"errors"
	"sync"

	"uziparser/internal/fetch"
	"uziparser/internal/geo"
	"uziparser/internal/sources"
)

// runner обходит один источник по всем городам цели.
type runner struct {
	s      *session
	src    sources.Source
	ctx    context.Context
	cancel context.CancelFunc
	sem    chan struct{} // ограничение параллельных запросов к источнику

	mu         sync.Mutex
	useBrowser bool
	skipReason string
	okPages    int
}

func (s *session) runSource(src sources.Source) {
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	r := &runner{s: s, src: src, ctx: ctx, cancel: cancel,
		sem: make(chan struct{}, src.Concurrency()), useBrowser: s.opts.Mode == "browser"}
	r.setState("работает")

	var wg sync.WaitGroup
	cities := 0
	for _, city := range s.cities {
		slug := city.Slugs[src.Site()]
		if slug == "" {
			continue
		}
		cities++
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.crawlCity(city, slug)
		}()
	}
	wg.Wait()

	r.mu.Lock()
	reason, okPages := r.skipReason, r.okPages
	r.mu.Unlock()
	switch {
	case cities == 0:
		r.setState("нет городов цели на этом сайте")
	case reason != "":
		r.setState("пропущен: " + reason)
	case s.ctx.Err() != nil:
		r.setState("остановлен")
	case okPages == 0:
		r.setState("ошибка: сайт недоступен (см. журнал)")
	default:
		r.setState("готово")
	}
}

func (r *runner) crawlCity(city *geo.City, slug string) {
	r.s.e.update(func(st *Status) { st.PagesTotal++ })
	first, ok := r.page(city, slug, 1)
	if !ok {
		return
	}
	maxPages := r.s.e.cfg.MaxPages
	total := min(first.TotalPages, maxPages)
	if total > 1 {
		r.s.e.update(func(st *Status) { st.PagesTotal += total - 1 })
		var wg sync.WaitGroup
		for p := 2; p <= total && r.ctx.Err() == nil; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r.page(city, slug, p)
			}()
		}
		wg.Wait()
		return
	}
	// Число страниц неизвестно, а первая полная — идём последовательно до пустой.
	if total == 0 && len(first.Doctors) >= 10 {
		for p := 2; p <= maxPages && r.ctx.Err() == nil; p++ {
			r.s.e.update(func(st *Status) { st.PagesTotal++ })
			res, ok := r.page(city, slug, p)
			if !ok || len(res.Doctors) == 0 {
				return
			}
		}
	}
}

// page загружает и разбирает одну страницу списка.
func (r *runner) page(city *geo.City, slug string, n int) (*sources.Result, bool) {
	select {
	case r.sem <- struct{}{}:
	case <-r.ctx.Done():
		return nil, false
	}
	defer func() { <-r.sem }()

	e := r.s.e
	url := r.src.ListURL(slug, n)
	p, err := r.fetch(url)
	if err != nil {
		switch {
		case r.ctx.Err() != nil:
		case errors.Is(err, fetch.ErrNotFound):
			if n == 1 {
				e.log("%s: для города %s нет страницы УЗИ (404)", r.src.Title(), city.Name)
			}
		case errors.Is(err, fetch.ErrBlocked):
			r.skip("сайт требует капчу — включите «Показывать браузер» и решите её в окне Chrome")
		default:
			r.fail("%s %s стр.%d: %v", r.src.Title(), city.Name, n, err)
		}
		return nil, false
	}
	res, err := r.src.Parse(p.Body)
	// Пустая первая страница по HTTP чаще всего означает JS-заглушку антибота: пробуем браузер.
	if err == nil && n == 1 && len(res.Doctors) == 0 && r.canSwitch() {
		r.switchToBrowser()
		if p, err = r.fetch(url); err == nil {
			res, err = r.src.Parse(p.Body)
		} else if !errors.Is(err, fetch.ErrNotFound) && r.ctx.Err() == nil {
			r.fail("%s %s стр.%d: %v", r.src.Title(), city.Name, n, err)
			return nil, false
		}
	}
	if err != nil {
		r.fail("%s %s стр.%d: разбор: %v", r.src.Title(), city.Name, n, err)
		return nil, false
	}
	if n == 1 {
		if len(res.Doctors) == 0 {
			e.log("%s %s: на странице 0 карточек — возможно, изменилась разметка (см. selectors.json)", r.src.Title(), city.Name)
		} else {
			e.log("%s %s: страниц %d, на первой %d врачей", r.src.Title(), city.Name, max(res.TotalPages, 1), len(res.Doctors))
		}
	}
	r.mu.Lock()
	r.okPages++
	r.mu.Unlock()
	r.s.add(r.src, city, res.Doctors)
	return res, true
}

// fetch выбирает загрузчик; в режиме auto при блокировке переключает источник на браузер.
func (r *runner) fetch(url string) (*fetch.Page, error) {
	for {
		f, err := r.fetcher()
		if err != nil {
			return nil, err
		}
		p, err := f.Fetch(r.ctx, url)
		if errors.Is(err, fetch.ErrBlocked) && f == r.s.http && r.s.opts.Mode == "auto" {
			r.switchToBrowser()
			continue
		}
		return p, err
	}
}

// canSwitch — источник ещё на HTTP и режим позволяет перейти на браузер.
func (r *runner) canSwitch() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.useBrowser && r.s.opts.Mode == "auto"
}

func (r *runner) fetcher() (fetch.Fetcher, error) {
	r.mu.Lock()
	b := r.useBrowser
	r.mu.Unlock()
	if !b {
		return r.s.http, nil
	}
	f, err := r.s.getBrowser()
	if err != nil {
		r.skip("браузер недоступен: " + err.Error())
	}
	return f, err
}

func (r *runner) switchToBrowser() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.useBrowser {
		return
	}
	r.useBrowser = true
	r.s.e.update(func(st *Status) {
		if ss := st.Sources[r.src.Name()]; ss != nil {
			ss.Mode = "browser"
		}
		st.Mode = "auto (+браузер)"
	})
	r.s.e.log("%s прямые запросы заблокированы или пусты — переключаюсь на браузер", r.src.Title())
}

func (r *runner) skip(reason string) {
	r.mu.Lock()
	first := r.skipReason == ""
	if first {
		r.skipReason = reason
	}
	r.mu.Unlock()
	if first {
		r.s.e.log("%s пропущен: %s", r.src.Title(), reason)
		r.cancel()
	}
}

func (r *runner) fail(format string, a ...any) {
	r.s.e.update(func(st *Status) {
		st.Errors++
		if ss := st.Sources[r.src.Name()]; ss != nil {
			ss.Errors++
		}
		st.Pages++
		e := r.s.e
		e.logf(format, a...)
	})
}

func (r *runner) setState(s string) {
	r.s.e.update(func(st *Status) {
		if ss := st.Sources[r.src.Name()]; ss != nil {
			ss.State = s
		}
	})
}
