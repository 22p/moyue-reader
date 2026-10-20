package store

import (
	"database/sql"
	"errors"
	"time"
)

// Annotation 是用户在书中的一处划线或笔记。位置使用章节内正文字符偏移，
// 不修改原始书籍文件。
type Annotation struct {
	ID        string    `json:"id"`
	Chapter   int       `json:"chapter"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	Text      string    `json:"text"`
	Note      string    `json:"note"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

const annotationColumns = `id, chapter, start_offset, end_offset, text, note, color, created_at, updated_at`

// ListAnnotations 返回用户在某本书上的全部划线笔记。
func (s *Store) ListAnnotations(userID, bookID string) []Annotation {
	rows, err := s.db.Query(`SELECT `+annotationColumns+` FROM annotations
WHERE user_id = ? AND book_id = ? ORDER BY chapter, start_offset`, userID, bookID)
	if err != nil {
		return []Annotation{}
	}
	defer rows.Close()
	out := make([]Annotation, 0, 16)
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return out
		}
		out = append(out, a)
	}
	return out
}

// CreateAnnotation 新增划线或笔记。
func (s *Store) CreateAnnotation(userID, bookID string, a Annotation) (Annotation, error) {
	if err := s.requireUserBook(userID, bookID); err != nil {
		return Annotation{}, err
	}
	a.ID = randID()
	now := time.Now()
	a.CreatedAt = now
	a.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO annotations
(id, user_id, book_id, chapter, start_offset, end_offset, text, note, color, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, userID, bookID, a.Chapter, a.Start, a.End, a.Text, a.Note, a.Color,
		toMS(a.CreatedAt), toMS(a.UpdatedAt))
	if err != nil {
		return Annotation{}, err
	}
	return a, nil
}

// UpdateAnnotation 修改笔记内容或颜色。
func (s *Store) UpdateAnnotation(userID, bookID, id string, note, color *string) (Annotation, error) {
	row := s.db.QueryRow(`SELECT `+annotationColumns+` FROM annotations
WHERE id = ? AND user_id = ? AND book_id = ?`, id, userID, bookID)
	a, err := scanAnnotation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Annotation{}, ErrNotFound
	}
	if err != nil {
		return Annotation{}, err
	}
	if note != nil {
		a.Note = *note
	}
	if color != nil {
		a.Color = *color
	}
	a.UpdatedAt = time.Now()
	_, err = s.db.Exec(`UPDATE annotations SET note = ?, color = ?, updated_at = ?
WHERE id = ? AND user_id = ? AND book_id = ?`,
		a.Note, a.Color, toMS(a.UpdatedAt), id, userID, bookID)
	if err != nil {
		return Annotation{}, err
	}
	return a, nil
}

// DeleteAnnotation 删除一条划线笔记。
func (s *Store) DeleteAnnotation(userID, bookID, id string) error {
	res, err := s.db.Exec(`DELETE FROM annotations WHERE id = ? AND user_id = ? AND book_id = ?`,
		id, userID, bookID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) requireUserBook(userID, bookID string) error {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM users WHERE id = ?`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := s.db.QueryRow(`SELECT 1 FROM books WHERE id = ?`, bookID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func scanAnnotation(row scanner) (Annotation, error) {
	var a Annotation
	var created, updated int64
	if err := row.Scan(&a.ID, &a.Chapter, &a.Start, &a.End, &a.Text, &a.Note,
		&a.Color, &created, &updated); err != nil {
		return Annotation{}, err
	}
	a.CreatedAt = fromMS(created)
	a.UpdatedAt = fromMS(updated)
	return a, nil
}
