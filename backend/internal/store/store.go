// Package store — SQLite-хранилище врачей и экспорт в Excel.
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Record — строка таблицы doctors (один врач в одном городе).
type Record struct {
	FIO         string    `json:"fio"`
	FIOKey      string    `json:"-"`
	Position    string    `json:"position"`
	Specialties string    `json:"specialties"`
	Workplaces  string    `json:"workplaces"`
	Experience  string    `json:"experience"`
	City        string    `json:"city"`
	Region      string    `json:"region"`
	Sources     string    `json:"sources"`
	URLs        string    `json:"urls"`
	CheckedAt   time.Time `json:"checkedAt"`
	SessionID   int64     `json:"-"`
}

// Line — запись в формате «ФИО — Должность; сфера работы».
func (r Record) Line() string {
	var b strings.Builder
	b.WriteString(r.FIO)
	b.WriteString(" — ")
	b.WriteString(r.Position)
	if other := otherSpecs(r.Specialties, r.Position); other != "" {
		b.WriteString("; сфера: ")
		b.WriteString(other)
	}
	if r.Workplaces != "" {
		b.WriteString("; место работы: ")
		b.WriteString(r.Workplaces)
	}
	return b.String()
}

func otherSpecs(specs, position string) string {
	var out []string
	for _, s := range strings.Split(specs, "; ") {
		if s != "" && !strings.EqualFold(s, position) {
			out = append(out, s)
		}
	}
	return strings.Join(out, ", ")
}

// Store — обёртка над SQLite.
type Store struct {
	db   *sql.DB
	Path string
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	target      TEXT NOT NULL,
	started_at  TEXT NOT NULL,
	finished_at TEXT,
	found       INTEGER NOT NULL DEFAULT 0,
	skipped     INTEGER NOT NULL DEFAULT 0,
	status      TEXT NOT NULL DEFAULT 'running'
);
CREATE TABLE IF NOT EXISTS doctors (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	fio         TEXT NOT NULL,
	fio_key     TEXT NOT NULL,
	position    TEXT NOT NULL DEFAULT '',
	specialties TEXT NOT NULL DEFAULT '',
	workplaces  TEXT NOT NULL DEFAULT '',
	experience  TEXT NOT NULL DEFAULT '',
	city        TEXT NOT NULL,
	region      TEXT NOT NULL DEFAULT '',
	sources     TEXT NOT NULL DEFAULT '',
	urls        TEXT NOT NULL DEFAULT '',
	first_seen  TEXT NOT NULL,
	checked_at  TEXT NOT NULL,
	session_id  INTEGER NOT NULL,
	UNIQUE (fio_key, city)
);
CREATE INDEX IF NOT EXISTS idx_doctors_session ON doctors(session_id);
`

// Open открывает (или создаёт) базу.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("инициализация БД: %w", err)
	}
	// Сессии, оборванные аварийным завершением, помечаем как прерванные.
	_, _ = db.Exec(`UPDATE sessions SET status='interrupted' WHERE status='running'`)
	return &Store{db: db, Path: path}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const timeFmt = time.RFC3339

// NewSession регистрирует новую сессию парсинга.
func (s *Store) NewSession(target string, started time.Time) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO sessions(target, started_at) VALUES(?, ?)`, target, started.Format(timeFmt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishSession фиксирует итог сессии.
func (s *Store) FinishSession(id int64, found, skipped int, status string) error {
	_, err := s.db.Exec(`UPDATE sessions SET finished_at=?, found=?, skipped=?, status=? WHERE id=?`,
		time.Now().Format(timeFmt), found, skipped, status, id)
	return err
}

// Upsert пишет пачку записей одной транзакцией.
func (s *Store) Upsert(recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`
INSERT INTO doctors (fio, fio_key, position, specialties, workplaces, experience, city, region, sources, urls, first_seen, checked_at, session_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (fio_key, city) DO UPDATE SET
	fio=excluded.fio, position=excluded.position, specialties=excluded.specialties,
	workplaces=excluded.workplaces, experience=excluded.experience, region=excluded.region,
	sources=excluded.sources, urls=excluded.urls, checked_at=excluded.checked_at, session_id=excluded.session_id`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range recs {
		ts := r.CheckedAt.Format(timeFmt)
		if _, err := st.Exec(r.FIO, r.FIOKey, r.Position, r.Specialties, r.Workplaces, r.Experience,
			r.City, r.Region, r.Sources, r.URLs, ts, ts, r.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SessionDoctors возвращает врачей, подтверждённых в сессии (только актуальные данные).
func (s *Store) SessionDoctors(sessionID int64) ([]Record, error) {
	rows, err := s.db.Query(`SELECT fio, position, specialties, workplaces, experience, city, region, sources, urls, checked_at
		FROM doctors WHERE session_id=? ORDER BY region, city, fio`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		var ts string
		if err := rows.Scan(&r.FIO, &r.Position, &r.Specialties, &r.Workplaces, &r.Experience, &r.City, &r.Region, &r.Sources, &r.URLs, &ts); err != nil {
			return nil, err
		}
		r.CheckedAt, _ = time.Parse(timeFmt, ts)
		r.SessionID = sessionID
		out = append(out, r)
	}
	return out, rows.Err()
}

// SessionInfo — сведения о сессии.
type SessionInfo struct {
	ID        int64
	Target    string
	StartedAt time.Time
	Found     int
	Skipped   int
	Status    string
}

// Session возвращает сессию по id; id=0 — последняя.
func (s *Store) Session(id int64) (*SessionInfo, error) {
	q := `SELECT id, target, started_at, found, skipped, status FROM sessions WHERE id=?`
	args := []any{id}
	if id == 0 {
		q = `SELECT id, target, started_at, found, skipped, status FROM sessions ORDER BY id DESC LIMIT 1`
		args = nil
	}
	var si SessionInfo
	var ts string
	if err := s.db.QueryRow(q, args...).Scan(&si.ID, &si.Target, &ts, &si.Found, &si.Skipped, &si.Status); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("сессий ещё не было")
		}
		return nil, err
	}
	si.StartedAt, _ = time.Parse(timeFmt, ts)
	return &si, nil
}
