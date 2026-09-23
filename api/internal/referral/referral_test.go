// Unit-тесты referral (см. D2).
package referral

import (
	"regexp"
	"testing"
)

func TestGenCodeFormat(t *testing.T) {
	seen := map[string]bool{}
	ok, _ := regexp.Compile(`^[A-Z2-9]{8}$`)
	for i := 0; i < 200; i++ {
		c := genCode()
		if !ok.MatchString(c) {
			t.Fatalf("bad code: %s", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code: %s", c)
		}
		seen[c] = true
	}
}
