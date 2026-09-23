// Merge Link anon→TG (см. docs/project-book/02-functional/02-auth.md, T09).
// Правила: survivor = TG-аккаунт; valid_until = max (источник — subscriptions);
// счетчики free/love = max; trial 1 раз на tg+fingerprint; дубль users удаляется;
// второй JWT инвалидируется (DEL sess:loser).
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5"

	"taro/api/internal/apierr"
)

// userCtxKey — ключ user_id в контексте (ставит RequireAuth).
type userCtxKey struct{}

// RequireAuth — middleware: JWT из cookie taro_jwt + живой sess:<uid> → user_id в контекст.
// DEL sess (logout, merge проигравшего) инвалидирует токен мгновенно. Иначе 401.
// S03: ошибка Redis → 503 (не 401 — сессия может быть жива, Redis просто лежит).
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
			return
		}
		uid, err := ParseJWT(c.Value)
		if err != nil {
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия истекла, войди снова")
			return
		}
		n, err := s.rd.Exists(r.Context(), sessKey(uid)).Result()
		if err != nil {
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
			return
		}
		if n == 0 {
			apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Сессия завершена, войди снова")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey{}, uid)))
	})
}

// UserID достает user_id из контекста.
func UserID(ctx context.Context) string {
	uid, _ := ctx.Value(userCtxKey{}).(string)
	return uid
}

// linkRequest — POST /v1/auth/link {initData}.
func (s *Service) HandleLink(w http.ResponseWriter, r *http.Request) {
	current := UserID(r.Context())
	if current == "" {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthorized, "Нужен вход")
		return
	}
	var req struct {
		InitData    string `json:"initData"`
		Fingerprint string `json:"fingerprint"`
	}
	if !apierr.Decode(w, r, &req) || req.InitData == "" {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Некорректный initData")
		return
	}
	tgID, err := VerifyInitData(req.InitData, os.Getenv("TG_BOT_TOKEN"))
	if err != nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeInvalidTg, "Не удалось подтвердить Telegram")
		return
	}
	survivor, merged, err := s.Link(r.Context(), current, tgID, req.Fingerprint)
	if err != nil {
		if err.Error() == "already_linked" {
			apierr.Write(w, http.StatusConflict, "ALREADY_LINKED", "Telegram уже привязан")
			return
		}
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось привязать Telegram")
		return
	}
	tok, err := IssueJWT(survivor, UserTTL)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось создать сессию")
		return
	}
	if err := s.rememberSession(r.Context(), survivor, UserTTL); err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeUnavailable, "Сервис занят, попробуй позже")
		return
	}
	writeCookie(w, tok, UserTTL)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"merged": merged, "user_id": survivor})
}

// Link привязывает tg_id к current в одной транзакции. Возвращает survivor.
func (s *Service) Link(ctx context.Context, current string, tgID int64, fingerprint string) (string, bool, error) {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var curTg *int64
	if err := tx.QueryRow(ctx, `SELECT tg_id FROM users WHERE id=$1`, current).Scan(&curTg); err != nil {
		return "", false, err
	}
	if curTg != nil {
		if *curTg == tgID {
			return current, true, nil // уже привязан к тому же TG — идемпотентно
		}
		return "", false, errAlreadyLinked()
	}
	var other string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE tg_id=$1`, tgID).Scan(&other)
	if err == pgx.ErrNoRows {
		// TG свободен: аттачим к current + trial
		if _, err := tx.Exec(ctx, `UPDATE users SET tg_id=$1 WHERE id=$2`, tgID, current); err != nil {
			return "", false, err
		}
		if err := grantTrialTx(ctx, tx, current); err != nil {
			return "", false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", false, err
		}
		return current, true, nil
	}
	if err != nil {
		return "", false, err
	}
	// TG занят other: мержим current → other (все FK перелинковываются)
	for _, q := range []string{
		`UPDATE readings SET user_id=$1 WHERE user_id=$2`,
		`UPDATE subscriptions SET user_id=$1 WHERE user_id=$2`,
		`UPDATE payments SET user_id=$1 WHERE user_id=$2`,
		`UPDATE single_entitlements SET user_id=$1 WHERE user_id=$2`,
		`UPDATE referrals SET referrer_id=$1 WHERE referrer_id=$2`,
		`UPDATE referrals SET referee_id=$1 WHERE referee_id=$2`,
	} {
		if _, err := tx.Exec(ctx, q, other, current); err != nil {
			return "", false, err
		}
	}
	// счетчики entitlements — max(a, b) по каждой паре (читаем в Go, пишем upsert)
	if err := mergeCountersTx(ctx, tx, other, current); err != nil {
		return "", false, err
	}
	if err := grantTrialTx(ctx, tx, other); err != nil {
		return "", false, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id=$1`, current); err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, err
	}
	_ = s.rd.Del(ctx, "sess:"+current).Err() // инвалидация второго JWT
	return other, true, nil
}

// mergeCountersTx сливает счетчики free/love как max(survivor, loser).
func mergeCountersTx(ctx context.Context, tx pgx.Tx, survivor, loser string) error {
	type counters struct {
		freeUsed int
		freeDate *string
		loveUsed int
		loveWeek *string
	}
	read := func(uid string) (counters, error) {
		var c counters
		err := tx.QueryRow(ctx,
			`SELECT free_used_today, free_date::text, love_used_week, love_week::text
			   FROM entitlements WHERE user_id=$1`, uid).Scan(&c.freeUsed, &c.freeDate, &c.loveUsed, &c.loveWeek)
		if err == pgx.ErrNoRows {
			return counters{}, nil
		}
		return c, err
	}
	a, err := read(survivor)
	if err != nil {
		return err
	}
	b, err := read(loser)
	if err != nil {
		return err
	}
	freeUsed := max(a.freeUsed, b.freeUsed)
	loveUsed := max(a.loveUsed, b.loveUsed)
	freeDate := a.freeDate
	if b.freeDate != nil && (freeDate == nil || *b.freeDate > *freeDate) {
		freeDate = b.freeDate
	}
	loveWeek := a.loveWeek
	if b.loveWeek != nil && (loveWeek == nil || *b.loveWeek > *loveWeek) {
		loveWeek = b.loveWeek
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO entitlements (user_id, free_used_today, free_date, love_used_week, love_week)
		VALUES ($1,$2,$3::date,$4,$5::date)
		ON CONFLICT (user_id) DO UPDATE SET
		  free_used_today = EXCLUDED.free_used_today,
		  free_date = EXCLUDED.free_date,
		  love_used_week = EXCLUDED.love_used_week,
		  love_week = EXCLUDED.love_week`,
		survivor, freeUsed, freeDate, loveUsed, loveWeek)
	return err
}

// grantTrialTx — trial внутри транзакции (проверка subscriptions trial_3d).
func grantTrialTx(ctx context.Context, tx pgx.Tx, userID string) error {
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE user_id=$1 AND plan_code='trial_3d'`, userID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var planID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM plans WHERE code='trial_3d' AND is_active ORDER BY valid_from DESC LIMIT 1`).Scan(&planID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		 VALUES ($1,$2,'trial_3d',0, now() + interval '3 days')`, userID, planID)
	return err
}

type linkedError struct{}

func (linkedError) Error() string { return "already_linked" }

func errAlreadyLinked() error { return linkedError{} }
