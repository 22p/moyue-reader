// Package store 负责书库数据的持久化。
//
// 数据保存在 data/moyue.db（SQLite，纯 Go 驱动，无需 CGO）。
// 书库、书籍、账号、会话、阅读进度、书签、每日时长各占一张表，
// 写操作只影响相关行，不再整份重写文件。外键开启级联删除，
// 删除书库/书籍/账号时会一并清理其阅读数据。
package store

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrNotFound 表示对象不存在。
	ErrNotFound = errors.New("对象不存在")
	// ErrConflict 表示用户名/书库名等唯一字段冲突。
	ErrConflict = errors.New("已存在同名记录")
	// ErrLastAdmin 表示不能删除最后一个管理员。
	ErrLastAdmin = errors.New("至少需要保留一个管理员")
)

// Progress 是某个用户在某本书上的阅读进度。
type Progress struct {
	Chapter        int        `json:"chapter"`
	ChapterPercent float64    `json:"chapterPercent"`
	Percent        float64    `json:"percent"`
	Finished       bool       `json:"finished"`
	Seconds        int64      `json:"seconds"`
	UpdatedAt      *time.Time `json:"updatedAt,omitempty"`
}

// Bookmark 是用户添加的书签。
type Bookmark struct {
	ID           string    `json:"id"`
	Chapter      int       `json:"chapter"`
	ChapterTitle string    `json:"chapterTitle"`
	Percent      float64   `json:"percent"`
	Snippet      string    `json:"snippet"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Book 是书库中的一本书。
type Book struct {
	ID           string    `json:"id"`
	LibraryID    string    `json:"libraryId"`
	Type         string    `json:"type"` // epub / txt / pdf / cbz / cbr
	File         string    `json:"file"` // 书库内的相对路径
	Title        string    `json:"title"`
	Creator      string    `json:"creator"`
	Language     string    `json:"language"`
	Publisher    string    `json:"publisher"`
	Description  string    `json:"description"`
	PublishedAt  string    `json:"publishedAt"`
	ChapterCount int       `json:"chapterCount"`
	HasCover     bool      `json:"hasCover"`
	Size         int64     `json:"size"`
	ModTime      int64     `json:"modTime"`
	Checksum     string    `json:"checksum,omitempty"`
	AddedAt      time.Time `json:"addedAt"`
}

// Library 是一个书库目录，Kind 为 local 或 webdav。
type Library struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Path       string    `json:"path"` // 本地目录；WebDAV 书库为下载缓存目录
	Type       string    `json:"type"` // mixed / ebook / epub / txt / comic / pdf
	Kind       string    `json:"kind"` // local / webdav
	RemoteURL  string    `json:"remoteUrl,omitempty"`
	RemoteUser string    `json:"remoteUser,omitempty"`
	RemotePass string    `json:"-"`
	CreatedAt  time.Time `json:"createdAt"`
}

// User 是一个账号。阅读进度、书签与统计按 user_id 单独存表。
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	DisplayName  string    `json:"displayName"`
	PasswordHash string    `json:"passwordHash"`
	IsAdmin      bool      `json:"isAdmin"`
	Libraries    []string  `json:"libraries"`
	CreatedAt    time.Time `json:"createdAt"`
}

// Session 是登录会话（key 为 token 的 SHA-256）。
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	UserAgent string    `json:"userAgent"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"createdAt"`
	LastSeen  time.Time `json:"lastSeen"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Store 是 SQLite 支持的仓库，可被多个请求并发使用。
type Store struct {
	db   *sql.DB
	path string
}

const schema = `
CREATE TABLE IF NOT EXISTS libraries (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL COLLATE NOCASE UNIQUE,
    path        TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT 'mixed',
    kind        TEXT NOT NULL DEFAULT 'local',
    remote_url  TEXT NOT NULL DEFAULT '',
    remote_user TEXT NOT NULL DEFAULT '',
    remote_pass TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL COLLATE NOCASE UNIQUE,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    is_admin      INTEGER NOT NULL DEFAULT 0,
    created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS user_libraries (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, library_id)
);
CREATE INDEX IF NOT EXISTS idx_user_libraries_lib ON user_libraries(library_id);

CREATE TABLE IF NOT EXISTS books (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    type          TEXT NOT NULL,
    file          TEXT NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    creator       TEXT NOT NULL DEFAULT '',
    language      TEXT NOT NULL DEFAULT '',
    publisher     TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    published_at  TEXT NOT NULL DEFAULT '',
    chapter_count INTEGER NOT NULL DEFAULT 0,
    has_cover     INTEGER NOT NULL DEFAULT 0,
    size          INTEGER NOT NULL DEFAULT 0,
    mod_time      INTEGER NOT NULL DEFAULT 0,
    checksum      TEXT NOT NULL DEFAULT '',
    added_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_books_library ON books(library_id);
CREATE INDEX IF NOT EXISTS idx_books_checksum ON books(library_id, checksum);

CREATE TABLE IF NOT EXISTS sessions (
    key        TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS progress (
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id         TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    chapter         INTEGER NOT NULL DEFAULT 0,
    chapter_percent REAL NOT NULL DEFAULT 0,
    percent         REAL NOT NULL DEFAULT 0,
    finished        INTEGER NOT NULL DEFAULT 0,
    seconds         INTEGER NOT NULL DEFAULT 0,
    updated_at      INTEGER,
    PRIMARY KEY (user_id, book_id)
);
CREATE INDEX IF NOT EXISTS idx_progress_book ON progress(book_id);

CREATE TABLE IF NOT EXISTS bookmarks (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    chapter       INTEGER NOT NULL DEFAULT 0,
    chapter_title TEXT NOT NULL DEFAULT '',
    percent       REAL NOT NULL DEFAULT 0,
    snippet       TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bookmarks_user_book ON bookmarks(user_id, book_id, created_at);

CREATE TABLE IF NOT EXISTS daily (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day     TEXT NOT NULL,
    seconds INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, day)
);

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT 0
);
`

// Open 打开（或创建）数据目录中的 SQLite 数据库。
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dbPath, err := filepath.Abs(filepath.Join(dir, "moyue.db"))
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dbPath)}).String() +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 单连接即可满足家庭/小团队规模，同时彻底避免 SQLITE_BUSY。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	s := &Store{db: db, path: dbPath}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	// 数据库内含密码哈希与会话，仅允许当前用户读写。
	_ = os.Chmod(dbPath, 0o600)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库：%w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Chmod(dbPath+suffix, 0o600)
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("升级数据库：%w", err)
	}
	if _, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, nowMS()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path 返回数据库文件路径。
