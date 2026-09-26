// Package geo — справочник регионов и городов со slug'ами источников.
package geo

import (
	_ "embed"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

//go:embed cities.txt
var citiesTxt string

//go:embed presets.txt
var presetsTxt string

//go:embed neighbors.txt
var neighborsTxt string

//go:embed coords.txt
var coordsTxt string

// Preset — готовый набор целей.
type Preset struct {
	Name    string `json:"name"`
	Target  string `json:"target"`  // "region:…;city:…"
	Summary string `json:"summary"` // "6 регионов, 97 городов"
	Cities  int    `json:"cities"`
}

// City — город и его идентификаторы на сайтах-источниках.
type City struct {
	ID     string            `json:"id"`     // стабильный ключ, например "kazan"
	Name   string            `json:"name"`   // "Казань"
	Region string            `json:"region"` // "Республика Татарстан"
	Slugs  map[string]string `json:"slugs"`  // source -> slug
	Lat    float64           `json:"lat"`
	Lon    float64           `json:"lon"`
}

// Region — регион и его города.
type Region struct {
	Name   string  `json:"name"`
	Cities []*City `json:"cities"`
}

// Catalog — загруженный справочник.
type Catalog struct {
	Regions   []*Region
	Presets   []Preset
	Neighbors map[string][]string // регион -> граничащие регионы справочника
	byID      map[string]*City
	bySlug    map[string]map[string]*City // source -> slug -> city
}

// Порядок колонок slug'ов в cities.txt.
var sourceColumns = []string{"prodoctorov", "napopravku", "zoon"}

// Load разбирает встроенные cities.txt и presets.txt.
func Load() (*Catalog, error) {
	c, err := Parse(citiesTxt)
	if err != nil {
		return nil, err
	}
	if err := c.ParseNeighbors(neighborsTxt); err != nil {
		return nil, err
	}
	if err := c.ParseCoords(coordsTxt); err != nil {
		return nil, err
	}
	return c, c.ParsePresets(presetsTxt)
}

// ParseNeighbors разбирает соседство "Регион|сосед1;сосед2". Связь всегда двусторонняя.
func (c *Catalog) ParseNeighbors(text string) error {
	c.Neighbors = map[string][]string{}
	add := func(a, b string) {
		for _, x := range c.Neighbors[a] {
			if x == b {
				return
			}
		}
		c.Neighbors[a] = append(c.Neighbors[a], b)
	}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		region, list, ok := strings.Cut(line, "|")
		if !ok || c.Region(region) == nil {
			return fmt.Errorf("neighbors.txt:%d: неизвестный регион %q", n+1, region)
		}
		for _, nb := range strings.Split(list, ";") {
			nb = strings.TrimSpace(nb)
			if nb == "" {
				continue
			}
			if c.Region(nb) == nil {
				return fmt.Errorf("neighbors.txt:%d: неизвестный сосед %q", n+1, nb)
			}
			add(region, nb)
			add(nb, region)
		}
	}
	return nil
}

// ParsePresets разбирает наборы "Название|цель1;цель2" и проверяет, что все цели существуют.
func (c *Catalog) ParsePresets(text string) error {
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, target, ok := strings.Cut(line, "|")
		if !ok {
			return fmt.Errorf("presets.txt:%d: ожидается Название|цели", n+1)
		}
		p := Preset{Name: strings.TrimSpace(name), Target: strings.TrimSpace(target)}
		cities, _, err := c.Resolve(p.Target)
		if err != nil {
			return fmt.Errorf("presets.txt:%d: %w", n+1, err)
		}
		regions := map[string]bool{}
		for _, city := range cities {
			regions[city.Region] = true
		}
		p.Cities = len(cities)
		p.Summary = fmt.Sprintf("%d рег., %d гор.", len(regions), len(cities))
		c.Presets = append(c.Presets, p)
	}
	return nil
}

// Parse разбирает справочник в формате "Регион|Город|slug prodoctorov|slug napopravku|slug zoon".
func Parse(text string) (*Catalog, error) {
	c := &Catalog{byID: map[string]*City{}, bySlug: map[string]map[string]*City{}}
	regionIdx := map[string]*Region{}
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 3 {
			return nil, fmt.Errorf("cities.txt:%d: ожидается Регион|Город|slug…", n+1)
		}
		city := &City{Region: strings.TrimSpace(parts[0]), Name: strings.TrimSpace(parts[1]), Slugs: map[string]string{}}
		for i, src := range sourceColumns {
			if 2+i >= len(parts) { // хвостовые колонки необязательны
				break
			}
			if s := strings.TrimSpace(parts[2+i]); s != "" {
				city.Slugs[src] = s
				if city.ID == "" {
					city.ID = s
				}
			}
		}
		if city.ID == "" { // город без источников не нужен
			continue
		}
		if _, dup := c.byID[city.ID]; dup {
			return nil, fmt.Errorf("cities.txt:%d: повтор id %q", n+1, city.ID)
		}
		c.byID[city.ID] = city
		for src, slug := range city.Slugs {
			if c.bySlug[src] == nil {
				c.bySlug[src] = map[string]*City{}
			}
			c.bySlug[src][slug] = city
		}
		r := regionIdx[city.Region]
		if r == nil {
			r = &Region{Name: city.Region}
			regionIdx[city.Region] = r
			c.Regions = append(c.Regions, r)
		}
		r.Cities = append(r.Cities, city)
	}
	// Регионы по алфавиту, но Москва и Петербург первыми.
	sort.SliceStable(c.Regions, func(i, j int) bool {
		pi, pj := priority(c.Regions[i].Name), priority(c.Regions[j].Name)
		if pi != pj {
			return pi < pj
		}
		return c.Regions[i].Name < c.Regions[j].Name
	})
	return c, nil
}

