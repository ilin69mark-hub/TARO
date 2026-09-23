// Unit-тесты чистых функций readings (см. D2).
package readings

import (
	"testing"
	"time"
)

func TestDrawDeterministic(t *testing.T) {
	a := draw(42, 3)
	b := draw(42, 3)
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("same seed differs")
		}
	}
}

func TestDrawNoDuplicates(t *testing.T) {
	seen := map[int]bool{}
	for _, c := range draw(7, 10) {
		if c.CardID < 0 || c.CardID > 77 {
			t.Fatalf("card out of range: %d", c.CardID)
		}
		if seen[c.CardID] {
			t.Fatalf("duplicate card: %d", c.CardID)
		}
		seen[c.CardID] = true
	}
}

func TestDrawReversedRate(t *testing.T) {
	rev, total := 0, 0
	for s := int64(0); s < 200; s++ {
		for _, c := range draw(s, 5) {
			total++
			if c.Reversed {
				rev++
			}
		}
	}
	rate := float64(rev) / float64(total)
	if rate < 0.10 || rate > 0.20 {
		t.Fatalf("reversed rate %.3f outside [0.10, 0.20]", rate)
	}
}

func TestIsCrisis(t *testing.T) {
	if !isCrisis("хочу покончить с собой") {
		t.Fatal("missed crisis")
	}
	if isCrisis("Что важно сегодня?") {
		t.Fatal("false positive")
	}
}

func TestSameMSKDay(t *testing.T) {
	msk, _ := time.LoadLocation("Europe/Moscow")
	a := time.Date(2026, 9, 23, 23, 50, 0, 0, msk)
	b := time.Date(2026, 9, 24, 0, 5, 0, 0, msk) // 15 мин позже, уже другой день
	if sameMSKDay(a, b) {
		t.Fatal("different MSK days equal")
	}
	if !sameMSKDay(a, a) {
		t.Fatal("same day differs")
	}
}
