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
