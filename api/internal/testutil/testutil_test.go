// Единственный тест пакета testutil, и он про то, чем этот пакет кормит остальные:
// идентичности для тестов обязаны быть РЕАЛЬНО уникальными, иначе результат
// теста зависит от порядка и числа прогонов (A14/F-18.4, A17/F-43).
package testutil

import (
	"net"
	"strconv"
	"testing"
)

func allowedFirstOctet(v byte) bool {
	for _, candidate := range uniqueIPFirstOctets {
		if candidate == v {
			return true
		}
	}
	return false
}

func TestUniqueIPIsParseableAndInPrivateSpace(t *testing.T) {
	for i := 0; i < 500; i++ {
		value := UniqueIP(t)
		ip := net.ParseIP(value)
		if ip == nil {
			t.Fatalf("UniqueIP returned an unparseable address: %q", value)
		}
		if ip.To4() == nil {
			t.Fatalf("UniqueIP must return IPv4, got %q", value)
		}
		if v4 := ip.To4(); !allowedFirstOctet(v4[0]) {
			t.Fatalf("UniqueIP must stay in the allowed prefixes, got %q", value)
		}
	}
}

func TestUniqueIPDoesNotAlias(t *testing.T) {
	// Главное свойство: адреса НЕ повторяются и НЕ обрезаются. Прежняя формула
	// (`base*1000+counter`, первый октет `&0x3f`) мапила n и n+2^22 на один
	// адрес, из-за чего соседние ПРОЦЕССЫ делили бакет лимита входа — и
	// TestE2EAdminLoginRateLimit падал в зависимости от того, гоняли ли его за
	// последние 15 минут.
	const calls = uniqueIPPerProcess
	seen := make(map[string]struct{}, calls)
	for i := 0; i < calls; i++ {
		value := UniqueIP(t)
		if _, dup := seen[value]; dup {
			t.Fatalf("UniqueIP repeated %q at call %d: addresses alias", value, i)
		}
		seen[value] = struct{}{}
	}
	// Соседние вызовы обязаны отличаться младшим битом адреса: это доказывает,
	// что счётчик не «схлопывается» маской (прежний `|0x01` терял бит).
	first := net.ParseIP(UniqueIP(t)).To4()
	second := net.ParseIP(UniqueIP(t)).To4()
	if strconv.Itoa(int(first[3])) == strconv.Itoa(int(second[3])) && first[2] == second[2] && first[1] == second[1] {
		t.Fatalf("consecutive UniqueIP calls collapsed to the same address: %d.%d.%d", first[1], first[2], first[3])
	}
}
