package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// 本文件覆盖数据层的三项正确性：迁移链幂等（含 user_version 回退再升级）、
// 账号隔离、级联删除。

func openStore(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func userVersion(t *testing.T, st *Store) int {
	t.Helper()
	var v int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	return v
}

func setUserVersion(t *testing.T, st *Store, v int) {
	t.Helper()
	if _, err := st.db.Exec(`PRAGMA user_version = ` + itoa(v)); err != nil {
		t.Fatalf("设置 user_version: %v", err)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

/* --------------------- 迁移链幂等 --------------------- */

func TestMigrationIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")

	st := openStore(t, dir)
	if got := userVersion(t, st); got != schemaVersion {
		t.Fatalf("首次打开后 user_version = %d, 期望 %d", got, schemaVersion)
	}
	st.Close()

	// 重复打开：migrate 重跑不应报错，版本也不应变化
	for i := 0; i < 3; i++ {
		st = openStore(t, dir)
		if got := userVersion(t, st); got != schemaVersion {
			t.Fatalf("第 %d 次重开后 user_version = %d, 期望 %d", i+2, got, schemaVersion)
		}
		st.Close()
	}
}

func TestMigrationReplayAfterVersionRollback(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")

	// 先在 v4 上写入真实数据，回退版本后这些数据必须还在
	st := openStore(t, dir)
	lib := &Library{Name: "迁移库", Path: dir, Type: "mixed", Kind: "local"}
	if err := st.AddLibrary(lib); err != nil {
		t.Fatalf("AddLibrary: %v", err)
	}
	st.Close()

	// 逐级回退再升级：4 -> 1 -> 4，检查 migrateV2/V3/V4 重放是否幂等
	for _, target := range []int{3, 2, 1} {
		st = openStore(t, dir)
		setUserVersion(t, st, target)
		st.Close()

		st = openStore(t, dir)
		if got := userVersion(t, st); got != schemaVersion {
			t.Fatalf("从 %d 回退重开后 user_version = %d, 期望 %d", target, got, schemaVersion)
		}
		if libs := st.ListLibraries(); len(libs) != 1 || libs[0].ID != lib.ID {
			t.Fatalf("回退到 %d 再升级后书库丢失或变化: %+v", target, libs)
		}
		// V2 迁移会重建 sessions.id 的唯一索引，重复执行不能报错
		if _, err := st.db.Exec(`SELECT COUNT(*) FROM sessions`); err != nil {
			t.Fatalf("sessions 表不可用: %v", err)
		}
		if _, err := st.db.Exec(`SELECT COUNT(*) FROM annotations`); err != nil {
			t.Fatalf("annotations 表不可用: %v", err)
		}
		st.Close()
	}
}

func TestMigrationRejectsHigherVersion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st := openStore(t, dir)
	setUserVersion(t, st, schemaVersion+10)
	st.Close()

	if _, err := Open(dir); err == nil {
		t.Fatal("user_version 高于程序支持版本时 Open 应当报错")
	}
}

func TestForeignKeysEnabled(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "data"))
	defer st.Close()
	var fk int
	if err := st.db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, 期望 1（DSN 里的 _pragma=foreign_keys(1) 未生效则级联全部失效）", fk)
	}
}

/* --------------------- 账号隔离 --------------------- */

func TestAccountIsolationAcrossUsers(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "data"))
	defer st.Close()

	alice := &User{Username: "alice", PasswordHash: "x", IsAdmin: true}
	bob := &User{Username: "bob", PasswordHash: "x"}
	if err := st.CreateUser(alice); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(bob); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Name: "共享库", Path: t.TempDir(), Type: "mixed", Kind: "local"}
	if err := st.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	book := &Book{ID: "book1", LibraryID: lib.ID, Type: "epub", File: "a.epub", Title: "书"}
	if err := st.AddBook(book); err != nil {
		t.Fatal(err)
	}

	// 进度互不干扰
	if _, err := st.SaveProgress(alice.ID, book.ID, 2, 0.5); err != nil {
		t.Fatal(err)
	}
	if p := st.ProgressFor(bob.ID, book.ID); p.Chapter != 0 || p.Percent != 0 {
		t.Errorf("bob 看到了 alice 的进度: %+v", p)
	}

	// 批注互不干扰，且 bob 改/删 alice 的批注必须失败
	a, err := st.CreateAnnotation(alice.ID, book.ID, Annotation{
		Chapter: 0, Start: 0, End: 2, Text: "原文", Color: "yellow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.ListAnnotations(bob.ID, book.ID); len(got) != 0 {
		t.Errorf("bob 看到了 alice 的批注: %+v", got)
	}
	note := "篡改"
	if _, err := st.UpdateAnnotation(bob.ID, book.ID, a.ID, &note, nil); err == nil {
		t.Error("bob 竟然能修改 alice 的批注")
	}
	if err := st.DeleteAnnotation(bob.ID, book.ID, a.ID); err == nil {
		t.Error("bob 竟然能删除 alice 的批注")
	}
	if got := st.ListAnnotations(alice.ID, book.ID); len(got) != 1 {
		t.Errorf("alice 的批注被跨用户操作影响了: %+v", got)
	}
}