func priority(region string) int {
	switch region {
	case "Москва":
		return 0
	case "Санкт-Петербург":
		return 1
	case "Московская область":
		return 2
	}
	return 10
}

// City возвращает город по id.
func (c *Catalog) City(id string) *City { return c.byID[id] }

// CityBySlug ищет город по slug источника.
func (c *Catalog) CityBySlug(source, slug string) *City { return c.bySlug[source][slug] }

// Region возвращает регион по имени.
func (c *Catalog) Region(name string) *Region {
	for _, r := range c.Regions {
		if r.Name == name {
			return r
		}
	}
	return nil
}

// Resolve превращает цель в список городов. Цель: "city:<id>", "region:<имя>",
// "preset:<имя>" или несколько таких через ";". Повторы городов убираются.
func (c *Catalog) Resolve(target string) ([]*City, string, error) {
	var cities []*City
	var names []string
	seen := map[string]bool{}
	for _, part := range strings.Split(target, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		list, name, err := c.resolveOne(part)
		if err != nil {
			return nil, "", err
		}
		names = append(names, name)
		for _, city := range list {
			if !seen[city.ID] {
				seen[city.ID] = true
				cities = append(cities, city)
			}
		}
	}
	if len(cities) == 0 {
		return nil, "", fmt.Errorf("цель не задана")
	}
	return cities, strings.Join(names, ", "), nil
}

func (c *Catalog) resolveOne(target string) ([]*City, string, error) {
	kind, val, ok := strings.Cut(target, ":")
	if !ok {
		return nil, "", fmt.Errorf("цель должна быть вида city:<id>, region:<имя> или preset:<имя>")
	}
	switch kind {
	case "city":
		if city := c.City(val); city != nil {
			return []*City{city}, city.Name, nil
		}
	case "region":
		if r := c.Region(val); r != nil {
			return r.Cities, r.Name, nil
		}
	case "preset":
		for _, p := range c.Presets {
			if p.Name == val {
				cities, _, err := c.Resolve(p.Target)
				return cities, p.Name, err
			}
		}
	}
	return nil, "", fmt.Errorf("не найдено: %s", target)
}

// ParseCoords разбирает координаты "Регион|Город|широта|долгота".
func (c *Catalog) ParseCoords(text string) error {
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		p := strings.Split(line, "|")
		if len(p) != 4 {
			return fmt.Errorf("coords.txt:%d: ожидается Регион|Город|широта|долгота", n+1)
		}
		lat, err1 := strconv.ParseFloat(p[2], 64)
		lon, err2 := strconv.ParseFloat(p[3], 64)
		if err1 != nil || err2 != nil {
			return fmt.Errorf("coords.txt:%d: неверные координаты", n+1)
		}
		r := c.Region(p[0])
		if r == nil {
			return fmt.Errorf("coords.txt:%d: неизвестный регион %q", n+1, p[0])
		}
		found := false
		for _, city := range r.Cities {
			if city.Name == p[1] {
				city.Lat, city.Lon, found = lat, lon, true
			}
		}
		if !found {
			return fmt.Errorf("coords.txt:%d: неизвестный город %q", n+1, p[1])
		}
	}
	return nil
}

// DistanceKm — расстояние по прямой между городами (формула гаверсинусов).
func DistanceKm(a, b *City) float64 {
	const r = 6371.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLon := toRad(b.Lat-a.Lat), toRad(b.Lon-a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(toRad(a.Lat))*math.Cos(toRad(b.Lat))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// NearbyCity — город рядом с заданным.
type NearbyCity struct {
	City *City
	Km   float64
}

// Nearby возвращает города в радиусе km от города id, ближайшие первыми.
func (c *Catalog) Nearby(id string, km float64) []NearbyCity {
	base := c.City(id)
	if base == nil || (base.Lat == 0 && base.Lon == 0) {
		return nil
	}
	var out []NearbyCity
	for _, city := range c.byID {
		if city == base || (city.Lat == 0 && city.Lon == 0) {
			continue
		}
		if d := DistanceKm(base, city); d <= km {
			out = append(out, NearbyCity{City: city, Km: d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Km < out[j].Km })
	return out
}
