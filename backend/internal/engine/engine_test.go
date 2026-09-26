package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"uziparser/internal/fetch"
	"uziparser/internal/geo"
	"uziparser/internal/sources"
	"uziparser/internal/store"
)

// testSrc направляет источник на локальный тестовый сервер.
type testSrc struct {
	sources.Source
	base string
}

func (t testSrc) ListURL(slug string, page int) string {
	return fmt.Sprintf("%s/%s/%s/%d", t.base, t.Name(), slug, page)
}

const pages, perPage = 120, 20

// napopravkuPage — синтетическая страница в разметке napopravku; каждый 10-й врач без места приёма.
func napopravkuPage(n int) string {
	var b strings.Builder
	b.WriteString(`<html><body><a href="/kazan/doctors/vrach-uzd/page-` + strconv.Itoa(pages) + `/">last</a>`)
	for i := 0; i < perPage; i++ {
		id := (n-1)*perPage + i
		work := fmt.Sprintf(`<div class="workplace-address-card"><div class="workplace-address-card__name"><span>Клиника %d</span></div><p class="workplace-address-card__address">г Казань, ул Тестовая, %d</p></div>`, id%50, id)
		if id%10 == 9 {
			work = ""
		}
		fmt.Fprintf(&b, `<div itemscope itemtype="https://schema.org/Physician" class="doctor-card-v2 selection__section">
<meta itemprop="name" content="Тестов%d Иван Петрович"><a href="/kazan/doctor-profile/t%d/" class="object-info__title-link">Тестов%d Иван Петрович</a>
<ul><li class="items-dot-list__list-item">Стаж %d лет</li></ul>
<div itemprop="jobTitle">врач УЗИ</div><div itemprop="medicalSpecialty">гинеколог</div>%s
<button class="n-btn appointment-button">Записаться на прием</button></div>`, id, id, id, id%30+1, work)
	}
	b.WriteString(`</body></html>`)
	return b.String()
}

// fakeBrowser отдаёт реальный снимок prodoctorov (как будто прошли антибот).
type fakeBrowser struct{ body []byte }

func (f fakeBrowser) Name() string { return "browser" }
func (f fakeBrowser) Fetch(ctx context.Context, url string) (*fetch.Page, error) {
	time.Sleep(5 * time.Millisecond)
	return &fetch.Page{URL: url, FinalURL: url, Status: 200, Body: f.body}, nil
}

func TestSessionEndToEnd(t *testing.T) {
	unblock, _ := os.ReadFile("../sources/testdata/prodoctorov_unblock.html")
	pd, _ := os.ReadFile("../sources/testdata/prodoctorov_kazan.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch parts[0] {
		case "napopravku":
			n, _ := strconv.Atoi(parts[2])
			if n > pages {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, napopravkuPage(n))
		case "prodoctorov": // антибот для прямых HTTP-запросов
			http.Redirect(w, r, "/unblock?next=x", http.StatusFound)
		case "unblock":
			w.Write(unblock)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cat, _ := geo.Load()
	srcs := []sources.Source{
		testSrc{sources.NewNapopravku(nil), srv.URL},
		testSrc{sources.NewProdoctorov(nil), srv.URL},
	}
	e := New(cat, st, srcs, Config{OutDir: dir, MaxPages: 400})
	browserStarted := false
	e.NewBrowser = func(fetch.BrowserOptions) (fetch.Fetcher, func(), error) {
		browserStarted = true
		return fakeBrowser{pd}, func() {}, nil
	}

	start := time.Now()
	if err := e.Start(Options{Target: "city:kazan", Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(Options{Target: "city:kazan"}); err == nil {
		t.Error("второй запуск во время сессии должен быть запрещён")
	}
	e.Wait()
	s := e.Status()
	t.Logf("за %v: найдено %d, отсеяно %d, страниц %d/%d, ошибок %d", time.Since(start).Round(time.Millisecond), s.Found, s.Skipped, s.Pages, s.PagesTotal, s.Errors)

	wantNP := pages * perPage * 9 / 10
	if s.State != StateDone || s.Running {
		t.Fatalf("состояние %s: %s", s.State, s.Message)
	}
	if !browserStarted || s.Sources["prodoctorov"].Mode != "browser" {
		t.Error("prodoctorov должен был переключиться на браузер после антибота")
	}
	if s.Sources["napopravku"].Mode != "http" {
		t.Error("napopravku должен остаться на быстром HTTP")
	}
	if s.Found < wantNP+15 {
		t.Errorf("найдено %d, ожидалось ≥ %d", s.Found, wantNP+15)
	}
	if s.Skipped < pages*perPage/10 {
		t.Errorf("отсеяно %d, ожидалось ≥ %d", s.Skipped, pages*perPage/10)
	}
	if s.Errors != 0 {
		t.Errorf("ошибок %d: %v", s.Errors, s.Logs)
	}
	recs, _ := st.SessionDoctors(s.SessionID)
	if len(recs) != s.Found {
		t.Errorf("в БД %d строк, в статусе %d", len(recs), s.Found)
	}
	if _, err := os.Stat(s.ExportPath); err != nil {
		t.Errorf("Excel не создан: %v", err)
	}
}

func TestStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		n, _ := strconv.Atoi(strings.Split(strings.Trim(r.URL.Path, "/"), "/")[2])
		fmt.Fprint(w, napopravkuPage(n))
	}))
	defer srv.Close()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "t.db"))
	defer st.Close()
	cat, _ := geo.Load()
	e := New(cat, st, []sources.Source{testSrc{sources.NewNapopravku(nil), srv.URL}}, Config{OutDir: dir})
	if err := e.Start(Options{Target: "region:Республика Татарстан"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	e.Stop()
	e.Wait()
	s := e.Status()
	if s.State != StateStopped || s.Running {
		t.Fatalf("state=%s", s.State)
	}
	if s.Found == 0 || s.ExportPath == "" {
		t.Errorf("частичный результат должен сохраниться: found=%d export=%q", s.Found, s.ExportPath)
	}
}

func TestPresetRostovNeighbors(t *testing.T) {
	var hits sync.Map // source/slug -> true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		hits.Store(parts[0]+"/"+parts[1], true)
		page := napopravkuPage(1)
		page = strings.Replace(page, `/page-`+strconv.Itoa(pages)+`/`, `/page-1/`, 1) // одна страница на город
		fmt.Fprint(w, page)
	}))
	defer srv.Close()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "t.db"))
	defer st.Close()
	cat, _ := geo.Load()
	e := New(cat, st, []sources.Source{
		testSrc{sources.NewNapopravku(nil), srv.URL},
		testSrc{sources.NewProdoctorov(nil), srv.URL},
	}, Config{OutDir: dir})

	target := "preset:Ростов и соседние регионы;city:doneck"
	cities, _, _ := cat.Resolve(target)
	want := 0
	for _, c := range cities {
		for _, site := range []string{"napopravku", "prodoctorov"} {
			if c.Slugs[site] != "" {
				want++
			}
		}
	}
	if err := e.Start(Options{Target: target}); err != nil {
		t.Fatal(err)
	}
	e.Wait()
	s := e.Status()
	n := 0
	hits.Range(func(_, _ any) bool { n++; return true })
	t.Logf("%s: городов %d, запросов к городам %d, найдено %d, файл %s", s.TargetName, s.Cities, n, s.Found, filepath.Base(s.ExportPath))
	if s.Cities != len(cities) || n != want {
		t.Errorf("обойдено %d из %d страниц городов", n, want)
	}
	for _, slug := range []string{"napopravku/taganrog", "prodoctorov/shahty", "prodoctorov/elista", "napopravku/doneck"} {
		if _, ok := hits.Load(slug); !ok {
			t.Errorf("не запрошен %s", slug)
		}
	}
	if s.State != StateDone || s.ExportPath == "" {
		t.Errorf("state=%s export=%q", s.State, s.ExportPath)
	}
}

