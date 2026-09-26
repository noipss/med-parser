package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Prodoctorov — prodoctorov.ru. Под антиботом, поэтому обычно нужен режим браузера.
type Prodoctorov struct {
	sel    Selectors
	spec   string // ultrazvukovoy-diagnost | funkcionalnyy-diagnost
	extra  bool
	pageRe *regexp.Regexp
}

var prodoctorovDefaults = Selectors{
	"card":        `div.b-doctor-card[data-doctor-id]`,
	"name":        `.b-doctor-card__name-surname`,
	"nameLink":    `.b-doctor-card__name-link`,
	"spec":        `.b-doctor-card__spec`,
	"experience":  `.b-doctor-card__experience-years`,
	"category":    `.b-doctor-card__category`,
	"lpuOption":   `select.b-doctor-card__lpu-select option`,
	"lpuName":     `.b-doctor-card__lpu-name`,
	"appointment": `.b-doctor-card__appointment-btn, .appointment_request, .b-doctor-card__lpu-phone-num`,
}

// NewProdoctorov — врачи УЗИ; over переопределяет селекторы.
func NewProdoctorov(over Selectors) *Prodoctorov {
	return newProdoctorov(over, "ultrazvukovoy-diagnost", false)
}

// NewProdoctorovFD — врачи функциональной диагностики (дополнительно).
func NewProdoctorovFD(over Selectors) *Prodoctorov {
	return newProdoctorov(over, "funkcionalnyy-diagnost", true)
}

func newProdoctorov(over Selectors, spec string, extra bool) *Prodoctorov {
	return &Prodoctorov{sel: merge(prodoctorovDefaults, over), spec: spec, extra: extra,
		pageRe: regexp.MustCompile(regexp.QuoteMeta(spec) + `/\?(?:[^"'\s]*&(?:amp;)?)?page=(\d+)`)}
}

func (p *Prodoctorov) Name() string {
	if p.extra {
		return "prodoctorov-fd"
	}
	return "prodoctorov"
}
func (p *Prodoctorov) Site() string { return "prodoctorov" }
func (p *Prodoctorov) Extra() bool  { return p.extra }
func (p *Prodoctorov) Title() string {
	if p.extra {
		return "ПроДокторов · функц. диагностика"
	}
	return "ПроДокторов"
}
func (p *Prodoctorov) Concurrency() int { return 3 } // бережно: антибот

func (p *Prodoctorov) ListURL(slug string, page int) string {
	if page <= 1 {
		return fmt.Sprintf("https://prodoctorov.ru/%s/%s/", slug, p.spec)
	}
	return fmt.Sprintf("https://prodoctorov.ru/%s/%s/?page=%d", slug, p.spec, page)
}

var (
	prodoctorovPagesLenRe = regexp.MustCompile(`pages-length="(\d+)"`)
	profileCityRe         = regexp.MustCompile(`^/([a-z0-9-]+)/vrach/`)
)

type timetableItem struct {
	DoctorFio string `json:"doctorFio"`
	LpuIds    []int  `json:"lpuIds"`
}

func (p *Prodoctorov) Parse(body []byte) (*Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res := &Result{TotalPages: max(maxPage(string(body), p.pageRe), maxPage(string(body), prodoctorovPagesLenRe))}
	seen := map[string]bool{}
	doc.Find(p.sel["card"]).Each(func(_ int, card *goquery.Selection) {
		id := card.AttrOr("data-doctor-id", "")
		if id == "" || seen[id] { // рекламные блоки дублируют карточки
			return
		}
		var tt timetableItem
		hasTT := json.Unmarshal([]byte(card.AttrOr("data-timetable-item", "")), &tt) == nil

		d := Doctor{FIO: clean(tt.DoctorFio)}
		if d.FIO == "" {
			d.FIO = clean(card.Find(p.sel["name"]).First().Text())
		}
		if d.FIO == "" {
			return
		}
		seen[id] = true
		href := card.Find(p.sel["nameLink"]).First().AttrOr("href", "")
		d.URL = absURL("https://prodoctorov.ru", href)
		if m := profileCityRe.FindStringSubmatch(strings.TrimPrefix(href, "https://prodoctorov.ru")); m != nil {
			d.CitySlug = m[1]
		}

		d.Position, d.Specialties = SplitPosition(strings.Split(card.Find(p.sel["spec"]).First().Text(), ","))

		exp := clean(card.Find(p.sel["experience"]).First().Text())
		if cat := clean(card.Find(p.sel["category"]).First().Text()); cat != "" {
			exp = strings.TrimPrefix(exp+"; "+cat, "; ")
		}
		d.Experience = exp

		seenLpu := map[string]bool{}
		addLpu := func(name, addr string) {
			name = strings.Trim(clean(name), "«»\" ")
			if name == "" || seenLpu[name] {
				return
			}
			seenLpu[name] = true
			if addr = clean(addr); addr != "" {
				name += " (" + addr + ")"
			}
			d.Workplaces = append(d.Workplaces, name)
		}
		card.Find(p.sel["lpuOption"]).Each(func(_ int, o *goquery.Selection) {
			addLpu(o.Text(), o.AttrOr("data-adit-text", ""))
		})
		if len(d.Workplaces) == 0 {
			card.Find(p.sel["lpuName"]).Each(func(_ int, o *goquery.Selection) { addLpu(o.Text(), "") })
		}

		// Актуальность: у врача есть текущие клиники (lpuIds) и запись/телефон.
		works := (hasTT && len(tt.LpuIds) > 0) || len(d.Workplaces) > 0
		switch {
		case inactiveReason(statusText(card)) != "":
			d.Reason = inactiveReason(statusText(card))
		case !works:
			d.Reason = "нет текущего места работы"
		case card.Find(p.sel["appointment"]).Length() == 0 && !strings.Contains(card.Text(), "Запись на приём"):
			d.Reason = "нет записи на приём"
		default:
			d.Actual = true
		}
		res.Doctors = append(res.Doctors, d)
	})
	return res, nil
}
