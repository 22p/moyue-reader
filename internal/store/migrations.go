package store

import (
	"database/sql"
	"fmt"
)

// schemaVersion 是当前数据库结构版本。修改表结构时递增，并在 migrate 中
// 添加对应的升级步骤，避免升级后旧库缺少字段。
const schemaVersion = 3

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于程序支持的 %d", version, schemaVersion)
	}
	if version == 0 {
		version = 1
		if _, err := s.db.Exec(`PRAGMA user_version = 1`); err != nil {
			return err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if version < 2 {
		if err := migrateV2(tx); err != nil {
			return err
		}
		version = 2
	}
	if version < 3 {
		if err := migrateV3(tx); err != nil {
			return err
		}
		version = 3
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, version)); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV2 增加会话设备信息与批注表。
func migrateV2(tx *sql.Tx) error {
	columns := []struct {
		name string
		sql  string
	}{
		{"id", `ALTER TABLE sessions ADD COLUMN id TEXT NOT NULL DEFAULT ''`},
		{"user_agent", `ALTER TABLE sessions ADD COLUMN user_agent TEXT NOT NULL DEFAULT ''`},
		{"ip", `ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT ''`},
		{"last_seen", `ALTER TABLE sessions ADD COLUMN last_seen INTEGER NOT NULL DEFAULT 0`},
	}
	for _, c := range columns {
		ok, err := hasColumn(tx, "sessions", c.name)
		if err != nil {
			return err
		}
		if ok {
			continue
		}
		if _, err := tx.Exec(c.sql); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`UPDATE sessions SET last_seen = created_at WHERE last_seen = 0`); err != nil {
		return err
	}

	rows, err := tx.Query(`SELECT key FROM sessions WHERE id = ''`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, key)
	}
	rows.Close()
	for _, key := range keys {
		if _, err := tx.Exec(`UPDATE sessions SET id = ? WHERE key = ?`, randID(), key); err != nil {
			return err
		}
	}

	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS annotations (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id      TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    chapter      INTEGER NOT NULL DEFAULT 0,
    start_offset INTEGER NOT NULL DEFAULT 0,
    end_offset   INTEGER NOT NULL DEFAULT 0,
    text         TEXT NOT NULL DEFAULT '',
    note         TEXT NOT NULL DEFAULT '',
    color        TEXT NOT NULL DEFAULT 'yellow',
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_annotations_user_book
    ON annotations(user_id, book_id, chapter, start_offset);
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_id ON sessions(id);
`)
	return err
}

// migrateV3 增加全局配置表（目前用于语音朗读的接口设置）。
func migrateV3(tx *sql.Tx) error {
	_, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);`)
	return err
}

func hasColumn(tx *sql.Tx, table, column string) (bool, error) {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
