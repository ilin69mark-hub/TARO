// Package spreads — реестр раскладов (см. docs/project-book/02-functional/04).
// GET /v1/spreads отдает только is_active=true, сортировка по sort_order.
// Кэш Redis spreads:list:v1, TTL 5 мин; инвалидация — POST /v1/admin/config/publish.
package spreads

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/apierr"
)

// CacheKey и TTL — единый источник (см. 04-architecture/05-cache-redis.md).
const (
	CacheKey = "spreads:list:v1"
	CacheTTL = 5 * time.Minute
)

// Spread — один расклад из реестра.
type Spread struct {
	Code       string          `json:"code"`
	Name       string          `json:"name"`
	Positions  json.RawMessage `json:"positions"`
	IsPremium  bool            `json:"is_premium"`
	SortOrder  int             `json:"sort_order"`
	ConfigJSON json.RawMessage `json:"config_json"`
}

// Service читает реестр: сначала Redis, при промахе — PG.
type Service struct {
	pg *pgxpool.Pool
	rd *redis.Client
}

// New возвращает сервис реестра.
func New(pg *pgxpool.Pool, rd *redis.Client) *Service {
	return &Service{pg: pg, rd: rd}
}

// List возвращает активные расклады (кэш 5 мин).
func (s *Service) List(ctx context.Context) ([]Spread, error) {
	if cached, err := s.rd.Get(ctx, CacheKey).Bytes(); err == nil {
		var out []Spread
		if json.Unmarshal(cached, &out) == nil {
			return out, nil
		}
	}
	rows, err := s.pg.Query(ctx,
		`SELECT code, name_ru, positions, is_premium, sort_order, config_json
		   FROM spreads WHERE is_active ORDER BY sort_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Spread{}
	for rows.Next() {
		var sp Spread
		if err := rows.Scan(&sp.Code, &sp.Name, &sp.Positions, &sp.IsPremium, &sp.SortOrder, &sp.ConfigJSON); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if raw, err := json.Marshal(out); err == nil {
		_ = s.rd.Set(ctx, CacheKey, raw, CacheTTL).Err()
	}
	return out, nil
}

// HandleCard — GET /v1/cards/{id}: публичное значение карты для SEO (см. U16).
// Значения карт не секретны (см. 04-architecture/03: cards только SELECT).
func (s *Service) HandleCard(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(chi.URLParam(r, "id"))
	if err != nil || n < 0 || n > 77 {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Карта не найдена")
		return
	}
	var c struct {
		ID       int    `json:"id"`
		Name     string `json:"name_ru"`
		Upright  string `json:"upright_ru"`
		Reversed string `json:"reversed_ru"`
		ImageKey string `json:"image_key"`
	}
	if err := s.pg.QueryRow(r.Context(),
		`SELECT id, name_ru, upright_ru, reversed_ru, image_key FROM cards WHERE id=$1`, n).
		Scan(&c.ID, &c.Name, &c.Upright, &c.Reversed, &c.ImageKey); err != nil {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Карта не найдена")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}

// HandleList — GET /v1/spreads.
func (s *Service) HandleList(w http.ResponseWriter, r *http.Request) {
	out, err := s.List(r.Context())
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось загрузить расклады")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