func TestLibraryAccessGrant(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "data"))
	defer st.Close()

	lib := &Library{Name: "受限库", Path: t.TempDir(), Type: "mixed", Kind: "local"}
	if err := st.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	u := &User{Username: "reader", PasswordHash: "x", Libraries: []string{}}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	if st.CanAccess(u, lib.ID) {
		t.Error("未授权用户竟然可以访问书库")
	}
	// 管理员不受书库授权限制
	admin := &User{Username: "root", PasswordHash: "x", IsAdmin: true, Libraries: []string{}}
	if err := st.CreateUser(admin); err != nil {
		t.Fatal(err)
	}
	if !st.CanAccess(admin, lib.ID) {
		t.Error("管理员应当可以访问全部书库")
	}
}

/* --------------------- 级联删除 --------------------- */

func TestCascadeDelete(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "data"))
	defer st.Close()

	alice := &User{Username: "alice", PasswordHash: "x", IsAdmin: true}
	if err := st.CreateUser(alice); err != nil {
		t.Fatal(err)
	}
	lib := &Library{Name: "库", Path: t.TempDir(), Type: "mixed", Kind: "local"}
	if err := st.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	book := &Book{ID: "book1", LibraryID: lib.ID, Type: "epub", File: "a.epub", Title: "书"}
	other := &Book{ID: "book2", LibraryID: lib.ID, Type: "epub", File: "b.epub", Title: "另一本"}
	for _, b := range []*Book{book, other} {
		if err := st.AddBook(b); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.SaveProgress(alice.ID, book.ID, 1, 0.5); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddBookmark(alice.ID, book.ID, Bookmark{Chapter: 1, Percent: 0.5}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateAnnotation(alice.ID, book.ID, Annotation{
		Chapter: 1, Start: 0, End: 3, Text: "划线的字", Color: "yellow",
	}); err != nil {
		t.Fatal(err)
	}

	// 删除单本书：只清掉这本书的阅读数据
	if err := st.DeleteBook(book.ID); err != nil {
		t.Fatal(err)
	}
	if got := st.BookCount(lib.ID); got != 1 {
		t.Errorf("删掉一本书后剩余 %d 本, 期望 1", got)
	}
	if got := st.ListAnnotations(alice.ID, book.ID); len(got) != 0 {
		t.Errorf("删书后批注残留: %+v", got)
	}

	// 删除书库：书与阅读数据级联清空
	if err := st.DeleteLibrary(lib.ID); err != nil {
		t.Fatal(err)
	}
	if got := len(st.ListBooks()); got != 0 {
		t.Errorf("删书库后仍有 %d 本书（外键级联未生效）", got)
	}

	// 删除用户：会话与阅读数据级联清空
	if err := st.DeleteLibrary(lib.ID); err == nil {
		t.Error("删除不存在的书库应当返回 ErrNotFound")
	}
	admin2 := &User{Username: "root2", PasswordHash: "x", IsAdmin: true}
	if err := st.CreateUser(admin2); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetUser(alice.ID); err == nil {
		t.Error("用户删除后仍可查到")
	}
	if got := st.ListSessions(alice.ID); len(got) != 0 {
		t.Errorf("用户删除后会话残留: %+v", got)
	}
	// 最后一个管理员不可删
	users := st.ListUsers()
	if len(users) != 1 {
		t.Fatalf("剩余用户 %d, 期望 1", len(users))
	}
	if err := st.DeleteUser(admin2.ID); err != ErrLastAdmin {
		t.Errorf("删除最后一个管理员 = %v, 期望 ErrLastAdmin", err)
	}
}

// 确保 sql 包被使用（ErrNoRows 断言用得到）
var _ = sql.ErrNoRows
