package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// legacyState 是旧版 data/state.json 的结构，仅用于一次性迁移。
type legacyState struct {
	Users     map[string]*legacyUser `json:"users"`
	Libraries map[string]*Library    `json:"libraries"`
	Books     map[string]*Book       `json:"books"`
	Sessions  map[string]Session     `json:"sessions"`
}

type legacyUser struct {
	ID           string                `json:"id"`
	Username     string                `json:"username"`
	DisplayName  string                `json:"displayName"`
	PasswordHash string                `json:"passwordHash"`
	IsAdmin      bool                  `json:"isAdmin"`
	Libraries    []string              `json:"libraries"`
	CreatedAt    time.Time             `json:"createdAt"`
	Progress     map[string]*Progress  `json:"progress"`
	Bookmarks    map[string][]Bookmark `json:"bookmarks"`
	Daily        map[string]int        `json:"daily"`
}

// importLegacyJSON 在数据库为空时导入旧版 state.json，成功后把文件改名为 .migrated。
// 这样老用户升级后账号、书库、书架、进度、书签和会话都能原样保留。
func (s *Store) importLegacyJSON(dir string) error {
	legacyPath := filepath.Join(dir, "state.json")
	raw, err := os.ReadFile(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var users, libraries, books, sessions int
	if err := s.db.QueryRow(`SELECT
(SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM libraries),
(SELECT COUNT(*) FROM books), (SELECT COUNT(*) FROM sessions)`).
		Scan(&users, &libraries, &books, &sessions); err != nil {
		return err
	}
	if users > 0 || libraries > 0 || books > 0 || sessions > 0 {
		return nil
	}

	var legacy legacyState
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return fmt.Errorf("解析 state.json 失败：%w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	for id, l := range legacy.Libraries {
		if l == nil {
			continue
		}
		if l.ID == "" {
			l.ID = id
		}
		if l.CreatedAt.IsZero() {
			l.CreatedAt = now
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO libraries (id, name, path, type, created_at)
VALUES (?, ?, ?, ?, ?)`, l.ID, l.Name, l.Path, l.Type, toMS(l.CreatedAt)); err != nil {
			return err
		}
	}
	for id, b := range legacy.Books {
		if b == nil {
			continue
		}
		if b.ID == "" {
			b.ID = id
		}
		if b.AddedAt.IsZero() {
			b.AddedAt = now
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO books (`+bookColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			b.ID, b.LibraryID, b.Type, b.File, b.Title, b.Creator, b.Language, b.Publisher,
			b.Description, b.PublishedAt, b.ChapterCount, boolInt(b.HasCover), b.Size,
			b.ModTime, b.Checksum, toMS(b.AddedAt)); err != nil {
			return err
		}
	}
	for id, u := range legacy.Users {
		if u == nil {
			continue
		}
		if u.ID == "" {
			u.ID = id
		}
		if u.CreatedAt.IsZero() {
			u.CreatedAt = now
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO users
(id, username, display_name, password_hash, is_admin, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			u.ID, u.Username, u.DisplayName, u.PasswordHash, boolInt(u.IsAdmin), toMS(u.CreatedAt)); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, libID := range u.Libraries {
			if libID == "" || seen[libID] {
				continue
			}
			seen[libID] = true
			if _, err := tx.Exec(`INSERT OR IGNORE INTO user_libraries (user_id, library_id)
VALUES (?, ?)`, u.ID, libID); err != nil {
				return err
			}
		}
		for bookID, p := range u.Progress {
			if p == nil {
				continue
			}
			var updated any
			if p.UpdatedAt != nil {
				updated = toMS(*p.UpdatedAt)
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO progress
(user_id, book_id, chapter, chapter_percent, percent, finished, seconds, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				u.ID, bookID, p.Chapter, p.ChapterPercent, p.Percent,
				boolInt(p.Finished), p.Seconds, updated); err != nil {
				return err
			}
		}
		for bookID, list := range u.Bookmarks {
			for _, bm := range list {
				if bm.ID == "" {
					bm.ID = randID()
				}
				if bm.CreatedAt.IsZero() {
					bm.CreatedAt = now
				}
				if _, err := tx.Exec(`INSERT OR REPLACE INTO bookmarks
(id, user_id, book_id, chapter, chapter_title, percent, snippet, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
					bm.ID, u.ID, bookID, bm.Chapter, bm.ChapterTitle, bm.Percent,
					bm.Snippet, toMS(bm.CreatedAt)); err != nil {
					return err
				}
			}
		}
		for day, seconds := range u.Daily {
			if day == "" || seconds <= 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO daily (user_id, day, seconds) VALUES (?, ?, ?)`,
				u.ID, day, seconds); err != nil {
				return err
			}
		}
	}
	for key, sess := range legacy.Sessions {
		if sess.UserID == "" || time.Now().After(sess.ExpiresAt) {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO sessions (key, user_id, created_at, expires_at)
VALUES (?, ?, ?, ?)`, key, sess.UserID, toMS(sess.CreatedAt), toMS(sess.ExpiresAt)); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_ = os.Rename(legacyPath, legacyPath+".migrated")
	return nil
}