func (s *Store) Path() string {
	return s.path
}

func nowMS() int64 { return time.Now().UnixMilli() }

func fromMS(ms int64) time.Time { return time.UnixMilli(ms) }

func toMS(t time.Time) int64 { return t.UnixMilli() }

func randID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return strings.Repeat("0", 16)
	}
	return hex.EncodeToString(buf[:])
}

// BookID 由书库与相对路径决定，文件内容变化时阅读进度仍然保留。
func BookID(libraryID, relPath string) string {
	sum := sha1.Sum([]byte(libraryID + "\x00" + relPath))
	return hex.EncodeToString(sum[:8])
}

// Checksum 计算内容摘要，用于上传去重。
func Checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "constraint failed")
}

/* ------------------------------- 用户 ------------------------------- */

// CountUsers 返回账号数量。
func (s *Store) CountUsers() int {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0
	}
	return n
}

// ListUsers 返回按创建时间排序的用户。
func (s *Store) ListUsers() []*User {
	rows, err := s.db.Query(`SELECT id, username, display_name, password_hash, is_admin, created_at
FROM users ORDER BY created_at, rowid`)
	if err != nil {
		return []*User{}
	}
	defer rows.Close()
	users := make([]*User, 0, 8)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return users
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return users
	}
	rows.Close()
	for _, u := range users {
		u.Libraries = s.userLibraries(u.ID)
	}
	return users
}

