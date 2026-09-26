package geo

import "testing"

func TestCatalog(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.Regions {
		for _, city := range r.Cities {
			if len(city.Slugs) == 0 {
				t.Errorf("%s без slug'ов", city.Name)
			}
		}
	}
	rostov := c.Region("Ростовская область")
	if rostov == nil || len(rostov.Cities) < 15 {
		t.Fatalf("Ростовская область: %v", rostov)
	}
	for _, id := range []string{"taganrog", "shahty", "novocherkassk", "bataysk", "volgodonsk", "azov"} {
		city := c.City(id)
		if city == nil || city.Slugs["prodoctorov"] == "" || city.Slugs["napopravku"] == "" {
			t.Errorf("%s: нет на обоих источниках: %+v", id, city)
		}
	}
}

func TestResolveMultiAndPreset(t *testing.T) {
	c, _ := Load()
	cities, name, err := c.Resolve("city:taganrog;region:Ростовская область;city:krasnodar")
	if err != nil {
		t.Fatal(err)
	}
	if len(cities) != len(c.Region("Ростовская область").Cities)+1 {
		t.Errorf("дубли не убраны: %d", len(cities))
	}
	if name != "Таганрог, Ростовская область, Краснодар" {
		t.Errorf("name=%q", name)
	}
	if len(c.Presets) < 2 {
		t.Fatal("наборы не загружены")
	}
	cities, _, err = c.Resolve("preset:Ростов и соседние регионы")
	if err != nil || len(cities) < 60 {
		t.Fatalf("набор: %d городов, %v", len(cities), err)
	}
	if _, _, err := c.Resolve("city:nope;city:kazan"); err == nil {
		t.Error("неизвестная цель должна давать ошибку")
	}
}

func TestNeighbors(t *testing.T) {
	c, _ := Load()
	for r, list := range c.Neighbors {
		for _, nb := range list {
			found := false
			for _, back := range c.Neighbors[nb] {
				found = found || back == r
			}
			if !found || nb == r {
				t.Errorf("соседство %s — %s несимметрично", r, nb)
			}
		}
	}
	want := map[string]bool{"Воронежская область": true, "Волгоградская область": true, "Республика Калмыкия": true,
		"Ставропольский край": true, "Краснодарский край": true, "Донецкая Народная Республика": true, "Луганская Народная Республика": true}
	got := c.Neighbors["Ростовская область"]
	if len(got) != len(want) {
		t.Fatalf("соседи Ростовской области: %v", got)
	}
	for _, nb := range got {
		if !want[nb] {
			t.Errorf("лишний сосед: %s", nb)
		}
	}
	if err := c.ParseNeighbors("Ростовская область|Атлантида"); err == nil {
		t.Error("неизвестный регион должен давать ошибку")
	}
}

func TestCoordsAndNearby(t *testing.T) {
	c, _ := Load()
	for _, r := range c.Regions {
		for _, city := range r.Cities {
			if city.Lat < 41 || city.Lat > 70 || city.Lon < 19 || city.Lon > 180 {
				t.Errorf("%s: координаты вне России: %v, %v", city.Name, city.Lat, city.Lon)
			}
		}
	}
	near := c.Nearby("rostov-na-donu", 30)
	var names []string
	for _, n := range near {
		names = append(names, n.City.Name)
	}
	if len(near) != 3 || names[0] != "Батайск" || names[1] != "Аксай" || names[2] != "Азов" {
		t.Errorf("в 30 км от Ростова ожидались Батайск, Аксай, Азов; получено %v", names)
	}
	if d := DistanceKm(c.City("rostov-na-donu"), c.City("taganrog")); d < 55 || d > 65 {
		t.Errorf("Ростов—Таганрог %.0f км", d)
	}
	if len(c.Nearby("nope", 100)) != 0 {
		t.Error("неизвестный город")
	}
}
