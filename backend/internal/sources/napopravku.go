package sources

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Napopravku — napopravku.ru. Разметка SSR с microdata schema.org/Physician.
type Napopravku struct {
	sel    Selectors
	spec   string // vrach-uzd | vrach-funkcionalnoy-diagnostiki
	extra  bool
	pageRe *regexp.Regexp
}

var napopravkuDefaults = Selectors{
	"card":          `[itemtype="https://schema.org/Physician"], .doctor-card-v2`,
	"name":          `meta[itemprop="name"]`,
	"nameLink":      `.object-info__title-link`,
	"jobTitle":      `[itemprop="jobTitle"]`,
	"specialty":     `[itemprop="medicalSpecialty"]`,
	"experience":    `.items-dot-list__list-item`,
	"workplace":     `.workplace-address-card`,
	"workplaceName": `.workplace-address-card__name`,
	"workplaceAddr": `.workplace-address-card__address`,
	"appointment":   `.appointment-button, .clinic-phone__value`,
}

// NewNapopravku — врачи УЗД; over переопределяет селекторы.
func NewNapopravku(over Selectors) *Napopravku { return newNapopravku(over, "vrach-uzd", false) }

// NewNapopravkuFD — врачи функциональной диагностики (дополнительно).
func NewNapopravkuFD(over Selectors) *Napopravku {
	return newNapopravku(over, "vrach-funkcionalnoy-diagnostiki", true)
}

func newNapopravku(over Selectors, spec string, extra bool) *Napopravku {
	return &Napopravku{sel: merge(napopravkuDefaults, over), spec: spec, extra: extra,
		pageRe: regexp.MustCompile(`/doctors/` + regexp.QuoteMeta(spec) + `/page-(\d+)/`)}
}

func (n *Napopravku) Name() string {
	if n.extra {
		return "napopravku-fd"
	}
	return "napopravku"
}
func (n *Napopravku) Site() string { return "napopravku" }
func (n *Napopravku) Extra() bool  { return n.extra }
func (n *Napopravku) Title() string {
	if n.extra {
		return "НаПоправку · функц. диагностика"
	}
	return "НаПоправку"
}
func (n *Napopravku) Concurrency() int { return 6 }

func (n *Napopravku) ListURL(slug string, page int) string {
	if page <= 1 {
		return fmt.Sprintf("https://napopravku.ru/%s/doctors/%s/", slug, n.spec)
	}
	return fmt.Sprintf("https://napopravku.ru/%s/doctors/%s/page-%d/", slug, n.spec, page)
}

func (n *Napopravku) Parse(body []byte) (*Result, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	res := &Result{TotalPages: maxPage(string(body), n.pageRe)}
	seen := map[string]bool{}
	doc.Find(n.sel["card"]).Each(func(_ int, card *goquery.Selection) {
		// .doctor-card-v2 и itemtype совпадают на одном элементе; вложенные карточки пропускаем.
		if card.ParentsFiltered(n.sel["card"]).Length() > 0 {
			return
		}
		d := Doctor{}
		d.FIO = clean(card.Find(n.sel["name"]).First().AttrOr("content", ""))
		link := card.Find(n.sel["nameLink"]).First()
		if d.FIO == "" {
			d.FIO = clean(link.Text())
		}
		if d.FIO == "" {
			return
		}
		d.URL = absURL("https://napopravku.ru", link.AttrOr("href", ""))
		key := d.URL + "|" + d.FIO
		if seen[key] {
			return
		}
		seen[key] = true

		var specs []string
		card.Find(n.sel["jobTitle"]).Each(func(_ int, s *goquery.Selection) { specs = append(specs, s.Text()) })
		card.Find(n.sel["specialty"]).Each(func(_ int, s *goquery.Selection) { specs = append(specs, s.Text()) })
		d.Position, d.Specialties = SplitPosition(specs)

		card.Find(n.sel["experience"]).EachWithBreak(func(_ int, s *goquery.Selection) bool {
			if t := clean(s.Text()); strings.Contains(strings.ToLower(t), "стаж") {
				d.Experience = t
				return false
			}
			return true
		})

		card.Find(n.sel["workplace"]).Each(func(_ int, w *goquery.Selection) {
			name := clean(w.Find(n.sel["workplaceName"]).Text())
			addr := clean(w.Find(n.sel["workplaceAddr"]).Text())
			switch {
			case name != "" && addr != "":
				d.Workplaces = append(d.Workplaces, name+" ("+addr+")")
			case name != "":
				d.Workplaces = append(d.Workplaces, name)
			}
		})

		// Актуальность: есть место приёма и способ записаться, нет «не принимает».
		switch {
		case inactiveReason(statusText(card)) != "":
			d.Reason = inactiveReason(statusText(card))
		case len(d.Workplaces) == 0:
			d.Reason = "нет текущего места приёма"
		case card.Find(n.sel["appointment"]).Length() == 0 && !strings.Contains(card.Text(), "Записаться"):
			d.Reason = "нет записи на приём"
		default:
			d.Actual = true
		}
		res.Doctors = append(res.Doctors, d)
	})
	return res, nil
}
