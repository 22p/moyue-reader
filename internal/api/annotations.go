package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"moyue/internal/store"
)

var annotationColors = map[string]bool{
	"yellow": true,
	"green":  true,
	"blue":   true,
	"pink":   true,
}

func validAnnotationColor(c string) bool {
	return annotationColors[strings.ToLower(c)]
}

func (s *Server) addAnnotation(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	switch b.Type {
	case "txt", "epub", "fb2", "mobi", "azw", "azw3", "prc":
	default:
		writeError(w, http.StatusBadRequest, "this format does not support highlights and notes")
		return
	}
	var req struct {
		Chapter int    `json:"chapter"`
		Start   int    `json:"start"`
		End     int    `json:"end"`
		Text    string `json:"text"`
		Note    string `json:"note"`
		Color   string `json:"color"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Color = strings.ToLower(strings.TrimSpace(req.Color))
	if req.Color == "" {
		req.Color = "yellow"
	}
	if req.Chapter < 0 || (b.ChapterCount > 0 && req.Chapter >= b.ChapterCount) {
		writeError(w, http.StatusBadRequest, "chapter not found")
		return
	}
	if req.Start < 0 || req.End <= req.Start || req.End-req.Start > 10000 {
		writeError(w, http.StatusBadRequest, "invalid highlight range")
		return
	}
	if strings.TrimSpace(req.Text) == "" || utf8.RuneCountInString(req.Text) > 2000 {
		writeError(w, http.StatusBadRequest, "invalid highlighted text")
		return
	}
	if utf8.RuneCountInString(req.Note) > 5000 {
		writeError(w, http.StatusBadRequest, "note is too long")
		return
	}
	if !validAnnotationColor(req.Color) {
		writeError(w, http.StatusBadRequest, "invalid color")
		return
	}
	a, err := s.store.CreateAnnotation(u.ID, b.ID, store.Annotation{
		Chapter: req.Chapter,
		Start:   req.Start,
		End:     req.End,
		Text:    req.Text,
		Note:    strings.TrimSpace(req.Note),
		Color:   req.Color,
	})
	if err != nil {
		writeError(w, errStatus(err), "failed to save highlight")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"annotation": a})
}

func (s *Server) updateAnnotation(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	var req struct {
		Note  *string `json:"note"`
		Color *string `json:"color"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Color != nil {
		c := strings.ToLower(strings.TrimSpace(*req.Color))
		if !validAnnotationColor(c) {
			writeError(w, http.StatusBadRequest, "invalid color")
			return
		}
		req.Color = &c
	}
	if req.Note != nil && utf8.RuneCountInString(*req.Note) > 5000 {
		writeError(w, http.StatusBadRequest, "note is too long")
		return
	}
	a, err := s.store.UpdateAnnotation(u.ID, b.ID, r.PathValue("annotationID"), req.Note, req.Color)
	if err != nil {
		writeError(w, errStatus(err), "failed to update note")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"annotation": a})
}

func (s *Server) deleteAnnotation(w http.ResponseWriter, r *http.Request) {
	b, u, ok := s.loadBook(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteAnnotation(u.ID, b.ID, r.PathValue("annotationID")); err != nil {
		writeError(w, errStatus(err), "failed to delete note")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
