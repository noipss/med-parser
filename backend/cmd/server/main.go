// Команда server — HTTP API парсера и раздача интерфейса.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"uziparser/internal/engine"
	"uziparser/internal/geo"
	"uziparser/internal/sources"
	"uziparser/internal/store"
	"uziparser/internal/web"
)

func main() {
	home, _ := os.UserHomeDir()
	addr := flag.String("addr", envOr("UZI_ADDR", "127.0.0.1:8765"), "адрес HTTP-сервера")
	dataDir := flag.String("data", envOr("UZI_DATA", filepath.Join(home, "Documents", "UZI-Parser")), "папка для БД, выгрузок и профиля Chrome")
	watchParent := flag.Bool("watch-parent", false, "завершиться, когда закроется родительский процесс (Tauri)")
	flag.Parse()

	if err := run(*addr, *dataDir, *watchParent); err != nil {
		log.Fatal(err)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(addr, dataDir string, watchParent bool) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	cat, err := geo.Load()
	if err != nil {
		return err
	}
	srcs, err := sources.LoadOverrides(filepath.Join(dataDir, "selectors.json"))
	if err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(dataDir, "uzi_doctors.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	eng := engine.New(cat, st, srcs, engine.Config{
		OutDir:     dataDir,
		ProfileDir: filepath.Join(dataDir, "chrome-profile"),
	})
	api := &api{eng: eng, cat: cat, dataDir: dataDir}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("порт занят или недоступен (%s): %w", addr, err)
	}
	srv := &http.Server{Handler: api.routes(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if watchParent {
		go watchParentProcess(stop)
	}
	go func() {
		log.Printf("УЗИ-парсер: http://%s  (данные: %s)", ln.Addr(), dataDir)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v", err)
			stop()
		}
	}()
	<-ctx.Done()

	log.Print("Завершение: останавливаю сессию и сохраняю данные…")
	eng.Stop()
	waitDone := make(chan struct{})
	go func() { eng.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(20 * time.Second):
	}
	shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return srv.Shutdown(shCtx)
}

// watchParentProcess завершает сервер, если родитель (окно Tauri) исчез.
func watchParentProcess(stop func()) {
	ppid := os.Getppid()
	for range time.Tick(2 * time.Second) {
		if os.Getppid() != ppid {
			stop()
			return
		}
	}
}

type api struct {
	eng     *engine.Engine
	cat     *geo.Catalog
	dataDir string
}

func (a *api) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/regions", a.regions)
	mux.HandleFunc("GET /api/presets", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.cat.Presets) })
	mux.HandleFunc("GET /api/nearby", a.nearby)
	mux.HandleFunc("GET /api/neighbors", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.cat.Neighbors) })
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, a.eng.Status()) })
	mux.HandleFunc("POST /api/start", a.start)
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) { a.eng.Stop(); writeJSON(w, a.eng.Status()) })
	mux.HandleFunc("POST /api/export", a.export)
	mux.HandleFunc("GET /api/export.xlsx", a.download)
	mux.HandleFunc("POST /api/reveal", a.reveal)
	mux.Handle("/", spa(web.FS()))
	return cors(mux)
}

type regionDTO struct {
	Name   string    `json:"name"`
	Cities []cityDTO `json:"cities"`
}
type cityDTO struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Sources []string `json:"sources"`
}

func (a *api) regions(w http.ResponseWriter, _ *http.Request) {
	out := make([]regionDTO, 0, len(a.cat.Regions))
	for _, r := range a.cat.Regions {
		rd := regionDTO{Name: r.Name}
		for _, c := range r.Cities {
			cd := cityDTO{ID: c.ID, Name: c.Name}
			for _, s := range a.eng.Sources() {
				if !s.Extra() && c.Slugs[s.Site()] != "" {
					cd.Sources = append(cd.Sources, s.Title())
				}
			}
			rd.Cities = append(rd.Cities, cd)
		}
		out = append(out, rd)
	}
	writeJSON(w, out)
}

type nearbyDTO struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Region  string   `json:"region"`
	Km      int      `json:"km"`
	Sources []string `json:"sources"`
}

// nearby — города в радиусе km (по умолчанию 50) от города ?city=<id>.
func (a *api) nearby(w http.ResponseWriter, r *http.Request) {
	km, err := strconv.ParseFloat(r.URL.Query().Get("km"), 64)
	if err != nil || km <= 0 || km > 1000 {
		km = 50
	}
	out := []nearbyDTO{}
	for _, n := range a.cat.Nearby(r.URL.Query().Get("city"), km) {
		d := nearbyDTO{ID: n.City.ID, Name: n.City.Name, Region: n.City.Region, Km: int(n.Km + 0.5)}
		for _, s := range a.eng.Sources() {
			if !s.Extra() && n.City.Slugs[s.Site()] != "" {
				d.Sources = append(d.Sources, s.Title())
			}
		}
		out = append(out, d)
	}
	writeJSON(w, out)
}

func (a *api) start(w http.ResponseWriter, r *http.Request) {
	var opts engine.Options
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
		httpError(w, http.StatusBadRequest, "неверный запрос: "+err.Error())
		return
	}
	if err := a.eng.Start(opts); err != nil {
		httpError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, a.eng.Status())
}

func (a *api) export(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID int64 `json:"sessionId"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	path, n, err := a.eng.Export(req.SessionID)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"path": path, "count": n})
}

func (a *api) download(w http.ResponseWriter, r *http.Request) {
	sid, _ := strconv.ParseInt(r.URL.Query().Get("session"), 10, 64)
	path, _, err := a.eng.Export(sid)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+urlEscape(filepath.Base(path)))
	http.ServeFile(w, r, path)
}

// reveal показывает файл в Finder/Проводнике (только внутри папки данных).
func (a *api) reveal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	p := req.Path
	if p == "" {
		p = a.dataDir
	}
	abs, err := filepath.Abs(p)
	if err != nil || !strings.HasPrefix(abs, filepath.Clean(a.dataDir)) {
		httpError(w, http.StatusForbidden, "путь вне папки данных")
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", abs)
	case "windows":
		cmd = exec.Command("explorer", "/select,", abs)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(abs))
	}
	if err := cmd.Start(); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go cmd.Wait()
	writeJSON(w, map[string]string{"path": abs})
}

// spa раздаёт статику, а неизвестные пути отдаёт index.html.
func spa(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name != "" {
			if _, err := fs.Stat(files, name); err != nil {
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}

func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
