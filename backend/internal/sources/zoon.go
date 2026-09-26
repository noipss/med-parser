package sources

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Zoon — zoon.ru. Самый крупный по объёму справочник врачей; 50 карточек на страницу.
type Zoon struct {
	sel     Selectors
	spec    string // сегмент специальности: vrach_uzi
	title   string
	extra   bool
	pageRe  *regexp.Regexp
	perPage int
}

var zoonDefaults = Selectors{
	"card":          `[data-uitest="prof-item-block"]`,
	"name":          `.prof-name`,
	"spec":          `.prof-spec-list`,
	"experience":    `.specialist-experience`,
	"workplace":     `.specialist-place, .js-prof-schedule-org, .js-prof-org`,
	"workplaceName": `.specialist-place-name`,
	"workplaceAddr": `.specialist-address-text`,
	"appointment":   `.js-phone[data-number], [data-slot], .specialist-phone`,
	"orgOption":     `select[name="organization_id"] option`,
	"total":         `.page-title-block`,
}

// NewZoon — врачи УЗИ.
func NewZoon(over Selectors) *Zoon { return newZoon(over, "vrach_uzi", "Zoon", false) }

// NewZoonFD — врачи функциональной диагностики (дополнительно).
func NewZoonFD(over Selectors) *Zoon {
	return newZoon(over, "vrach_funktsionalnoj_diagnostiki", "Zoon · функц. диагностика", true)
}

func newZoon(over Selectors, spec, title string, extra bool) *Zoon {
	return &Zoon{sel: merge(zoonDefaults, over), spec: spec, title: title, extra: extra, perPage: 50,
		pageRe: regexp.MustCompile(`/p-doctor-` + regexp.QuoteMeta(spec) + `/page-(\d+)/`)}
}

func (z *Zoon) Name() string {
	if z.extra {
		return "zoon-fd"
	}
	return "zoon"
}
func (z *Zoon) Site() string     { return "zoon" }
func (z *Zoon) Title() string    { return z.title }
func (z *Zoon) Extra() bool      { return z.extra }
func (z *Zoon) Concurrency() int { return 4 }

func (z *Zoon) ListURL(slug string, page int) string {
	if page <= 1 {
		return fmt.Sprintf("https://zoon.ru/%s/p-doctor-%s/", slug, z.spec)
	}
	return fmt.Sprintf("https://zoon.ru/%s/p-doctor-%s/page-%d/", slug, z.spec, page)
}

var zoonTotalRe = regexp.MustCompile(`(\d[\d\s\x{00a0}]*)\s*(?:специалист|врач)`)

func (z *Zoon) Parse(body []byte) (*Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res := &Result{TotalPages: maxPage(string(body), z.pageRe)}
	// Ссылки ведут только на ближайшие страницы, поэтому число страниц считаем по общему количеству.
	if m := zoonTotalRe.FindStringSubmatch(doc.Find(z.sel["total"]).First().Text()); m != nil {
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, m[1])
		if n, err := strconv.Atoi(digits); err == nil && n > 0 {
			res.TotalPages = max(res.TotalPages, (n+z.perPage-1)/z.perPage)
		}
	}

	seen := map[string]bool{}
	doc.Find(z.sel["card"]).Each(func(_ int, card *goquery.Selection) {
		link := card.Find(z.sel["name"]).First()
		d := Doctor{FIO: ReorderFIO(clean(link.Text()))}
		d.URL = absURL("https://zoon.ru", link.AttrOr("href", ""))
		if d.FIO == "" || seen[d.URL+d.FIO] {
			return
		}
		seen[d.URL+d.FIO] = true

		d.Position, d.Specialties = SplitPosition(strings.Split(card.Find(z.sel["spec"]).First().Text(), ","))
		d.Experience = clean(card.Find(z.sel["experience"]).First().Text())
		// Врач в нескольких клиниках: названия в <select>, адреса — в блоках с тем же data-id.
		orgNames := map[string]string{}
		card.Find(z.sel["orgOption"]).Each(func(_ int, o *goquery.Selection) {
			orgNames[o.AttrOr("value", "")] = clean(o.Text())
		})
		card.Find(z.sel["workplace"]).Each(func(_ int, w *goquery.Selection) {
			name := clean(w.Find(z.sel["workplaceName"]).First().Text())
			if name == "" {
				name = orgNames[w.AttrOr("data-id", "")]
			}
			addr := clean(w.Find(z.sel["workplaceAddr"]).First().Text())
			switch {
			case name != "" && addr != "":
				d.Workplaces = append(d.Workplaces, name+" ("+addr+")")
			case name != "":
				d.Workplaces = append(d.Workplaces, name)
			}
		})

		if len(d.Workplaces) == 0 { // адресов нет, но клиники перечислены в списке выбора
			seenOrg := map[string]bool{}
			card.Find(z.sel["orgOption"]).Each(func(_ int, o *goquery.Selection) {
				if n := clean(o.Text()); n != "" && !seenOrg[n] {
					seenOrg[n] = true
					d.Workplaces = append(d.Workplaces, n)
				}
			})
		}

		switch {
		case inactiveReason(statusText(card)) != "":
			d.Reason = inactiveReason(statusText(card))
		case len(d.Workplaces) == 0:
			d.Reason = "нет текущего места работы"
		case card.Find(z.sel["appointment"]).Length() == 0:
			d.Reason = "нет телефона/записи"
		default:
			d.Actual = true
		}
		res.Doctors = append(res.Doctors, d)
	})
	return res, nil
}
