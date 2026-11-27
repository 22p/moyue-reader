package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPerUserProgressIsolation(t *testing.T) {
	s := newTestStore(t)

	alice := &User{Username: "alice", DisplayName: "Alice"}
	bob := &User{Username: "bob", DisplayName: "Bob"}
	if err := s.CreateUser(alice); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(bob); err != nil {
		t.Fatal(err)
	}

	lib := &Library{Name: "小说", Path: t.TempDir(), Type: "mixed"}
	if err := s.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*User{alice, bob} {
		if err := s.UpdateUser(u.ID, func(user *User) error {
			user.Libraries = []string{lib.ID}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	book := &Book{
		LibraryID:    lib.ID,
		Type:         "txt",
		File:         "a.txt",
		Title:        "测试",
		ChapterCount: 10,
	}
	if err := s.AddBook(book); err != nil {
		t.Fatal(err)
	}

	p, err := s.SaveProgress(alice.ID, book.ID, 2, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if p.Percent < 24.9 || p.Percent > 25.1 {
		t.Errorf("percent = %v, want 25", p.Percent)
	}
	if got := s.ProgressFor(bob.ID, book.ID); got.UpdatedAt != nil {
		t.Error("bob should not see alice's progress")
	}

	if err := s.AddReading(alice.ID, book.ID, 90); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBookmark(alice.ID, book.ID, Bookmark{Chapter: 1, Snippet: "hello"}); err != nil {
		t.Fatal(err)
	}
	if len(s.BookmarksFor(bob.ID, book.ID)) != 0 {
		t.Error("bookmarks leaked between users")
	}

	stats := s.Stats(alice.ID)
	if stats.Books != 1 || stats.TotalSeconds != 90 {
		t.Errorf("alice stats = %+v", stats)
	}
	if bobStats := s.Stats(bob.ID); bobStats.Books != 1 || bobStats.TotalSeconds != 0 {
		t.Errorf("bob stats = %+v", bobStats)
	}
}

func TestLibraryAccessAndDeletion(t *testing.T) {
	s := newTestStore(t)
	u := &User{Username: "reader"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	lib1 := &Library{Name: "一", Path: t.TempDir(), Type: "mixed"}
	lib2 := &Library{Name: "二", Path: t.TempDir(), Type: "mixed"}
	_ = s.AddLibrary(lib1)
	_ = s.AddLibrary(lib2)

	if err := s.UpdateUser(u.ID, func(user *User) error {
		user.Libraries = []string{lib1.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUser(u.ID)
	if !s.CanAccess(got, lib1.ID) || s.CanAccess(got, lib2.ID) {
		t.Error("library permissions wrong")
	}

	b := &Book{LibraryID: lib1.ID, File: "b.epub", Type: "epub", ChapterCount: 2}
	_ = s.AddBook(b)
	_, _ = s.SaveProgress(u.ID, b.ID, 0, 0.5)

	if err := s.DeleteLibrary(lib1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetBook(b.ID); err == nil {
		t.Error("book should be removed with library")
	}
	if len(s.BookmarksFor(u.ID, b.ID)) != 0 {
		t.Error("book data should be dropped")
	}
	got, _ = s.GetUser(u.ID)
	if len(got.Libraries) != 0 {
		t.Errorf("library permission not cleaned: %v", got.Libraries)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := newTestStore(t)
	u := &User{Username: "u"}
	_ = s.CreateUser(u)

	now := time.Now()
	if err := s.PutSession("k1", Session{UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetSession("k1"); !ok {
		t.Error("session should exist")
	}
	if _, ok := s.GetSession("nope"); ok {
		t.Error("unknown session accepted")
	}
	if err := s.PutSession("k2", Session{UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOtherSessions(u.ID, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetSession("k1"); !ok {
		t.Error("current session should survive")
	}
	if _, ok := s.GetSession("k2"); ok {
		t.Error("other session should be removed")
	}
}

func TestBookIDStable(t *testing.T) {
	a := BookID("lib", "a/b.epub")
	b := BookID("lib", "a/b.epub")
	c := BookID("lib", "a/c.epub")
	if a != b {
		t.Error("BookID should be deterministic")
	}
	if a == c {
		t.Error("different paths should produce different IDs")
	}
}

func TestSQLitePersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Username: "reader", DisplayName: "读者"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Name: "主书库", Path: t.TempDir(), Type: "mixed"}
	if err := s.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateUser(u.ID, func(x *User) error {
		x.Libraries = []string{lib.ID}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b := &Book{LibraryID: lib.ID, File: "a.txt", Type: "txt", Title: "书", ChapterCount: 4}
	if err := s.AddBook(b); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProgress(u.ID, b.ID, 2, 0.25); err != nil {
		t.Fatal(err)
	}
	if err := s.AddReading(u.ID, b.ID, 125); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBookmark(u.ID, b.ID, Bookmark{Chapter: 2, Snippet: "摘录"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := s.PutSession("token", Session{UserID: u.ID, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	got, err := s2.GetUserByName("reader")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Libraries) != 1 || got.Libraries[0] != lib.ID {
		t.Errorf("libraries = %v", got.Libraries)
	}
	p := s2.ProgressFor(u.ID, b.ID)
	if p.Chapter != 2 || p.Seconds != 125 || p.UpdatedAt == nil {
		t.Errorf("progress = %+v", p)
	}
	if p.Percent < 56 || p.Percent > 57 {
		t.Errorf("percent = %v, want ~56.25", p.Percent)
	}
	if marks := s2.BookmarksFor(u.ID, b.ID); len(marks) != 1 || marks[0].Snippet != "摘录" {
		t.Errorf("bookmarks = %+v", marks)
	}
	if _, ok := s2.GetSession("token"); !ok {
		t.Error("session lost after reopen")
	}
	if stats := s2.Stats(u.ID); stats.TotalSeconds != 125 || stats.TodaySeconds != 125 {
		t.Errorf("stats = %+v", stats)
	}
}

func TestDeleteUserCascadesReadingData(t *testing.T) {
	s := newTestStore(t)
	u := &User{Username: "gone"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Name: "库", Path: t.TempDir(), Type: "mixed"}
	_ = s.AddLibrary(lib)
	b := &Book{LibraryID: lib.ID, File: "b.epub", Type: "epub", ChapterCount: 2}
	_ = s.AddBook(b)
	_, _ = s.SaveProgress(u.ID, b.ID, 1, 0.5)
	_, _ = s.AddBookmark(u.ID, b.ID, Bookmark{Chapter: 1})
	_ = s.AddReading(u.ID, b.ID, 30)

	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if p := s.ProgressFor(u.ID, b.ID); p.UpdatedAt != nil || p.Seconds != 0 {
		t.Errorf("progress not cascaded: %+v", p)
	}
	if len(s.BookmarksFor(u.ID, b.ID)) != 0 {
		t.Error("bookmarks not cascaded")
	}
}

func TestAnnotationCRUDAndIsolation(t *testing.T) {
	s := newTestStore(t)
	alice := &User{Username: "alice"}
	bob := &User{Username: "bob"}
	_ = s.CreateUser(alice)
	_ = s.CreateUser(bob)
	lib := &Library{Name: "批注库", Path: t.TempDir(), Type: "mixed"}
	_ = s.AddLibrary(lib)
	book := &Book{LibraryID: lib.ID, File: "a.epub", Type: "epub", ChapterCount: 3}
	_ = s.AddBook(book)

	a, err := s.CreateAnnotation(alice.ID, book.ID, Annotation{
		Chapter: 1, Start: 10, End: 24, Text: "一段划线文字", Note: "想法", Color: "yellow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.CreatedAt.IsZero() {
		t.Errorf("annotation = %+v", a)
	}
	if got := s.ListAnnotations(alice.ID, book.ID); len(got) != 1 || got[0].Note != "想法" {
		t.Errorf("alice annotations = %+v", got)
	}
	if got := s.ListAnnotations(bob.ID, book.ID); len(got) != 0 {
		t.Error("annotations leaked to another user")
	}
	note, color := "改过的想法", "green"
	updated, err := s.UpdateAnnotation(alice.ID, book.ID, a.ID, &note, &color)
	if err != nil || updated.Note != note || updated.Color != color {
		t.Fatalf("updated = %+v, err = %v", updated, err)
	}
	if err := s.DeleteAnnotation(bob.ID, book.ID, a.ID); err != ErrNotFound {
		t.Errorf("bob delete = %v, want ErrNotFound", err)
	}
	if err := s.DeleteAnnotation(alice.ID, book.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.ListAnnotations(alice.ID, book.ID)) != 0 {
		t.Error("annotation not deleted")
	}
}

func TestSessionDevices(t *testing.T) {
	s := newTestStore(t)
	u := &User{Username: "dev"}
	_ = s.CreateUser(u)
	now := time.Now()
	if err := s.PutSession("k1", Session{
		UserID: u.ID, UserAgent: "UA-1", IP: "192.0.2.1",
		CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSession("k2", Session{
		UserID: u.ID, UserAgent: "UA-2", IP: "192.0.2.2",
		CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	list := s.ListSessions(u.ID)
	if len(list) != 2 {
		t.Fatalf("sessions = %d", len(list))
	}
	for _, sess := range list {
		if sess.ID == "" || sess.UserAgent == "" || sess.IP == "" || sess.LastSeen.IsZero() {
			t.Errorf("session missing device fields: %+v", sess)
		}
	}
	if err := s.DeleteSessionByID(u.ID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := s.ListSessions(u.ID); len(got) != 1 {
		t.Errorf("sessions after revoke = %d", len(got))
	}
	if err := s.DeleteSessionByID("other-user", list[1].ID); err != ErrNotFound {
		t.Errorf("cross-user delete = %v", err)
	}
}

func TestMigrateV1ToCurrent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "moyue.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stmts := []string{
		`CREATE TABLE users (
  id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, display_name TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '', is_admin INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL
)`,
		`CREATE TABLE sessions (
  key TEXT PRIMARY KEY, user_id TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO users VALUES ('u1','old','Old','x',0,?)`, toMS(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions VALUES ('k1','u1',?,?)`, toMS(now), toMS(now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// v2 会补齐会话 id / last_seen；随后 v5 因移除本地登录而清空旧会话。
	if list := s.ListSessions("u1"); len(list) != 0 {
		t.Fatalf("legacy sessions should be cleared, got %d", len(list))
	}
	if _, err := s.db.Exec(`SELECT id_token FROM sessions LIMIT 1`); err != nil {
		t.Fatalf("sessions 缺少 id_token 字段: %v", err)
	}
	if u, err := s.GetUser("u1"); err != nil || u.IsAdmin {
		t.Fatalf("legacy user = %+v, err = %v", u, err)
	}
	// v7 会删除本地登录遗留的密码字段。
	if _, err := s.db.Exec(`SELECT password_hash FROM users LIMIT 1`); err == nil {
		t.Fatal("password_hash 字段应在迁移后删除")
	}
	var version int
	_ = s.db.QueryRow(`PRAGMA user_version`).Scan(&version)
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}
	if err := s.SetSetting("tts", `{"enabled":true}`); err != nil {
		t.Fatalf("settings table missing after migration: %v", err)
	}
	if got, err := s.GetSetting("tts"); err != nil || got != `{"enabled":true}` {
		t.Fatalf("setting = %q, err = %v", got, err)
	}
	if got, err := s.GetSetting("missing"); err != nil || got != "" {
		t.Fatalf("missing setting = %q, err = %v", got, err)
	}
}

func TestLastAdminCannotBeDemoted(t *testing.T) {
	s := newTestStore(t)
	a := &User{Username: "admin-a", IsAdmin: true}
	b := &User{Username: "admin-b", IsAdmin: true}
	if err := s.CreateUser(a); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser(b); err != nil {
		t.Fatal(err)
	}
	demote := func(id string) error {
		return s.UpdateUser(id, func(u *User) error {
			u.IsAdmin = false
			return nil
		})
	}
	if err := demote(a.ID); err != nil {
		t.Fatalf("demote first admin: %v", err)
	}
	if err := demote(b.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin = %v, want ErrLastAdmin", err)
	}
}

func TestUpsertOIDCUser(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	lib := &Library{Name: "OIDC 书库", Path: t.TempDir(), Type: "mixed"}
	if err := s.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	defaultLib := &Library{Name: "默认书库", Path: t.TempDir(), Type: "mixed"}
	if err := s.AddLibrary(defaultLib); err != nil {
		t.Fatal(err)
	}

	first, err := s.UpsertOIDCUser(OIDCIdentity{
		Issuer:            "https://id.example.com",
		Subject:           "sub-admin",
		Email:             "admin@example.com",
		DisplayName:       "Alice",
		PreferredUsername: "alice",
		Admin:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.IsAdmin || len(first.Libraries) != 2 {
		t.Fatalf("new admin = %+v", first)
	}

	// 再次登录：同一个人不重复建号，白名单移除后应降级，但书库权限保留。
	second, err := s.UpsertOIDCUser(OIDCIdentity{
		Issuer:            "https://id.example.com",
		Subject:           "sub-admin",
		Email:             "admin@example.com",
		PreferredUsername: "alice",
		Admin:             false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.IsAdmin {
		t.Fatalf("relogin = %+v, want same id and non-admin", second)
	}
	if len(second.Libraries) != 2 {
		t.Fatalf("libraries after demote = %v", second.Libraries)
	}

	// 同名 preferred_username 的另一个 subject 应得到唯一用户名。
	third, err := s.UpsertOIDCUser(OIDCIdentity{
		Issuer:            "https://id.example.com",
		Subject:           "sub-reader",
		Email:             "reader@example.com",
		PreferredUsername: "alice",
	})
	if err != nil {
		t.Fatal(err)
	}
	if third.Username == first.Username {
		t.Fatalf("duplicate username %q", third.Username)
	}
	// 普通用户登录即拥有默认书库，其它书库仍需管理员授权。
	if third.IsAdmin || len(third.Libraries) != 1 || third.Libraries[0] != defaultLib.ID {
		t.Fatalf("new non-admin = %+v", third)
	}
	if users := s.ListUsers(); len(users) != 2 {
		t.Fatalf("users = %d, want 2", len(users))
	}
}

func TestDefaultLibraryOpenToAllUsers(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	u := &User{Username: "reader"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	def := &Library{Name: "默认书库", Path: t.TempDir(), Type: "mixed"}
	other := &Library{Name: "其它书库", Path: t.TempDir(), Type: "mixed"}
	if err := s.AddLibrary(def); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLibrary(other); err != nil {
		t.Fatal(err)
	}

	// 未显式授权也能访问默认书库，其它书库仍需授权。
	got, err := s.GetUser(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !s.CanAccess(got, def.ID) || s.CanAccess(got, other.ID) {
		t.Fatalf("default access = %v, libraries = %v", s.CanAccess(got, def.ID), got.Libraries)
	}
	list := s.ListUsers()
	if len(list) != 1 || !s.CanAccess(list[0], def.ID) {
		t.Fatalf("list users default access = %+v", list)
	}

	// 管理员清空授权后，默认书库仍对用户开放。
	if err := s.UpdateUser(u.ID, func(user *User) error {
		user.Libraries = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUser(u.ID)
	if !s.CanAccess(got, def.ID) {
		t.Fatalf("default lost after clearing grants: %v", got.Libraries)
	}
}

// 新建库使用英文名 Default Library，同样对所有登录用户开放。
func TestDefaultLibraryEnglishName(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	u := &User{Username: "reader"}
	if err := s.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	def := &Library{Name: "Default Library", Path: t.TempDir(), Type: "mixed"}
	other := &Library{Name: "Other", Path: t.TempDir(), Type: "mixed"}
	if err := s.AddLibrary(def); err != nil {
		t.Fatal(err)
	}
	if err := s.AddLibrary(other); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUser(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !s.CanAccess(got, def.ID) || s.CanAccess(got, other.ID) {
		t.Fatalf("default access = %v, libraries = %v", s.CanAccess(got, def.ID), got.Libraries)
	}
}