// Один врач на трёх сайтах в разном написании склеивается в одну строку.
func TestMergeAcrossSources(t *testing.T) {
	if normFIO("Иванова Анна Сергеевна") != normFIO("Анна Сергеевна ИВАНОВА") || normFIO("Сёмина Ольга") != normFIO("Семина Ольга") {
		t.Fatal("ключ ФИО должен не зависеть от порядка слов, регистра и ё")
	}
}

// Пустая первая страница по HTTP (JS-заглушка) → источник уходит в браузер.
func TestEmptyPageFallsBackToBrowser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><script src="/js2/nobot/decode.js"></script></html>`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "t.db"))
	defer st.Close()
	cat, _ := geo.Load()
	e := New(cat, st, []sources.Source{testSrc{sources.NewNapopravku(nil), srv.URL}}, Config{OutDir: dir})
	e.NewBrowser = func(fetch.BrowserOptions) (fetch.Fetcher, func(), error) {
		return fakeBrowser{[]byte(napopravkuPage(1))}, func() {}, nil
	}
	if err := e.Start(Options{Target: "city:kazan", Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	e.Wait()
	s := e.Status()
	if s.Sources["napopravku"].Mode != "browser" || s.Found < 18 {
		t.Fatalf("mode=%s found=%d logs=%v", s.Sources["napopravku"].Mode, s.Found, s.Logs)
	}
}

func TestFDSourcesOptIn(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "t.db"))
	defer st.Close()
	cat, _ := geo.Load()
	e := New(cat, st, sources.All(), Config{OutDir: dir})
	if n := len(e.pickSources(nil, false)); n != 3 {
		t.Errorf("без опции ожидалось 3 раздела УЗИ, получено %d", n)
	}
	if n := len(e.pickSources(nil, true)); n != 6 {
		t.Errorf("с функц. диагностикой ожидалось 6 разделов, получено %d", n)
	}
	if n := len(e.pickSources([]string{"zoon"}, true)); n != 2 {
		t.Errorf("фильтр по сайту: %d", n)
	}
}

func TestDocKeyMergesAcrossCitiesOfRegion(t *testing.T) {
	cat, _ := geo.Load()
	rostov, bataysk, krasnodar := cat.City("rostov-na-donu"), cat.City("bataysk"), cat.City("krasnodar")
	if docKey("Иванова Анна Сергеевна", rostov) != docKey("Анна Сергеевна Иванова", bataysk) {
		t.Error("один врач в Ростове и Батайске должен склеиваться")
	}
	if docKey("Иванова Анна Сергеевна", rostov) == docKey("Иванова Анна Сергеевна", krasnodar) {
		t.Error("тёзки из разных регионов не должны склеиваться")
	}
	if docKey("Иванова А. С.", rostov) == docKey("Иванова А. С.", bataysk) {
		t.Error("ФИО с инициалами склеивается только в пределах города")
	}
	a := &agg{}
	a.merge(sources.Doctor{FIO: "Иванова Анна Сергеевна", Position: "Врач УЗИ"}, "zoon", rostov, time.Now())
	a.merge(sources.Doctor{FIO: "Иванова Анна Сергеевна", Workplaces: []string{"Клиника Б"}}, "napopravku", bataysk, time.Now())
	if r := a.record(1); r.City != "Ростов-на-Дону, Батайск" || r.Sources != "zoon, napopravku" || r.Region != "Ростовская область" {
		t.Errorf("запись: %+v", r)
	}
}
