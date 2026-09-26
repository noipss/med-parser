//go:build integration

package fetch

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// go test -tags integration ./internal/fetch/ — проверка реального Chrome.
func TestBrowserRealChrome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			w.Write([]byte(`<html><body><div id="captcha-container" class="smart-captcha"></div>Доступ ограничен</body></html>`))
			return
		}
		w.Write([]byte(`<html><body><div class="doctor">Иванов Иван</div><script>document.body.insertAdjacentHTML('beforeend','<p id=js>js-ok</p>')</script></body></html>`))
	}))
	defer srv.Close()

	b, err := NewBrowser(BrowserOptions{Headless: true, Tabs: 2, ProfileDir: t.TempDir(), PageTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	p, err := b.Fetch(ctx, srv.URL+"/ok")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(p.Body, []byte("Иванов Иван")) || !bytes.Contains(p.Body, []byte("js-ok")) {
		t.Fatalf("нет содержимого/JS: %s", p.Body)
	}
	if _, err := b.Fetch(ctx, srv.URL+"/blocked"); err != ErrBlocked {
		t.Fatalf("ожидался ErrBlocked, получено %v", err)
	}
	if os.Getenv("REAL_URL") != "" {
		p, err := b.Fetch(ctx, os.Getenv("REAL_URL"))
		t.Logf("real: err=%v status=%d len=%d", err, p.Status, len(p.Body))
	}
}
