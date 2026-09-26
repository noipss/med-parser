package store

import (
	"fmt"

	"github.com/xuri/excelize/v2"
)

var columns = []struct {
	title string
	width float64
}{
	{"№", 6}, {"Запись (ФИО — Должность; сфера работы)", 70}, {"ФИО", 34}, {"Должность", 24},
	{"Сфера работы (специализации)", 40}, {"Место работы", 60}, {"Город", 18}, {"Регион", 26},
	{"Стаж / категория", 26}, {"Источники", 22}, {"Ссылки", 60}, {"Проверено", 20},
}

// ExportXLSX пишет врачей сессии в Excel-файл и возвращает число строк.
func (s *Store) ExportXLSX(sessionID int64, path string) (int, error) {
	si, err := s.Session(sessionID)
	if err != nil {
		return 0, err
	}
	recs, err := s.SessionDoctors(si.ID)
	if err != nil {
		return 0, err
	}

	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Врачи УЗИ"
	f.SetSheetName("Sheet1", sheet)

	head, _ := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"1F4E79"}, Pattern: 1},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
	})
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return 0, err
	}
	if err := sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return 0, err
	}
	hdr := make([]any, len(columns))
	for i, c := range columns {
		_ = sw.SetColWidth(i+1, i+1, c.width)
		hdr[i] = excelize.Cell{StyleID: head, Value: c.title}
	}
	if err := sw.SetRow("A1", hdr, excelize.RowOpts{Height: 30}); err != nil {
		return 0, err
	}
	for i, r := range recs {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		row := []any{i + 1, r.Line(), r.FIO, r.Position, r.Specialties, r.Workplaces, r.City, r.Region,
			r.Experience, r.Sources, r.URLs, r.CheckedAt.Local().Format("02.01.2006 15:04")}
		if err := sw.SetRow(cell, row); err != nil {
			return 0, err
		}
	}
	last, _ := excelize.CoordinatesToCellName(len(columns), len(recs)+1)
	if err := sw.AddTable(&excelize.Table{Range: "A1:" + last, Name: "doctors", StyleName: "TableStyleLight9"}); err != nil && len(recs) > 0 {
		return 0, err
	}
	if err := sw.Flush(); err != nil {
		return 0, err
	}

	// Лист с параметрами выгрузки.
	const info = "Сессия"
	if _, err := f.NewSheet(info); err != nil {
		return 0, err
	}
	rows := [][]any{
		{"Цель", si.Target},
		{"Начало сессии", si.StartedAt.Local().Format("02.01.2006 15:04:05")},
		{"Актуальных врачей", len(recs)},
		{"Отсеяно неактуальных", si.Skipped},
		{"Критерий актуальности", "Врач найден в живом списке источника во время этой сессии, у него есть текущее место приёма и запись/телефон; пометки «не принимает» отсеяны"},
	}
	for i, r := range rows {
		_ = f.SetSheetRow(info, fmt.Sprintf("A%d", i+1), &r)
	}
	_ = f.SetColWidth(info, "A", "A", 26)
	_ = f.SetColWidth(info, "B", "B", 100)
	return len(recs), f.SaveAs(path)
}
