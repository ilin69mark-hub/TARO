// Package diary — дневник рефлексии (см. V05–V12, Could мес.4–6).
// Только свои записи (user_id из JWT); чужие id → 404 (не 403 — не палим существование).
// Тело 1–10000 символов (CHECK в БД + валидация). DeleteMe чистит каскадом (см. V10).
package diary

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
)

// Entry — запись дневника.
type Entry struct {
	ID        string  `json:"id"`
	ReadingID *string `json:"reading_id"`
	Body      string  `json:"body"`
	Mood      *string `json:"mood"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// Service — дневник.
type Service struct {
	pg *pgxpool.Pool
}

// New возвращает сервис.
func New(pg *pgxpool.Pool) *Service { return &Service{pg: pg} }

var moods = map[string]bool{"up": true, "down": true, "calm": true, "anxious": true, "grateful": true}

type entryRequest struct {
	ReadingID *string `json:"reading_id"`
	Body      string  `json:"body"`
	Mood      *string `json:"mood"`
}

func (s *Service) validate(uid string, req entryRequest) string {
	if strings.ContainsRune(req.Body, 0) {
		return "Некорректный текст" // S04: NUL роняет PG
	}
	if len([]rune(req.Body)) < 1 || len([]rune(req.Body)) > 10000 {
		return "Текст от 1 до 10000 символов"
	}
	if req.Mood != nil && !moods[*req.Mood] {
		return "Неизвестное настроение"
	}
	return ""
}

// HandleCreate — POST /v1/diary.
func (s *Service) HandleCreate(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req entryRequest
	if !apierr.Decode(w, r, &req) {
		return
	}
	if msg := s.validate(uid, req); msg != "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, msg)
		return
	}
	// reading_id обязан принадлежать юзеру (иначе NULL-игнор? Нет — 422, честно)
	if req.ReadingID != nil {
		var owner string
		if err := s.pg.QueryRow(r.Context(),
			`SELECT user_id FROM readings WHERE id=$1`, *req.ReadingID).Scan(&owner); err != nil || owner != uid {
			apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Чужой расклад")
			return
		}
	}
	var e Entry
	var ts time.Time
	var ts2 time.Time
	err := s.pg.QueryRow(r.Context(), `
		INSERT INTO diary_entries (user_id, reading_id, body, mood) VALUES ($1,$2,$3,$4)
		RETURNING id, reading_id, body, mood, created_at, updated_at`,
		uid, req.ReadingID, req.Body, req.Mood).Scan(&e.ID, &e.ReadingID, &e.Body, &e.Mood, &ts, &ts2)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось сохранить")
		return
	}
	e.CreatedAt = ts.Format(time.RFC3339)
	e.UpdatedAt = ts2.Format(time.RFC3339)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(e)
}

// HandleList — GET /v1/diary?limit&offset&mood.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	mood := r.URL.Query().Get("mood")
	rows, err := s.pg.Query(r.Context(), `
		SELECT id, reading_id, body, mood, created_at, updated_at FROM diary_entries
		 WHERE user_id=$1 AND ($3='' OR mood=$3)
		 ORDER BY created_at DESC LIMIT $2 OFFSET $4`, uid, limit, mood, offset)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось загрузить")
		return
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var c, u time.Time
		if err := rows.Scan(&e.ID, &e.ReadingID, &e.Body, &e.Mood, &c, &u); err != nil {
			continue
		}
		e.CreatedAt = c.Format(time.RFC3339)
		e.UpdatedAt = u.Format(time.RFC3339)
		out = append(out, e)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// owned грузит свою запись или false.
func (s *Service) owned(ctx context.Context, uid, id string) (Entry, bool) {
	var e Entry
	var c, u time.Time
	err := s.pg.QueryRow(ctx, `
		SELECT id, reading_id, body, mood, created_at, updated_at FROM diary_entries
		 WHERE id=$1 AND user_id=$2`, id, uid).Scan(&e.ID, &e.ReadingID, &e.Body, &e.Mood, &c, &u)
	if err != nil {
		return Entry{}, false
	}
	e.CreatedAt = c.Format(time.RFC3339)
	e.UpdatedAt = u.Format(time.RFC3339)
	return e, true
}

// HandleGet — GET /v1/diary/{id} (чужие → 404).
func (s *Service) HandleGet(w http.ResponseWriter, r *http.Request) {
	e, ok := s.owned(r.Context(), auth.UserID(r.Context()), chi.URLParam(r, "id"))
	if !ok {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Запись не найдена")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(e)
}

// HandleUpdate — PUT /v1/diary/{id} (только своя).
func (s *Service) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	id := chi.URLParam(r, "id")
	if _, ok := s.owned(r.Context(), uid, id); !ok {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Запись не найдена")
		return
	}
	var req entryRequest
	if !apierr.Decode(w, r, &req) {
		return
	}
	if msg := s.validate(uid, req); msg != "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, msg)
		return
	}
	var e Entry
	var c, u time.Time
	err := s.pg.QueryRow(r.Context(), `
		UPDATE diary_entries SET body=$1, mood=$2, updated_at=now()
		 WHERE id=$3 AND user_id=$4
		RETURNING id, reading_id, body, mood, created_at, updated_at`,
		req.Body, req.Mood, id, uid).Scan(&e.ID, &e.ReadingID, &e.Body, &e.Mood, &c, &u)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось обновить")
		return
	}
	e.CreatedAt = c.Format(time.RFC3339)
	e.UpdatedAt = u.Format(time.RFC3339)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(e)
}

// HandleExport — GET /v1/diary/export: все свои записи одним JSON (см. V12).
func (s *Service) HandleExport(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	rows, err := s.pg.Query(r.Context(), `
		SELECT id, reading_id, body, mood, created_at, updated_at FROM diary_entries
		 WHERE user_id=$1 ORDER BY created_at`, uid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось выгрузить")
		return
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var c, u time.Time
		if err := rows.Scan(&e.ID, &e.ReadingID, &e.Body, &e.Mood, &c, &u); err != nil {
			continue
		}
		e.CreatedAt = c.Format(time.RFC3339)
		e.UpdatedAt = u.Format(time.RFC3339)
		out = append(out, e)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="taro-diary.json"`)
	_ = json.NewEncoder(w).Encode(out)
}

// HandleDelete — DELETE /v1/diary/{id} (только своя).
func (s *Service) HandleDelete(w http.ResponseWriter, r *http.Request) {
	res, err := s.pg.Exec(r.Context(),
		`DELETE FROM diary_entries WHERE id=$1 AND user_id=$2`, chi.URLParam(r, "id"), auth.UserID(r.Context()))
	if err != nil || res.RowsAffected() == 0 {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Запись не найдена")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}