// GetUser 按 ID 查找用户。
func (s *Store) GetUser(id string) (*User, error) {
	row := s.db.QueryRow(`SELECT id, username, display_name, password_hash, is_admin, created_at
FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Libraries = s.userLibraries(u.ID)
	return u, nil
}

// GetUserByName 按用户名（忽略大小写）查找用户。
func (s *Store) GetUserByName(name string) (*User, error) {
	row := s.db.QueryRow(`SELECT id, username, display_name, password_hash, is_admin, created_at
FROM users WHERE username = ? COLLATE NOCASE`, name)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Libraries = s.userLibraries(u.ID)
	return u, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(row scanner) (*User, error) {
	var u User
	var admin int
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash, &admin, &created); err != nil {
		return nil, err
	}
	u.IsAdmin = admin != 0
	u.CreatedAt = fromMS(created)
	u.Libraries = []string{}
	return &u, nil
}

func (s *Store) userLibraries(userID string) []string {
	rows, err := s.db.Query(`SELECT ul.library_id FROM user_libraries ul
JOIN libraries l ON l.id = ul.library_id
WHERE ul.user_id = ? ORDER BY l.created_at, l.name`, userID)
	if err != nil {
		return []string{}
	}
	defer rows.Close()
	ids := make([]string, 0, 4)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ids
		}
		ids = append(ids, id)
	}
	return ids
}

// CreateUser 新增用户。
func (s *Store) CreateUser(u *User) error {
	if u.ID == "" {
		u.ID = randID()
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	if u.Libraries == nil {
		u.Libraries = []string{}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`INSERT INTO users (id, username, display_name, password_hash, is_admin, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.DisplayName, u.PasswordHash, boolInt(u.IsAdmin), toMS(u.CreatedAt))
	if err != nil {
		if isUniqueErr(err) {
			return ErrConflict
		}
		return err
	}
	if err := replaceUserLibraries(tx, u.ID, u.Libraries); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateUser 在事务内修改用户，fn 返回错误时不落盘。
func (s *Store) UpdateUser(id string, fn func(*User) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	row := tx.QueryRow(`SELECT id, username, display_name, password_hash, is_admin, created_at
FROM users WHERE id = ?`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := loadUserLibraries(tx, u); err != nil {
		return err
	}
	wasAdmin := u.IsAdmin
	if err := fn(u); err != nil {
		return err
	}
	// 降级管理员必须在同一事务内检查，避免并发请求同时把最后几个管理员降级。
	if wasAdmin && !u.IsAdmin {
		var admins int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}
	_, err = tx.Exec(`UPDATE users SET username = ?, display_name = ?, password_hash = ?, is_admin = ?
WHERE id = ?`, u.Username, u.DisplayName, u.PasswordHash, boolInt(u.IsAdmin), u.ID)
	if err != nil {
		if isUniqueErr(err) {
			return ErrConflict
		}
		return err
	}
	if err := replaceUserLibraries(tx, u.ID, u.Libraries); err != nil {
		return err
	}
	return tx.Commit()
}

func loadUserLibraries(tx *sql.Tx, u *User) error {
	rows, err := tx.Query(`SELECT ul.library_id FROM user_libraries ul
JOIN libraries l ON l.id = ul.library_id
WHERE ul.user_id = ? ORDER BY l.created_at, l.name`, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	u.Libraries = []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		u.Libraries = append(u.Libraries, id)
	}
	return rows.Err()
}

func replaceUserLibraries(tx *sql.Tx, userID string, libs []string) error {
	if _, err := tx.Exec(`DELETE FROM user_libraries WHERE user_id = ?`, userID); err != nil {
		return err
	}
	seen := make(map[string]bool, len(libs))
	for _, libID := range libs {
		if libID == "" || seen[libID] {
			continue
		}
		seen[libID] = true
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_libraries (user_id, library_id) VALUES (?, ?)`,
			userID, libID); err != nil {
			return err
		}
	}
	return nil
}

// DeleteUser 删除用户及其会话，禁止删除最后一个管理员。
func (s *Store) DeleteUser(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var admin int
	err = tx.QueryRow(`SELECT is_admin FROM users WHERE id = ?`, id).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if admin != 0 {
		var admins int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return ErrLastAdmin
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

/* ------------------------------- 会话 ------------------------------- */

// PutSession 保存会话（key 为 token 哈希）。
func (s *Store) PutSession(key string, sess Session) error {
	if sess.ID == "" {
		sess.ID = randID()
	}
	if sess.LastSeen.IsZero() {
		sess.LastSeen = sess.CreatedAt
	}
	if _, err := s.db.Exec(`INSERT INTO sessions
(key, id, user_id, user_agent, ip, created_at, last_seen, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET id = excluded.id, user_id = excluded.user_id,
    user_agent = excluded.user_agent, ip = excluded.ip,
    created_at = excluded.created_at, last_seen = excluded.last_seen,
    expires_at = excluded.expires_at`,
		key, sess.ID, sess.UserID, sess.UserAgent, sess.IP,
		toMS(sess.CreatedAt), toMS(sess.LastSeen), toMS(sess.ExpiresAt)); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, nowMS())
	return err
}

// GetSession 返回未过期的会话。
func (s *Store) GetSession(key string) (Session, bool) {
	var sess Session
	var created, lastSeen, expires int64
	err := s.db.QueryRow(`SELECT id, user_id, user_agent, ip, created_at, last_seen, expires_at
FROM sessions WHERE key = ? AND expires_at > ?`, key, nowMS()).
		Scan(&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP, &created, &lastSeen, &expires)
	if err != nil {
		return Session{}, false
	}
	sess.CreatedAt = fromMS(created)
	sess.LastSeen = fromMS(lastSeen)
	sess.ExpiresAt = fromMS(expires)
	return sess, true
}

// ListSessions 返回该用户全部未过期会话，按最后活跃时间倒序。
func (s *Store) ListSessions(userID string) []Session {
	rows, err := s.db.Query(`SELECT id, user_id, user_agent, ip, created_at, last_seen, expires_at
FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY last_seen DESC, created_at DESC`,
		userID, nowMS())
	if err != nil {
		return []Session{}
	}
	defer rows.Close()
	out := make([]Session, 0, 4)
	for rows.Next() {
		var sess Session
		var created, lastSeen, expires int64
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.UserAgent, &sess.IP,
			&created, &lastSeen, &expires); err != nil {
			return out
		}
		sess.CreatedAt = fromMS(created)
		sess.LastSeen = fromMS(lastSeen)
		sess.ExpiresAt = fromMS(expires)
		out = append(out, sess)
	}
	return out
}

// TouchSession 更新会话最后活跃时间（调用方负责节流）。
func (s *Store) TouchSession(key string, at time.Time) {
	_, _ = s.db.Exec(`UPDATE sessions SET last_seen = ? WHERE key = ? AND last_seen < ?`,
		toMS(at), key, toMS(at.Add(-time.Minute)))
}

// DeleteSessionByID 撤销该用户的一个会话。
func (s *Store) DeleteSessionByID(userID, id string) error {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUserSessions 注销该用户的全部登录会话。
func (s *Store) DeleteUserSessions(userID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteOtherSessions 保留当前会话，注销该用户的其它登录。
func (s *Store) DeleteOtherSessions(userID, keepKey string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND key <> ?`, userID, keepKey)
	return err
}

// DeleteSession 删除会话。
func (s *Store) DeleteSession(key string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE key = ?`, key)
	return err
}

/* ------------------------------- 书库 ------------------------------- */

// ListLibraries 返回按创建时间排序的书库。
func (s *Store) ListLibraries() []*Library {
	rows, err := s.db.Query(`SELECT id, name, path, type, kind, remote_url, remote_user, remote_pass, created_at
FROM libraries ORDER BY created_at, rowid`)
	if err != nil {
		return []*Library{}
	}
	defer rows.Close()
	libs := make([]*Library, 0, 4)
	for rows.Next() {
		var l Library
		var created int64
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &l.Type, &l.Kind,
			&l.RemoteURL, &l.RemoteUser, &l.RemotePass, &created); err != nil {
			return libs
		}
		if l.Kind == "" {
			l.Kind = "local"
		}
		l.CreatedAt = fromMS(created)
		libs = append(libs, &l)
	}
	return libs
}

// GetLibrary 按 ID 查找书库。
func (s *Store) GetLibrary(id string) (*Library, error) {
	var l Library
	var created int64
	err := s.db.QueryRow(`SELECT id, name, path, type, kind, remote_url, remote_user, remote_pass, created_at
FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Path, &l.Type, &l.Kind,
			&l.RemoteURL, &l.RemoteUser, &l.RemotePass, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.CreatedAt = fromMS(created)
	if l.Kind == "" {
		l.Kind = "local"
	}
	return &l, nil
}

// AddLibrary 新增书库。
func (s *Store) AddLibrary(l *Library) error {
	if l.ID == "" {
		l.ID = randID()
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now()
	}
	if l.Kind == "" {
		l.Kind = "local"
	}
	_, err := s.db.Exec(`INSERT INTO libraries
(id, name, path, type, kind, remote_url, remote_user, remote_pass, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.ID, l.Name, l.Path, l.Type, l.Kind, l.RemoteURL, l.RemoteUser, l.RemotePass, toMS(l.CreatedAt))
	if err != nil {
		if isUniqueErr(err) {
			return ErrConflict
		}
		return err
	}
	return nil
}

// DeleteLibrary 删除书库及其中的书籍，并清理用户授权与阅读记录（外键级联）。
func (s *Store) DeleteLibrary(id string) error {
	res, err := s.db.Exec(`DELETE FROM libraries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// BookCount 返回书库中的书籍数量。
func (s *Store) BookCount(libraryID string) int {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM books WHERE library_id = ?`, libraryID).Scan(&n); err != nil {
		return 0
	}
	return n
}

/* ------------------------------- 书籍 ------------------------------- */

const bookColumns = `id, library_id, type, file, title, creator, language, publisher,
description, published_at, chapter_count, has_cover, size, mod_time, checksum, added_at`

// AddBook 新增或更新书籍（以 ID 为准，保留首次入库时间）。
func (s *Store) AddBook(b *Book) error {
	if b.ID == "" {
		b.ID = BookID(b.LibraryID, b.File)
	}
	if b.AddedAt.IsZero() {
		b.AddedAt = time.Now()
	}
	var added int64
	err := s.db.QueryRow(`INSERT INTO books (`+bookColumns+`)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    library_id = excluded.library_id, type = excluded.type, file = excluded.file,
    title = excluded.title, creator = excluded.creator, language = excluded.language,
    publisher = excluded.publisher, description = excluded.description,
    published_at = excluded.published_at, chapter_count = excluded.chapter_count,
    has_cover = excluded.has_cover, size = excluded.size, mod_time = excluded.mod_time,
    checksum = excluded.checksum
RETURNING added_at`,
		b.ID, b.LibraryID, b.Type, b.File, b.Title, b.Creator, b.Language, b.Publisher,
		b.Description, b.PublishedAt, b.ChapterCount, boolInt(b.HasCover), b.Size,
		b.ModTime, b.Checksum, toMS(b.AddedAt)).Scan(&added)
	if err != nil {
		if isUniqueErr(err) {
			return ErrConflict
		}
		return err
	}
	b.AddedAt = fromMS(added)
	return nil
}

// GetBook 按 ID 查找书籍。
func (s *Store) GetBook(id string) (*Book, error) {
	row := s.db.QueryRow(`SELECT `+bookColumns+` FROM books WHERE id = ?`, id)
	b, err := scanBook(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

// FindBookByChecksum 在指定书库内按内容摘要查找。
func (s *Store) FindBookByChecksum(libraryID, checksum string) (*Book, bool) {
	if checksum == "" {
		return nil, false
	}
	row := s.db.QueryRow(`SELECT `+bookColumns+` FROM books
WHERE library_id = ? AND checksum = ? LIMIT 1`, libraryID, checksum)
	b, err := scanBook(row)
	if err != nil {
		return nil, false
	}
	return b, true
}

// ListBooks 返回全部书籍。
func (s *Store) ListBooks() []*Book {
	rows, err := s.db.Query(`SELECT ` + bookColumns + ` FROM books ORDER BY added_at, rowid`)
	if err != nil {
		return []*Book{}
	}
	defer rows.Close()
	books := make([]*Book, 0, 16)
	for rows.Next() {
		b, err := scanBook(rows)
		if err != nil {
			return books
		}
		books = append(books, b)
	}
	return books
}

func scanBook(row scanner) (*Book, error) {
	var b Book
	var cover int
	var added int64
	if err := row.Scan(&b.ID, &b.LibraryID, &b.Type, &b.File, &b.Title, &b.Creator,
		&b.Language, &b.Publisher, &b.Description, &b.PublishedAt, &b.ChapterCount,
		&cover, &b.Size, &b.ModTime, &b.Checksum, &added); err != nil {
		return nil, err
	}
	b.HasCover = cover != 0
	b.AddedAt = fromMS(added)
	return &b, nil
}

// CanAccess 判断用户是否有书库权限（管理员始终有）。
func (s *Store) CanAccess(u *User, libraryID string) bool {
	if u == nil {
		return false
	}
	if u.IsAdmin {
		return true
	}
	for _, id := range u.Libraries {
		if id == libraryID {
			return true
		}
	}
	return false
}

// PruneBooks 删除书库中不在 keep 集合里的书籍记录，并级联清理阅读数据。
// 返回删除的数量。
func (s *Store) PruneBooks(libraryID string, keep map[string]bool) (int, error) {
	rows, err := s.db.Query(`SELECT id FROM books WHERE library_id = ?`, libraryID)
	if err != nil {
		return 0, err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(stale) == 0 {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, id := range stale {
		if _, err := tx.Exec(`DELETE FROM books WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(stale), nil
}

// DeleteBook 删除书籍记录与所有用户的阅读数据。
func (s *Store) DeleteBook(id string) error {
	res, err := s.db.Exec(`DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

/* ----------------------------- 阅读数据 ----------------------------- */

// ProgressFor 返回用户在某本书上的进度。
func (s *Store) ProgressFor(userID, bookID string) Progress {
	var p Progress
	var finished int
	var updated sql.NullInt64
	err := s.db.QueryRow(`SELECT chapter, chapter_percent, percent, finished, seconds, updated_at
FROM progress WHERE user_id = ? AND book_id = ?`, userID, bookID).
		Scan(&p.Chapter, &p.ChapterPercent, &p.Percent, &finished, &p.Seconds, &updated)
	if err != nil {
		return Progress{}
	}
	p.Finished = finished != 0
	if updated.Valid {
		t := fromMS(updated.Int64)
		p.UpdatedAt = &t
	}
	return p
}

// ProgressMap 返回用户全部书的进度，书架列表用它避免逐本查询。
func (s *Store) ProgressMap(userID string) map[string]Progress {
	rows, err := s.db.Query(`SELECT book_id, chapter, chapter_percent, percent, finished, seconds, updated_at
FROM progress WHERE user_id = ?`, userID)
	if err != nil {
		return map[string]Progress{}
	}
	defer rows.Close()
	out := make(map[string]Progress, 16)
	for rows.Next() {
		var bookID string
		var p Progress
		var finished int
		var updated sql.NullInt64
		if err := rows.Scan(&bookID, &p.Chapter, &p.ChapterPercent, &p.Percent,
			&finished, &p.Seconds, &updated); err != nil {
			return out
		}
		p.Finished = finished != 0
		if updated.Valid {
			t := fromMS(updated.Int64)
			p.UpdatedAt = &t
		}
		out[bookID] = p
	}
	return out
}

// BookmarksFor 返回用户在某本书上的书签（新添加的在前）。
func (s *Store) BookmarksFor(userID, bookID string) []Bookmark {
	rows, err := s.db.Query(`SELECT id, chapter, chapter_title, percent, snippet, created_at
FROM bookmarks WHERE user_id = ? AND book_id = ? ORDER BY created_at DESC, rowid DESC`, userID, bookID)
	if err != nil {
		return []Bookmark{}
	}
	defer rows.Close()
	list := make([]Bookmark, 0, 8)
	for rows.Next() {
		var bm Bookmark
		var created int64
		if err := rows.Scan(&bm.ID, &bm.Chapter, &bm.ChapterTitle, &bm.Percent, &bm.Snippet, &created); err != nil {
			return list
		}
		bm.CreatedAt = fromMS(created)
		list = append(list, bm)
	}
	return list
}

// SaveProgress 保存进度，百分比由服务端按章节数计算。
func (s *Store) SaveProgress(userID, bookID string, chapter int, chapterPercent float64) (Progress, error) {
	var count int
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	err = s.db.QueryRow(`SELECT chapter_count FROM books WHERE id = ?`, bookID).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	if err != nil {
		return Progress{}, err
	}
	if count <= 0 {
		count = 1
	}
	if chapter < 0 {
		chapter = 0
	}
	if chapter >= count {
		chapter = count - 1
	}
	if chapterPercent < 0 {
		chapterPercent = 0
	}
	if chapterPercent > 1 {
		chapterPercent = 1
	}
	percent := (float64(chapter) + chapterPercent) / float64(count) * 100
	p := Progress{
		Chapter:        chapter,
		ChapterPercent: chapterPercent,
		Percent:        percent,
		Finished:       percent >= 99.5,
	}
	now := time.Now()
	p.UpdatedAt = &now

	_, err = s.db.Exec(`INSERT INTO progress
(user_id, book_id, chapter, chapter_percent, percent, finished, seconds, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 0, ?)
ON CONFLICT(user_id, book_id) DO UPDATE SET
    chapter = excluded.chapter, chapter_percent = excluded.chapter_percent,
    percent = excluded.percent, finished = excluded.finished,
    updated_at = excluded.updated_at`,
		userID, bookID, chapter, chapterPercent, percent, boolInt(p.Finished), toMS(now))
	if err != nil {
		return Progress{}, err
	}
	p.Seconds = s.progressSeconds(userID, bookID)
	return p, nil
}

func (s *Store) progressSeconds(userID, bookID string) int64 {
	var sec int64
	_ = s.db.QueryRow(`SELECT seconds FROM progress WHERE user_id = ? AND book_id = ?`,
		userID, bookID).Scan(&sec)
	return sec
}

// ResetProgress 清除一本书的进度（保留书签与累计时长）。
func (s *Store) ResetProgress(userID, bookID string) error {
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE progress SET chapter = 0, chapter_percent = 0, percent = 0,
finished = 0, updated_at = NULL WHERE user_id = ? AND book_id = ?`, userID, bookID)
	return err
}

// AddReading 累加阅读时长与当日统计。
func (s *Store) AddReading(userID, bookID string, seconds int) error {
	if seconds <= 0 {
		return nil
	}
	if seconds > 7200 {
		seconds = 7200
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := tx.QueryRow(`SELECT 1 FROM books WHERE id = ?`, bookID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_, err = tx.Exec(`INSERT INTO progress (user_id, book_id, seconds) VALUES (?, ?, ?)
ON CONFLICT(user_id, book_id) DO UPDATE SET seconds = progress.seconds + excluded.seconds`,
		userID, bookID, seconds)
	if err != nil {
		return err
	}
	day := time.Now().Format("2006-01-02")
	_, err = tx.Exec(`INSERT INTO daily (user_id, day, seconds) VALUES (?, ?, ?)
ON CONFLICT(user_id, day) DO UPDATE SET seconds = daily.seconds + excluded.seconds`,
		userID, day, seconds)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// AddBookmark 新增书签。
func (s *Store) AddBookmark(userID, bookID string, bm Bookmark) (Bookmark, error) {
	var exists int
	err := s.db.QueryRow(`SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return Bookmark{}, ErrNotFound
	}
	if err != nil {
		return Bookmark{}, err
	}
	err = s.db.QueryRow(`SELECT 1 FROM books WHERE id = ?`, bookID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return Bookmark{}, ErrNotFound
	}
	if err != nil {
		return Bookmark{}, err
	}
	bm.ID = randID()
	bm.CreatedAt = time.Now()
	_, err = s.db.Exec(`INSERT INTO bookmarks
(id, user_id, book_id, chapter, chapter_title, percent, snippet, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		bm.ID, userID, bookID, bm.Chapter, bm.ChapterTitle, bm.Percent, bm.Snippet, toMS(bm.CreatedAt))
	if err != nil {
		return Bookmark{}, err
	}
	return bm, nil
}

// DeleteBookmark 删除书签。
func (s *Store) DeleteBookmark(userID, bookID, bookmarkID string) error {
	res, err := s.db.Exec(`DELETE FROM bookmarks WHERE id = ? AND user_id = ? AND book_id = ?`,
		bookmarkID, userID, bookID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DailyStat 是某天的阅读时长。
type DailyStat struct {
	Date    string `json:"date"`
	Seconds int    `json:"seconds"`
}

// Stats 是用户维度的阅读统计。
type Stats struct {
	Books        int         `json:"books"`
	Finished     int         `json:"finished"`
	TotalSeconds int64       `json:"totalSeconds"`
	TodaySeconds int         `json:"todaySeconds"`
	Streak       int         `json:"streak"`
	Daily        []DailyStat `json:"daily"`
}

// Stats 汇总当前用户可见书籍与阅读数据。
func (s *Store) Stats(userID string) Stats {
	st := Stats{Daily: make([]DailyStat, 0, 7)}
	u, err := s.GetUser(userID)
	if err != nil {
		return st
	}

	if u.IsAdmin {
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&st.Books)
	} else {
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM books WHERE library_id IN
(SELECT library_id FROM user_libraries WHERE user_id = ?)`, userID).Scan(&st.Books)
	}

	var finished int
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(seconds), 0), COALESCE(SUM(finished), 0)
FROM progress WHERE user_id = ?`, userID).Scan(&st.TotalSeconds, &finished)
	st.Finished = finished

	now := time.Now()
	today := now.Format("2006-01-02")
	_ = s.db.QueryRow(`SELECT seconds FROM daily WHERE user_id = ? AND day = ?`,
		userID, today).Scan(&st.TodaySeconds)

	start := now
	if st.TodaySeconds == 0 {
		start = start.AddDate(0, 0, -1)
	}
	first := start.AddDate(0, 0, -3650).Format("2006-01-02")
	rows, err := s.db.Query(`SELECT day FROM daily WHERE user_id = ? AND day >= ? AND day <= ? AND seconds > 0
ORDER BY day DESC`, userID, first, start.Format("2006-01-02"))
	if err == nil {
		expected := start
		for rows.Next() {
			var day string
			if rows.Scan(&day) != nil || day != expected.Format("2006-01-02") {
				break
			}
			st.Streak++
			expected = expected.AddDate(0, 0, -1)
		}
		rows.Close()
	}

	for i := 6; i >= 0; i-- {
		key := now.AddDate(0, 0, -i).Format("2006-01-02")
		var sec int
		_ = s.db.QueryRow(`SELECT seconds FROM daily WHERE user_id = ? AND day = ?`,
			userID, key).Scan(&sec)
		st.Daily = append(st.Daily, DailyStat{Date: key, Seconds: sec})
	}
	return st
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
