// Package sources — разбор страниц сайтов-источников в записи о врачах.
package sources

import (
	"encoding/json"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Doctor — одна запись о враче из списка источника.
type Doctor struct {
	FIO         string   `json:"fio"`
	Position    string   `json:"position"`    // должность: «Врач УЗИ»
	Specialties []string `json:"specialties"` // сфера работы: все специализации
	Workplaces  []string `json:"workplaces"`  // клиники с адресами
	Experience  string   `json:"experience"`  // стаж, категория
	URL         string   `json:"url"`
	CitySlug    string   `json:"-"` // slug города из ссылки на профиль (если есть)
	Actual      bool     `json:"-"`
	Reason      string   `json:"-"` // почему запись неактуальна
}

// Result — разбор одной страницы списка.
type Result struct {
	Doctors    []Doctor
	TotalPages int // 0 — неизвестно
}

// Source — сайт-источник.
type Source interface {
	Name() string  // уникальный ключ раздела: "napopravku", "napopravku-fd"
	Site() string  // сайт — колонка slug'ов в cities.txt: "napopravku"
	Title() string // для UI: "НаПоправку"
	Extra() bool   // дополнительный раздел (функц. диагностика), включается опцией
	ListURL(slug string, page int) string
	Parse(body []byte) (*Result, error)
	Concurrency() int // сколько страниц качать одновременно
}

// Selectors — CSS-селекторы источника; их можно переопределить файлом selectors.json.
type Selectors map[string]string

// All возвращает все источники с селекторами по умолчанию.
func All() []Source { return build(nil) }

func build(o map[string]Selectors) []Source {
	return []Source{
		NewNapopravku(o["napopravku"]), NewProdoctorov(o["prodoctorov"]), NewZoon(o["zoon"]),
		NewNapopravkuFD(o["napopravku"]), NewProdoctorovFD(o["prodoctorov"]), NewZoonFD(o["zoon"]),
	}
}

// LoadOverrides читает selectors.json вида {"napopravku": {"card": "..."}}.
// Если файла нет, возвращает источники по умолчанию.
func LoadOverrides(path string) ([]Source, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return All(), nil
	}
	if err != nil {
		return nil, err
	}
	var o map[string]Selectors
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return build(o), nil
}

func merge(def, over Selectors) Selectors {
	out := Selectors{}
	for k, v := range def {
		out[k] = v
	}
	for k, v := range over {
		if strings.TrimSpace(v) != "" {
			out[k] = v
		}
	}
	return out
}

// --- общие помощники ---

var spaceRe = regexp.MustCompile(`\s+`)

func clean(s string) string { return strings.TrimSpace(spaceRe.ReplaceAllString(s, " ")) }

// Признаки того, что врач сейчас не принимает.
var inactiveMarkers = []string{
	"не принимает", "не ведёт приём", "не ведет прием", "не ведет приём",
	"больше не работает", "уволен", "приём временно не ведётся", "прием временно не ведется",
	"врач не работает",
}

// statusText — текст карточки без отзывов пациентов: в отзывах фразы вроде
// «жаль, что не принимает детей» не означают, что врач не работает.
func statusText(card *goquery.Selection) string {
	c := card.Clone()
	c.Find(`[class*="review"], [itemprop="review"], [class*="comment"]`).Remove()
	return c.Text()
}

func inactiveReason(text string) string {
	low := strings.ToLower(text)
	for _, m := range inactiveMarkers {
		if strings.Contains(low, m) {
			return "отмечен как «" + m + "»"
		}
	}
	return ""
}

var patronymicRe = regexp.MustCompile(`(?i)(вич|вна|ична|инична|оглы|огли|кызы|гызы|улы|уулу)$`)

// ReorderFIO приводит «Имя Отчество Фамилия» (так пишет zoon) к «Фамилия Имя Отчество».
func ReorderFIO(s string) string {
	f := strings.Fields(s)
	if len(f) == 3 && patronymicRe.MatchString(f[1]) && !patronymicRe.MatchString(f[2]) {
		return f[2] + " " + f[0] + " " + f[1]
	}
	return s
}

var uziRe = regexp.MustCompile(`(?i)узи|узд|ультразвук|сонолог|эхограф`)

// SplitPosition выбирает из специализаций должность, связанную с УЗИ.
func SplitPosition(specs []string) (string, []string) {
	var position string
	var out []string
	seen := map[string]bool{}
	for _, s := range specs {
		s = clean(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		s = capitalize(s)
		out = append(out, s)
		if position == "" && uziRe.MatchString(s) {
			position = s
		}
	}
	if position == "" && len(out) > 0 {
		position = out[0]
	}
	return position, out
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func maxPage(html string, re *regexp.Regexp) int {
	max := 0
	for _, m := range re.FindAllStringSubmatch(html, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max && n < 10000 {
			max = n
		}
	}
	return max
}

func absURL(base, href string) string {
	href = strings.TrimSpace(href)
	switch {
	case href == "":
		return ""
	case strings.HasPrefix(href, "http"):
		return href
	case strings.HasPrefix(href, "//"):
		return "https:" + href
	case strings.HasPrefix(href, "/"):
		return base + href
	}
	return base + "/" + href
}

// IsUZI — относится ли специализация к ультразвуковой диагностике.
func IsUZI(s string) bool { return uziRe.MatchString(s) }
