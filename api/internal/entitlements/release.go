package entitlements

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ReleaseReadingAuthorization возвращает entitlement, списанный под чтение,
// которое терминально не удалось сгенерировать (A12/F-12).
//
// Зачем: AuthorizeReading списывает дневной/love/single слот ДО вызова
// провайдера. При сталиве провайдера чтение уходит в failed, но слот оставался
// потраченным — пользователь платил (или терял бесплатный слот) впустую и не
// получал чтения.
//
// Идемпотентно: отменённая квитанция повторно не отменяется (released_at IS
// NULL в WHERE). Возвращается (bool, error): true — entitlement реально
// освобождён, false — отменять было нечего (уже отменён, подписка вместо
// слота, либо квитанции нет).
func (s *Service) ReleaseReadingAuthorization(ctx context.Context, readingID string) (bool, error) {
	if s == nil || s.pg == nil || readingID == "" {
		return false, errReadingAuthorizationInvalid
	}
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var kind, entitlementID string
	var period *string
	err = tx.QueryRow(ctx, `
		SELECT kind, COALESCE(entitlement_id::text,''), period_start::text
		  FROM reading_authorization_receipts
		 WHERE reading_id=$1 AND released_at IS NULL
		 FOR UPDATE`, readingID).Scan(&kind, &entitlementID, &period)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	switch kind {
	case "daily":
		if period != nil && *period != "" {
			if _, err := tx.Exec(ctx, `
				UPDATE entitlements SET free_used_today = GREATEST(free_used_today - 1, 0)
				 WHERE user_id=(SELECT user_id FROM readings WHERE id=$1) AND free_date=$2::date`,
				readingID, *period); err != nil {
				return false, err
			}
		}
	case "love_weekly":
		if period != nil && *period != "" {
			if _, err := tx.Exec(ctx, `
				UPDATE entitlements SET love_used_week = GREATEST(love_used_week - 1, 0)
				 WHERE user_id=(SELECT user_id FROM readings WHERE id=$1) AND love_week=$2::date`,
				readingID, *period); err != nil {
				return false, err
			}
		}
	case "single":
		if entitlementID == "" {
			return false, nil
		}
		// Снимаем только если квитанция действительно была владельцем этого
		// single_entitlement (защита от отмены чужой выдачи).
		tag, err := tx.Exec(ctx, `
			UPDATE single_entitlements SET consumed_reading_id=NULL
			 WHERE id=$1 AND consumed_reading_id=$2`, entitlementID, readingID)
		if err != nil {
			return false, err
		}
		if tag.RowsAffected() != 1 {
			return false, nil
		}
	case "subscription", "legacy":
		// Подписка не списывается — освобождать нечего.
		return false, nil
	default:
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE reading_authorization_receipts SET released_at=now()
		 WHERE reading_id=$1 AND released_at IS NULL`, readingID); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
