package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFreshSchemaHasFullSessionsColumns(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	rows, err := s.db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		have[name] = true
	}
	for _, name := range []string{"key", "id", "user_id", "user_agent", "ip", "id_token", "created_at", "last_seen", "expires_at"} {
		if !have[name] {
			t.Fatalf("fresh sessions table is missing column %q: %v", name, have)
		}
	}
}

func TestMaintainPrunesStaleData(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	u := &User{Username: "reader"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	daysAgo := func(n int) string { return now.AddDate(0, 0, -n).Format("2006-01-02") }
	for _, day := range []string{daysAgo(0), daysAgo(399), daysAgo(401)} {
		if _, err := s.db.Exec(`INSERT INTO daily (user_id, day, seconds) VALUES (?, ?, 60)`, u.ID, day); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO sessions
(key, id, user_id, user_agent, ip, id_token, created_at, last_seen, expires_at)
VALUES ('live', 'sess-live', ?, '', '', '', ?, ?, ?)`,
		u.ID, toMS(now), toMS(now), toMS(now.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO sessions
(key, id, user_id, user_agent, ip, id_token, created_at, last_seen, expires_at)
VALUES ('stale', 'sess-stale', ?, '', '', '', ?, ?, ?)`,
		u.ID, toMS(now.Add(-48*time.Hour)), toMS(now.Add(-48*time.Hour)), toMS(now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}

	if err := s.Maintain(); err != nil {
		t.Fatal(err)
	}

	var days []string
	rows, err := s.db.Query(`SELECT day FROM daily ORDER BY day`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var day string
		if err := rows.Scan(&day); err != nil {
			t.Fatal(err)
		}
		days = append(days, day)
	}
	rows.Close()
	if len(days) != 2 || days[0] != daysAgo(399) || days[1] != daysAgo(0) {
		t.Fatalf("daily after maintenance = %v", days)
	}

	var sessions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Fatalf("sessions after maintenance = %d, want 1", sessions)
	}
}

func TestMaintainCompactsSparseDatabase(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	u := &User{Username: "reader"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	padding := strings.Repeat("x", 2048)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1200; i++ {
		if _, err := tx.Exec(`INSERT INTO sessions
(key, id, user_id, user_agent, ip, id_token, created_at, last_seen, expires_at)
VALUES (?, ?, ?, ?, '', '', ?, ?, ?)`,
			fmt.Sprintf("filler-%d", i), fmt.Sprintf("id-%d", i), u.ID, padding,
			toMS(now), toMS(now), toMS(now.Add(time.Hour))); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	var free int64
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free < vacuumMinFreePages {
		t.Fatalf("freelist_count = %d, want at least %d for this test", free, vacuumMinFreePages)
	}

	// 过期会话清理不会删掉已删除页；只有 VACUUM 才能把文件收回去。
	if err := s.Maintain(); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free != 0 {
		t.Fatalf("freelist_count after VACUUM = %d, want 0", free)
	}
}
