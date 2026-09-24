// Unit-тесты чистых функций payments/referral (см. D2).
package payments

import (
	"context"
	"crypto/sha256"
	"testing"
)

func ctx() context.Context { return context.Background() }

// bucketOf — копия бакета VariantFor для теста детерминизма (см. payments.go).
func bucketOf(userID string, split int) bool {
	sum := sha256.Sum256([]byte(userID))
	return int(sum[0])*100/256 < split
}

func TestBucketStable(t *testing.T) {
	if bucketOf("u1", 50) != bucketOf("u1", 50) {
		t.Fatal("bucket unstable")
	}
	// границы: split=0 всегда control, split=100 всегда test
	if bucketOf("any-user", 0) {
		t.Fatal("split=0 must be control")
	}
	if !bucketOf("any-user", 100) {
		t.Fatal("split=100 must be test")
	}
}

func TestPurchaseFingerprint(t *testing.T) {
	base := purchaseFingerprint("month_299", 299, 199)
	if len(base) != 64 || base != purchaseFingerprint("month_299", 299, 199) {
		t.Fatalf("unstable fingerprint: %q", base)
	}
	for _, other := range []string{
		purchaseFingerprint("year_2490", 299, 199),
		purchaseFingerprint("month_299", 300, 199),
		purchaseFingerprint("month_299", 299, 200),
	} {
		if base == other {
			t.Fatal("fingerprint must bind plan, price and stars")
		}
	}
}

func TestWebhookIdentifiers(t *testing.T) {
	if !validUUID("00000000-0000-0000-0000-000000000001") || validUUID("not-a-uuid") {
		t.Fatal("uuid validation")
	}
	if !validOpaqueID("ch-123_A") || validOpaqueID("") || validOpaqueID("ch 123") {
		t.Fatal("charge validation")
	}
}

func TestTelegramCharge(t *testing.T) {
	charge := "ch-123_A"
	p := storedPayment{ProviderPaymentID: "tg:" + charge}
	if got := telegramCharge(p); got != charge {
		t.Fatalf("charge=%q", got)
	}
	p.ProviderPaymentID = "test:" + charge
	if got := telegramCharge(p); got != "" {
		t.Fatalf("non-tg charge=%q", got)
	}
}

func TestItoa(t *testing.T) {
	for n, want := range map[int]string{0: "0", 7: "7", 42: "42", 100: "100"} {
		if itoa(n) != want {
			t.Fatalf("itoa(%d)=%s want %s", n, itoa(n), want)
		}
	}
}

func TestProviders(t *testing.T) {
	if (StarsProvider{}).Name() != "tg_stars" {
		t.Fatal("stars name")
	}
	if (YooKassaProvider{}).Name() != "yookassa" {
		t.Fatal("yk name")
	}
	// без токена — ошибка, не паника
	if _, err := (StarsProvider{}).CreateInvoice(ctx(), nil, "p", "month_299", 199, 299); err == nil {
		t.Fatal("want no-token error")
	}
	if _, err := (YooKassaProvider{}).CreateInvoice(ctx(), nil, "p", "month_299", 0, 299); err == nil {
		t.Fatal("want disabled error")
	}
	// splitCharge
	if got := splitCharge("tg:ch_1"); len(got) != 2 || got[1] != "ch_1" {
		t.Fatalf("split: %v", got)
	}
	if got := splitCharge("abc"); len(got) != 1 {
		t.Fatalf("split plain: %v", got)
	}
}
