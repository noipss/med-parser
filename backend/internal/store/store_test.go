package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestUpsertAndExport(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	old, _ := s.NewSession("city:kazan", time.Now())
	_ = s.Upsert([]Record{{FIO: "Старый Врач Петрович", FIOKey: "старый врач петрович", City: "Казань", CheckedAt: time.Now(), SessionID: old}})

	sid, _ := s.NewSession("city:kazan", time.Now())
	var recs []Record
	for i := 0; i < 2500; i++ {
		fio := fmt.Sprintf("Иванов%d Иван Иванович", i)
		recs = append(recs, Record{FIO: fio, FIOKey: fio, Position: "Врач УЗИ", Specialties: "Врач УЗИ; Гинеколог",
			Workplaces: "Клиника (адрес)", City: "Казань", Region: "Республика Татарстан", Sources: "napopravku",
			CheckedAt: time.Now(), SessionID: sid})
	}
	if err := s.Upsert(recs); err != nil {
		t.Fatal(err)
	}
	// повтор не создаёт дублей
	if err := s.Upsert(recs[:10]); err != nil {
		t.Fatal(err)
	}
	_ = s.FinishSession(sid, len(recs), 3, "done")

	got, err := s.SessionDoctors(sid)
	if err != nil || len(got) != 2500 {
		t.Fatalf("got %d, err %v", len(got), err)
	}
	if line := got[0].Line(); line != "Иванов0 Иван Иванович — Врач УЗИ; сфера: Гинеколог; место работы: Клиника (адрес)" {
		t.Errorf("Line() = %q", line)
	}

	out := filepath.Join(dir, "out.xlsx")
	n, err := s.ExportXLSX(sid, out)
	if err != nil || n != 2500 {
		t.Fatalf("export n=%d err=%v", n, err)
	}
	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := f.GetRows("Врачи УЗИ")
	if len(rows) != 2501 {
		t.Errorf("строк в Excel: %d (старая сессия не должна попадать)", len(rows))
	}
}
