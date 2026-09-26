package sources

import (
	"os"
	"strings"
	"testing"

	"uziparser/internal/fetch"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNapopravkuParse(t *testing.T) {
	res, err := NewNapopravku(nil).Parse(load(t, "napopravku_kazan.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Doctors) < 15 {
		t.Fatalf("ожидалось ≥15 врачей на странице, получено %d", len(res.Doctors))
	}
	if res.TotalPages != 30 {
		t.Errorf("TotalPages = %d, ожидалось 30", res.TotalPages)
	}
	actual := 0
	for _, d := range res.Doctors {
		if len(strings.Fields(d.FIO)) < 2 {
			t.Errorf("странное ФИО %q", d.FIO)
		}
		if d.Position == "" || !strings.HasPrefix(d.URL, "https://napopravku.ru/") {
			t.Errorf("неполная запись: %+v", d)
		}
		if d.Actual {
			actual++
		}
	}
	if actual < len(res.Doctors)/2 {
		t.Errorf("актуальных слишком мало: %d из %d", actual, len(res.Doctors))
	}
	var found bool
	for _, d := range res.Doctors {
		if d.FIO == "Вайсс Хассан Мохамед" {
			found = true
			if d.Position != "Врач УЗД" || len(d.Workplaces) == 0 || d.Experience != "Стаж 27 лет" || !d.Actual {
				t.Errorf("карточка разобрана неверно: %+v", d)
			}
			if !strings.Contains(d.Workplaces[0], "Доктор Ходер") || !strings.Contains(d.Workplaces[0], "Чистопольская") {
				t.Errorf("место работы: %v", d.Workplaces)
			}
		}
	}
	if !found {
		t.Error("эталонный врач не найден")
	}
}

func TestProdoctorovParse(t *testing.T) {
	res, err := NewProdoctorov(nil).Parse(load(t, "prodoctorov_kazan.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Doctors) < 15 {
		t.Fatalf("ожидалось ≥15 врачей, получено %d", len(res.Doctors))
	}
	if res.TotalPages != 29 {
		t.Errorf("TotalPages = %d", res.TotalPages)
	}
	var found bool
	for _, d := range res.Doctors {
		if d.CitySlug == "" || d.Position == "" {
			t.Errorf("неполная запись: %+v", d)
		}
		if d.FIO == "Низамутдинов Вадим Мунирович" {
			found = true
			if d.Position != "Врач УЗИ" || d.CitySlug != "kazan" || !d.Actual {
				t.Errorf("карточка разобрана неверно: %+v", d)
			}
			if len(d.Specialties) != 3 || !strings.Contains(d.Experience, "Стаж 16 лет") {
				t.Errorf("специализации/стаж: %+v", d)
			}
			if len(d.Workplaces) == 0 || !strings.Contains(d.Workplaces[0], "Клиника восстановительной медицины") {
				t.Errorf("места работы: %v", d.Workplaces)
			}
		}
	}
	if !found {
		t.Error("эталонный врач не найден")
	}
}

func TestInactiveCard(t *testing.T) {
	html := `<div class="b-doctor-card" data-doctor-id="1" data-timetable-item='{"doctorFio":"Иванов Иван Иванович","lpuIds":[]}'>
	<div class="b-doctor-card__spec">Врач УЗИ</div><div>Врач не принимает</div></div>`
	res, _ := NewProdoctorov(nil).Parse([]byte(html))
	if len(res.Doctors) != 1 || res.Doctors[0].Actual {
		t.Fatalf("неактуальный врач должен быть отсеян: %+v", res.Doctors)
	}
}

func TestBlockDetection(t *testing.T) {
	p := &fetch.Page{FinalURL: "https://r.prodoctorov.ru/unblock?next=x", Status: 200, Body: load(t, "prodoctorov_unblock.html")}
	if !fetch.IsBlocked(p) {
		t.Error("антибот по адресу не распознан")
	}
	p.FinalURL = "https://prodoctorov.ru/moskva/ultrazvukovoy-diagnost/"
	if !fetch.IsBlocked(p) {
		t.Error("антибот по содержимому не распознан")
	}
	ok := &fetch.Page{FinalURL: "https://napopravku.ru/kazan/doctors/vrach-uzd/", Status: 200, Body: load(t, "napopravku_kazan.html")}
	if fetch.IsBlocked(ok) {
		t.Error("обычная страница ошибочно считается блокировкой")
	}
}

func TestSplitPosition(t *testing.T) {
	pos, specs := SplitPosition([]string{" уролог", "андролог ", "врач УЗИ", "уролог"})
	if pos != "Врач УЗИ" || len(specs) != 3 || specs[0] != "Уролог" {
		t.Errorf("pos=%q specs=%v", pos, specs)
	}
}

func TestListURL(t *testing.T) {
	if u := NewNapopravku(nil).ListURL("kazan", 3); u != "https://napopravku.ru/kazan/doctors/vrach-uzd/page-3/" {
		t.Error(u)
	}
	if u := NewProdoctorov(nil).ListURL("spb", 2); u != "https://prodoctorov.ru/spb/ultrazvukovoy-diagnost/?page=2" {
		t.Error(u)
	}
}

func TestReviewTextDoesNotMarkInactive(t *testing.T) {
	html := `<div itemscope itemtype="https://schema.org/Physician" class="doctor-card-v2">
	<meta itemprop="name" content="Петров Пётр Петрович"><div itemprop="jobTitle">врач УЗИ</div>
	<div class="compact-pinned-review-card">Хороший врач, жаль, что не принимает детей. Больше не пойду к другим.</div>
	<div class="workplace-address-card"><div class="workplace-address-card__name">Клиника</div></div>
	<button class="appointment-button">Записаться на прием</button></div>`
	res, _ := NewNapopravku(nil).Parse([]byte(html))
	if len(res.Doctors) != 1 || !res.Doctors[0].Actual {
		t.Fatalf("отзыв не должен делать врача неактуальным: %+v", res.Doctors)
	}
	// Маркер в служебной части карточки — по-прежнему отсеивает.
	res, _ = NewNapopravku(nil).Parse([]byte(strings.Replace(html, "<button", "<div>Врач не принимает</div><button", 1)))
	if res.Doctors[0].Actual {
		t.Fatal("служебная пометка «не принимает» должна отсеивать")
	}
}

func TestZoonParse(t *testing.T) {
	res, err := NewZoon(nil).Parse(load(t, "zoon_ekb.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Doctors) != 50 {
		t.Fatalf("ожидалось 50 карточек, получено %d", len(res.Doctors))
	}
	if res.TotalPages != 19 { // 903 врача / 50
		t.Errorf("TotalPages = %d, ожидалось 19", res.TotalPages)
	}
	actual := 0
	var found bool
	for _, d := range res.Doctors {
		if d.Actual {
			actual++
		}
		if !strings.HasPrefix(d.URL, "https://zoon.ru/ekb/p-doctor/") || d.Position == "" {
			t.Errorf("неполная запись: %+v", d)
		}
		if d.FIO == "Долгирев Данил Юрьевич" {
			found = true
			if d.Position != "Врач УЗИ" || len(d.Specialties) != 5 || d.Experience != "Стаж 31 год" || !d.Actual {
				t.Errorf("карточка: %+v", d)
			}
			if len(d.Workplaces) == 0 || !strings.Contains(d.Workplaces[0], "Кабинет врача") || !strings.Contains(d.Workplaces[0], "Космонавтов") {
				t.Errorf("место работы: %v", d.Workplaces)
			}
		}
	}
	if !found {
		t.Error("эталонный врач не найден (ФИО должно быть приведено к «Фамилия Имя Отчество»)")
	}
	t.Logf("актуальных %d из %d", actual, len(res.Doctors))
	if actual != 48 { // у двух врачей в снимке блок места работы пуст
		t.Errorf("актуальных %d, ожидалось 48", actual)
	}
	for _, d := range res.Doctors {
		if d.FIO == "Исаева Татьяна Владиславовна" { // несколько клиник: названия из <select>, адрес из блока расписания
			if len(d.Workplaces) < 1 || !strings.Contains(d.Workplaces[0], "ВитаМедика") || !strings.Contains(d.Workplaces[0], "Мамина-Сибиряка") {
				t.Errorf("Исаева: %v", d.Workplaces)
			}
		}
		if d.FIO == "Фишман Елена Анатольевна" && d.Actual {
			t.Error("карточка без места работы должна отсеиваться")
		}
	}
	if u := NewZoon(nil).ListURL("rostov", 3); u != "https://zoon.ru/rostov/p-doctor-vrach_uzi/page-3/" {
		t.Error(u)
	}
}

func TestReorderFIO(t *testing.T) {
	cases := map[string]string{
		"Данил Юрьевич Долгирев":  "Долгирев Данил Юрьевич",
		"Анна Сергеевна Иванова":  "Иванова Анна Сергеевна",
		"Иванова Анна Сергеевна":  "Иванова Анна Сергеевна",
		"Рашид Мамед оглы":        "Рашид Мамед оглы",
		"Лейла Ахмед кызы Алиева": "Лейла Ахмед кызы Алиева",
		"Ольга Петрова":           "Ольга Петрова",
	}
	for in, want := range cases {
		if got := ReorderFIO(in); got != want {
			t.Errorf("ReorderFIO(%q) = %q, want %q", in, got, want)
		}
	}
}
