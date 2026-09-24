// Публичный шеринг по opaque-токену (без PII в URL): POST создаёт, GET отдаёт превью.
// Толкование НЕ отдаётся никогда — только владелец видит interpretation (см. U12).
package readings

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
)

var shareTokenRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

const shareLifetime = 30 * 24 * time.Hour

func newShareToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// HandleCreateShare — POST /v1/share {reading_id} (auth+CSRF): стабильный токен расклада.
func (s *Service) HandleCreateShare(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req struct {
		ReadingID string `json:"reading_id"`
	}
	if !apierr.Decode(w, r, &req) || req.ReadingID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен reading_id")
		return
	}
	ctx := r.Context()
	// владелец? (проверка в SQL, чужие id — 404)
	var exists bool
	if err := s.pg.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM readings WHERE id=$1 AND user_id=$2)`,
		req.ReadingID, uid).Scan(&exists); err != nil || !exists {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Расклад не найден")
		return
	}
	token, err := newShareToken()
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать ссылку")
		return
	}
	expiresAt := time.Now().Add(shareLifetime)
	if _, err := s.pg.Exec(ctx, `
		INSERT INTO share_tokens (reading_id, token, expires_at) VALUES ($1,$2,$3)
		ON CONFLICT (reading_id) DO UPDATE
		   SET token=EXCLUDED.token, expires_at=EXCLUDED.expires_at, revoked_at=NULL
		 WHERE share_tokens.revoked_at IS NOT NULL OR share_tokens.expires_at <= now()`,
		req.ReadingID, token, expiresAt); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать ссылку")
		return
	}
	if err := s.pg.QueryRow(ctx,
		`SELECT token FROM share_tokens WHERE reading_id=$1`, req.ReadingID).Scan(&token); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать ссылку")
		return
	}
	if token == "" {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать ссылку")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func (s *Service) HandleRevokeShare(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req struct {
		ReadingID string `json:"reading_id"`
	}
	if !apierr.Decode(w, r, &req) || req.ReadingID == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен reading_id")
		return
	}
	res, err := s.pg.Exec(r.Context(), `
		UPDATE share_tokens SET revoked_at=now()
		 WHERE reading_id=$1
		   AND reading_id IN (SELECT id FROM readings WHERE user_id=$2)`, req.ReadingID, uid)
	if err != nil || res.RowsAffected() == 0 {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Ссылка не найдена")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// sharedCard — карта превью (без толкования).
type sharedCard struct {
	CardID   int    `json:"card_id"`
	NameRU   string `json:"name_ru,omitempty"`
	ImageKey string `json:"image_key,omitempty"`
	Reversed bool   `json:"reversed"`
	Position int    `json:"position"`
}

// HandleGetShare — GET /v1/share/{token} (публично, без auth): превью без толкования.
func (s *Service) HandleGetShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if !shareTokenRE.MatchString(token) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный токен")
		return
	}
	ctx := r.Context()
	var question, spreadCode string
	var cards json.RawMessage
	var created string
	err := s.pg.QueryRow(ctx, `
		SELECT r.question, r.spread_code, r.cards, r.created_at::text
		  FROM readings r JOIN share_tokens t ON t.reading_id = r.id
		 WHERE t.token=$1 AND t.revoked_at IS NULL AND t.expires_at > now()`, token).Scan(&question, &spreadCode, &cards, &created)
	if err != nil {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Ссылка не найдена")
		return
	}
	var spreadName string
	_ = s.pg.QueryRow(ctx, `SELECT name_ru FROM spreads WHERE code=$1`, spreadCode).Scan(&spreadName)
	out := s.enrichCards(ctx, cards)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"question": question, "spread": spreadName, "cards": out, "created_at": created,
	})
}
