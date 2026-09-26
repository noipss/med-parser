// Package fetch загружает страницы: быстрый HTTP-пул и настоящий браузер (chromedp).
package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Page — загруженная страница.
type Page struct {
	URL      string // запрошенный адрес
	FinalURL string // адрес после редиректов
	Status   int
	Body     []byte
}

// Fetcher — общий интерфейс HTTP- и браузерного загрузчика.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*Page, error)
	Name() string
}

var (
	// ErrBlocked — источник вернул антибот-страницу или капчу.
	ErrBlocked = errors.New("источник заблокировал запрос (антибот/капча)")
	// ErrNotFound — страницы нет (404), например город не поддерживается источником.
	ErrNotFound = errors.New("страница не найдена (404)")
)

// UserAgent — актуальный десктопный Chrome.
const UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"

var blockMarkers = [][]byte{
	[]byte("smart-captcha"),
	[]byte("captcha-container"),
	[]byte("smartcaptcha.cloud.yandex.ru"),
	[]byte("Доступ ограничен"),
	[]byte("подтвердите, что вы не робот"),
	[]byte("cf-challenge"),
	[]byte("challenge-platform"),
}

// IsBlocked определяет антибот-страницу по адресу, коду и содержимому.
func IsBlocked(p *Page) bool {
	if p == nil {
		return false
	}
	if strings.Contains(p.FinalURL, "/unblock") || strings.Contains(p.FinalURL, "captcha") {
		return true
	}
	if p.Status == http.StatusForbidden || p.Status == http.StatusTooManyRequests {
		return true
	}
	// Капча-страницы маленькие; на больших страницах слово «captcha» может встречаться в скриптах.
	if len(p.Body) < 60_000 {
		for _, m := range blockMarkers {
			if bytes.Contains(p.Body, m) {
				return true
			}
		}
	}
	return false
}

// HTTP — быстрый загрузчик с пулом соединений и повторами.
type HTTP struct {
	client  *http.Client
	retries int
}

// NewHTTP создаёт HTTP-загрузчик.
func NewHTTP(timeout time.Duration) *HTTP {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          128,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &HTTP{client: &http.Client{Transport: tr, Timeout: timeout}, retries: 2}
}

func (h *HTTP) Name() string { return "http" }

// Fetch загружает страницу. Повторяет при сетевых ошибках и 5xx.
func (h *HTTP) Fetch(ctx context.Context, url string) (*Page, error) {
	var lastErr error
	for attempt := 0; attempt <= h.retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 700 * time.Millisecond):
			}
		}
		p, err := h.once(ctx, url)
		if err == nil {
			if IsBlocked(p) {
				return p, ErrBlocked
			}
			if p.Status == http.StatusNotFound {
				return p, ErrNotFound
			}
			if p.Status >= 500 {
				lastErr = fmt.Errorf("HTTP %d", p.Status)
				continue
			}
			if p.Status >= 400 {
				return p, fmt.Errorf("HTTP %d", p.Status)
			}
			return p, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		lastErr = err
	}
	return nil, lastErr
}

func (h *HTTP) once(ctx context.Context, url string) (*Page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.6")
	req.Header.Set("Cache-Control", "no-cache") // только свежие данные
	req.Header.Set("Pragma", "no-cache")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	return &Page{URL: url, FinalURL: resp.Request.URL.String(), Status: resp.StatusCode, Body: body}, nil
}
