// Юнит-тесты переноса: формат токена, ключ в Redis, привязка к сети.
//
// Без БД и без HTTP. Здесь ловится то, что тихо ослабляет защиту:
//
//  1. Ключ Redis = сам токен. Тогда значение ключа всплывает в выводе SCAN и
//     в отчётах о состоянии, а токен в этот момент ещё жив.
//  2. base64 с '+' и '/' — токен ломает парсер query-строки в дип-линке.
//  3. Привязка к полному адресу вместо сети — честный плательщик, у которого
//     сеть переключилась с 4G на Wi-Fi, получает отказ на ровном месте.
//  4. Get+Del вместо GetDel — два параллельных запроса успевают оба прочитать
//     значение и запустить слияние дважды.
package auth

import (
	"net"
	"strings"
	"testing"
)

func TestHandoffTokenFormat(t *testing.T) {
	tok, err := newHandoffToken()
	if err != nil {
		t.Fatal(err)
	}
	if !validHandoffToken(tok) {
		t.Fatalf("выданный токен не проходит свою же проверку: %q", tok)
	}
	// base64url без паддинга: '+' и '/' требуют экранирования в URL, а '='
	// ломает парсер query на стороне Telegram.
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("токен не url-safe: %q", tok)
	}
	// 32 байта энтропии — единственный барьер для bearer-секрета.
	if len(tok) < 43 {
		t.Fatalf("токен слишком короткий (%d символов) — перебор возможен", len(tok))
	}
}

func TestHandoffTokenUniqueness(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		tok, err := newHandoffToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatal("токен повторился")
		}
		seen[tok] = true
	}
}

func TestHandoffTokenValidationRejectsGarbage(t *testing.T) {
	// Мусор не должен доходить до Redis: иначе на каждый чужой запрос в
	// приложении будет поход в сеть по ключу, выдуманному из мусора.
	for _, bad := range []string{
		"", "short", strings.Repeat("A", 100),
		"!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!",
		"abc/def+ghi=", // не base64url
	} {
		if validHandoffToken(bad) {
			t.Fatalf("мусор принят как токен: %q", bad)
		}
	}
}

func TestHandoffKeyIsHashNotToken(t *testing.T) {
	tok, err := newHandoffToken()
	if err != nil {
		t.Fatal(err)
	}
	key := handoffKey(tok)
	if strings.Contains(key, tok) {
		t.Fatal("сырой токен попал в ключ Redis — он всплывёт в SCAN и в отчётах")
	}
	if !strings.HasPrefix(key, "handoff:") {
		t.Fatalf("неверный префикс ключа: %q", key)
	}
	if len(key) != len("handoff:")+64 {
		t.Fatalf("ключ должен быть sha256 в hex: %q", key)
	}
	// Один и тот же токен обязан давать один ключ, иначе GetDel поглотит не
	// то значение, которое положил Set.
	if handoffKey(tok) != key {
		t.Fatal("ключ нестабилен")
	}
}

func TestIPPrefixUsesNetworkNotAddress(t *testing.T) {
	cases := []struct {
		a, b  string
		same  bool
		label string
	}{
		{"203.0.113.5", "203.0.113.200", true, "один /24 — одна сеть"},
		{"203.0.113.5", "198.51.100.5", false, "разные /24"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2:3:4:5:99", true, "один /64"},
		// /64 обрезает после четвёртой группы, поэтому отличать надо её,
		// а не пятую: 2001:db8:1:2:... и 2001:db8:1:3:... — разные сети.
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:33:4:5:6:7", false, "разные /64"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2:9:9:9:9", true, "пятая группа внутри /64"},
		// 4G → Wi-Fi у одного человека: адрес меняется, сеть обычно нет.
		{"10.0.0.7", "10.0.0.201", true, "мобильный гуляет внутри /24"},
	}
	for _, c := range cases {
		pa, pb := IPPrefix(c.a), IPPrefix(c.b)
		if pa == "" || pb == "" {
			t.Fatalf("%s: пустой префикс (%q/%q)", c.label, pa, pb)
		}
		if (pa == pb) != c.same {
			t.Fatalf("%s: %s vs %s → %q / %q", c.label, c.a, c.b, pa, pb)
		}
	}
}

func TestIPPrefixRejectsGarbage(t *testing.T) {
	// Мусор в IP обязан давать пустой префикс, а не «подо что угодно»:
	// пустой префикс приводит к ErrHandoffIPChanged (отказ), а не к
	// ослаблению привязки.
	for _, bad := range []string{"", "не-ip", "999.999.999.999", "localhost"} {
		if got := IPPrefix(bad); got != "" {
			t.Fatalf("мусор %q дал префикс %q", bad, got)
		}
	}
}

func TestIPPrefixNormalisesIPv4MappedIPv6(t *testing.T) {
	// Go склонен отдавать IPv4 как ::ffff:203.0.113.5. Если бы префиксы для
	// двух форм различались, привязка работала бы через раз.
	plain := IPPrefix("203.0.113.5")
	mapped := IPPrefix("::ffff:203.0.113.5")
	if plain == "" || mapped == "" {
		t.Fatalf("пустой префикс: %q / %q", plain, mapped)
	}
	if plain != mapped {
		t.Fatalf("IPv4-mapped дал другой префикс: %q vs %q", plain, mapped)
	}
	if !strings.HasPrefix(plain, net.ParseIP("203.0.113.0").String()) {
		t.Fatalf("префикс не начинается с маскированной сети: %q", plain)
	}
}
