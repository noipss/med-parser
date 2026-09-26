package fetch

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// BrowserOptions — настройки браузерного загрузчика.
type BrowserOptions struct {
	Headless       bool          // false — окно Chrome видно, капчу можно решить руками
	Tabs           int           // параллельные вкладки
	ProfileDir     string        // постоянный профиль: cookies после капчи сохраняются
	PageTimeout    time.Duration // таймаут одной страницы
	CaptchaTimeout time.Duration // сколько ждать, пока человек решит капчу
	OnCaptcha      func(active bool, url string)
}

// Browser загружает страницы через системный Google Chrome (chromedp).
type Browser struct {
	opts          BrowserOptions
	allocCancel   context.CancelFunc
	browserCancel context.CancelFunc
	tabs          chan context.Context
	captchaMu     sync.Mutex
	closeOnce     sync.Once
}

// Лишнее для разбора HTML: картинки, шрифты, видео, счётчики.
var blockedPatterns = func() []*network.BlockPattern {
	var ps []*network.BlockPattern
	for _, ext := range []string{"png", "jpg", "jpeg", "gif", "webp", "avif", "svg", "ico", "woff", "woff2", "ttf", "otf", "mp4", "webm"} {
		ps = append(ps, &network.BlockPattern{URLPattern: "*://*:*/*." + ext, Block: true})
	}
	for _, host := range []string{"mc.yandex.ru", "*.google-analytics.com", "www.googletagmanager.com", "top-fwz1.mail.ru", "resizer-*.napopravku.ru", "*.doubleclick.net"} {
		ps = append(ps, &network.BlockPattern{URLPattern: "*://" + host + "/*", Block: true})
	}
	return ps
}()

func setBlocking(on bool) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		ps := blockedPatterns
		if !on {
			ps = []*network.BlockPattern{}
		}
		_ = network.SetBlockedURLs().WithURLPatterns(ps).Do(ctx) // best effort
		return nil
	})
}

func setupTab() chromedp.Tasks {
	return chromedp.Tasks{
		network.Enable(),
		setBlocking(true),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(`Object.defineProperty(navigator,'webdriver',{get:()=>undefined})`).Do(ctx)
			return err
		}),
	}
}

// NewBrowser запускает Chrome и открывает вкладки.
func NewBrowser(opts BrowserOptions) (*Browser, error) {
	if opts.Tabs <= 0 {
		opts.Tabs = 3
	}
	if opts.PageTimeout <= 0 {
		opts.PageTimeout = 45 * time.Second
	}
	if opts.CaptchaTimeout <= 0 {
		opts.CaptchaTimeout = 3 * time.Minute
	}
	allocOpts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	allocOpts = append(allocOpts,
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("lang", "ru-RU"),
		chromedp.WindowSize(1280, 900),
	)
	if opts.Headless {
		allocOpts = append(allocOpts, chromedp.Flag("headless", "new"), chromedp.UserAgent(UserAgent))
	} else {
		allocOpts = append(allocOpts, chromedp.Flag("headless", false), chromedp.Flag("hide-scrollbars", false))
	}
	if opts.ProfileDir != "" {
		allocOpts = append(allocOpts, chromedp.UserDataDir(opts.ProfileDir))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	bctx, bcancel := chromedp.NewContext(allocCtx)
	b := &Browser{opts: opts, allocCancel: allocCancel, browserCancel: bcancel, tabs: make(chan context.Context, opts.Tabs)}

	// Первый Run обязан идти на самом bctx: chromedp связывает с ним жизнь браузера.
	// Таймаут старта — через таймер, который закрывает браузер, если он завис.
	startTimer := time.AfterFunc(30*time.Second, bcancel)
	err := chromedp.Run(bctx, setupTab())
	startTimer.Stop()
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("не удалось запустить Google Chrome (установлен ли он?): %w", err)
	}
	b.tabs <- bctx
	for i := 1; i < opts.Tabs; i++ {
		tctx, _ := chromedp.NewContext(bctx) // закрываются вместе с браузером
		if err := chromedp.Run(tctx, setupTab()); err != nil {
			break
		}
		b.tabs <- tctx
	}
	return b, nil
}

func (b *Browser) Name() string { return "browser" }

// Close закрывает Chrome.
func (b *Browser) Close() {
	b.closeOnce.Do(func() {
		b.browserCancel()
		b.allocCancel()
	})
}

// Fetch открывает страницу во вкладке и возвращает итоговый HTML.
func (b *Browser) Fetch(ctx context.Context, url string) (*Page, error) {
	var tab context.Context
	select {
	case tab = <-b.tabs:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { b.tabs <- tab }()

	p, err := b.load(ctx, tab, url)
	if err != nil {
		return nil, err
	}
	if IsBlocked(p) {
		return b.solveCaptcha(ctx, tab, url)
	}
	if p.Status == http.StatusNotFound {
		return p, ErrNotFound
	}
	return p, nil
}

func (b *Browser) load(ctx, tab context.Context, url string) (*Page, error) {
	tctx, cancel := context.WithTimeout(tab, b.opts.PageTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()

	resp, err := chromedp.RunResponse(tctx, chromedp.Navigate(url))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	p, err := snapshot(tctx)
	if err != nil {
		return nil, err
	}
	p.URL = url
	if resp != nil {
		p.Status = int(resp.Status)
	}
	return p, nil
}

func snapshot(ctx context.Context) (*Page, error) {
	var loc, html string
	if err := chromedp.Run(ctx, chromedp.Location(&loc), chromedp.OuterHTML("html", &html, chromedp.ByQuery)); err != nil {
		return nil, err
	}
	return &Page{FinalURL: loc, Status: http.StatusOK, Body: []byte(html)}, nil
}

// solveCaptcha ждёт, пока оператор решит капчу в видимом окне Chrome.
// Одновременно капчу решают только в одной вкладке; остальные ждут и затем
// повторяют запрос с уже полученными cookies.
func (b *Browser) solveCaptcha(ctx, tab context.Context, url string) (*Page, error) {
	if b.opts.Headless {
		return nil, ErrBlocked
	}
	b.captchaMu.Lock()
	defer b.captchaMu.Unlock()

	_ = chromedp.Run(tab, setBlocking(false)) // виджету капчи нужны картинки
	defer func() { _ = chromedp.Run(tab, setBlocking(true)) }()

	if p, err := b.load(ctx, tab, url); err == nil && !IsBlocked(p) {
		return p, nil // капчу уже решили в другой вкладке
	}
	if b.opts.OnCaptcha != nil {
		b.opts.OnCaptcha(true, url)
		defer b.opts.OnCaptcha(false, url)
	}
	_ = chromedp.Run(tab, page.BringToFront())

	deadline := time.Now().Add(b.opts.CaptchaTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1500 * time.Millisecond):
		}
		sctx, cancel := context.WithTimeout(tab, 5*time.Second)
		cur, err := snapshot(sctx)
		cancel()
		if err != nil || IsBlocked(cur) {
			continue
		}
		p, err := b.load(ctx, tab, url)
		if err == nil && !IsBlocked(p) {
			return p, nil
		}
	}
	return nil, ErrBlocked
}
