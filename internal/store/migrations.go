package store

import (
	"database/sql"
	"fmt"
)

// schemaVersion 是当前数据库结构版本。修改表结构时递增，并在 migrate 中
// 添加对应的升级步骤，避免升级后旧库缺少字段。
const schemaVersion = 7

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("database version %d is newer than the supported %d", version, schemaVersion)
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
	if version < 4 {
		if err := migrateV4(tx); err != nil {
			return err
		}
		version = 4
	}
	if version < 5 {
		if err := migrateV5(tx); err != nil {
			return err
		}
		version = 5
	}
	if version < 6 {
		if err := migrateV6(tx); err != nil {
			return err
		}
		version = 6
	}
	if version < 7 {
		if err := migrateV7(tx); err != nil {
			return err
		}
		version = 7
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

// migrateV4 增加 WebDAV 书库字段。
func migrateV4(tx *sql.Tx) error {
	columns := []struct {
		name string
		sql  string
	}{
		{"kind", `ALTER TABLE libraries ADD COLUMN kind TEXT NOT NULL DEFAULT 'local'`},
		{"remote_url", `ALTER TABLE libraries ADD COLUMN remote_url TEXT NOT NULL DEFAULT ''`},
		{"remote_user", `ALTER TABLE libraries ADD COLUMN remote_user TEXT NOT NULL DEFAULT ''`},
		{"remote_pass", `ALTER TABLE libraries ADD COLUMN remote_pass TEXT NOT NULL DEFAULT ''`},
	}
	for _, c := range columns {
		ok, err := hasColumn(tx, "libraries", c.name)
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
	return nil
}

// migrateV5 增加 OIDC 身份字段，登录账号改由外部身份提供商维护。
func migrateV5(tx *sql.Tx) error {
	columns := []struct {
		name string
		sql  string
	}{
		{"oidc_issuer", `ALTER TABLE users ADD COLUMN oidc_issuer TEXT NOT NULL DEFAULT ''`},
		{"oidc_subject", `ALTER TABLE users ADD COLUMN oidc_subject TEXT NOT NULL DEFAULT ''`},
		{"email", `ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT ''`},
		{"avatar_url", `ALTER TABLE users ADD COLUMN avatar_url TEXT NOT NULL DEFAULT ''`},
	}
	for _, c := range columns {
		ok, err := hasColumn(tx, "users", c.name)
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
	_, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc
    ON users(oidc_issuer, oidc_subject) WHERE oidc_subject <> ''`)
	if err != nil {
		return err
	}
	// 本地密码登录已移除：旧账号不能再作为管理员使用，旧会话也必须失效，
	// 强制所有用户重新走 OIDC 登录。
	if _, err := tx.Exec(`UPDATE users SET is_admin = 0 WHERE oidc_subject = ''`); err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM sessions`)
	return err
}

// migrateV6 在会话中保存 ID Token，用于 RP 主动注销的 id_token_hint。
func migrateV6(tx *sql.Tx) error {
	ok, err := hasColumn(tx, "sessions", "id_token")
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE sessions ADD COLUMN id_token TEXT NOT NULL DEFAULT ''`)
	return err
}

// migrateV7 删除本地密码登录遗留的 password_hash 字段。
func migrateV7(tx *sql.Tx) error {
	ok, err := hasColumn(tx, "users", "password_hash")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE users DROP COLUMN password_hash`)
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
