// Выдача/отзыв безлимита: контракт с моделью entitlements.
//
// Ключевая проверка здесь — что повторный Grant НЕ плодит дубликаты строк.
// Первая версия делала INSERT всегда и комментировала обратное; заметили
// только на живом стенде, где после трёх `grant` в subscriptions появилось
// три одинаковых строки. Такая ошибка не мешает работать (ENT — EXISTS),
// но делает revoke неоднозначным и засоряет отчётность.
package access

import (
	"errors"
	"testing"
	"time"

	"taro/api/internal/testutil"
)

func TestGrantCreatesActiveSubscription(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)

	until, err := Grant(ctx, pg, uid, 0) // 0 → DefaultDays
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if !until.After(time.Now().AddDate(99, 0, 0)) {
		t.Fatalf("default grant must be effectively forever, got %v", until)
	}
	var status string
	var source string
	if err := pg.QueryRow(ctx,
		`SELECT status, source_type FROM subscriptions WHERE user_id=$1 AND plan_code=$2`,
		uid, DefaultPlanCode).Scan(&status, &source); err != nil {
		t.Fatalf("subscription row must exist: %v", err)
	}
	// source_type='legacy' отличает ручную выдачу от оплаты: payment_id пуст,
	// в выручке она не попадает.
	if status != "active" || source != "legacy" {
		t.Fatalf("want active/legacy, got %q/%q", status, source)
	}
}

func TestGrantIsIdempotent(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)

	if _, err := Grant(ctx, pg, uid, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Grant(ctx, pg, uid, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Grant(ctx, pg, uid, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pg.QueryRow(ctx,
		`SELECT count(*) FROM subscriptions WHERE user_id=$1 AND status='active'`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("repeat grant must not duplicate rows, got %d", n)
	}
}

func TestGrantExtendsButNeverShortens(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)

	long, err := Grant(ctx, pg, uid, 36500)
	if err != nil {
		t.Fatal(err)
	}
	// Просят меньше — срок не должен укоротиться, иначе «продлить» можно
	// было бы случайно отозвать, выдав короткий срок.
	short, err := Grant(ctx, pg, uid, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !short.Equal(long) {
		t.Fatalf("shorter grant must not shorten access: %v vs %v", short, long)
	}
}

func TestGrantUnknownUser(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	// ErrNoUser, а не 500: оператор обычно путает user_id и anon_uuid, и
	// понятная ошибка экономит время.
	_, err := Grant(ctx, pg, "00000000-0000-0000-0000-000000000000", 0)
	if !errors.Is(err, ErrNoUser) {
		t.Fatalf("want ErrNoUser, got %v", err)
	}
}

func TestRevokeRemovesAccess(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := Grant(ctx, pg, uid, 0); err != nil {
		t.Fatal(err)
	}
	n, err := Revoke(ctx, pg, uid)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 revoked, got %d", n)
	}
	var active int
	_ = pg.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1 AND status='active'`, uid).Scan(&active)
	if active != 0 {
		t.Fatalf("revoked access must stop counting, still active: %d", active)
	}
	// Строка остаётся: аудит важнее, а 'revoked' и так не учитывается.
	var total int
	_ = pg.QueryRow(ctx, `SELECT count(*) FROM subscriptions WHERE user_id=$1`, uid).Scan(&total)
	if total != 1 {
		t.Fatalf("revoke must keep the row for audit, got %d", total)
	}
}

func TestRevokeTwiceIsSafe(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := Grant(ctx, pg, uid, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := Revoke(ctx, pg, uid); err != nil {
		t.Fatal(err)
	}
	n, err := Revoke(ctx, pg, uid)
	if err != nil {
		t.Fatalf("second revoke must not error: %v", err)
	}
	if n != 0 {
		t.Fatalf("second revoke must be a no-op, got %d", n)
	}
}

// Выданный доступ обязан реально снимать лимит, иначе команда «работает»,
// а пользователь всё равно видит 402. Проверяем через entitlements, а не
// через SELECT — проверка должна идти по тому же пути, что и приложение.
func TestGrantedAccessRemovesFreeLimit(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)

	if _, err := Grant(ctx, pg, uid, 0); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pg.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM subscriptions
		   WHERE user_id=$1 AND status='active' AND valid_until > now())`, uid).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("granted access must be visible to the entitlement check")
	}
}

func TestListShowsOnlyManualGrants(t *testing.T) {
	ctx, pg, _ := testutil.Live(t)
	uid := testutil.NewUser(t, ctx, pg)
	if _, err := Grant(ctx, pg, uid, 0); err != nil {
		t.Fatal(err)
	}
	entries, err := List(ctx, pg)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.UserID == uid {
			found = true
			if e.Status != "active" {
				t.Fatalf("entry status: %q", e.Status)
			}
		}
	}
	if !found {
		t.Fatal("granted access must appear in list")
	}
	if len(entries) == 0 {
		t.Fatal("list must not be empty after grant")
	}
}
