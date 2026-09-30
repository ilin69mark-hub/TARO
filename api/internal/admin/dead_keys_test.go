package admin

import (
	"encoding/json"
	"testing"
)

// Ключи, которые администратор мог записать, а код потом никогда не читал.
//
// Каждый из них проходил серверную валидацию, то есть панель радостно
// подтверждала сохранение. Для владельца это выглядело как «настройка есть», а
// для кода — как мусор в конфиге. Хуже всего с `ab.price_month.pct`: это было
// второе имя для величины, которую VariantFor читает как `split`, поэтому можно
// было записать `{"pct":50,"split":10}` и получить 10%, а не 50%, без единого
// предупреждения.
//
// Тест фиксирует, что таких ключей больше нет, и что валидатор отвергает их
// явно, а не молча.
func TestDeadConfigKeysAreRejected(t *testing.T) {
	dead := []struct{ key, value, why string }{
		{"ab.price_month", `{"enabled":true,"control":299,"test":349,"split":10,"pct":50}`,
			"pct не читается, величина называется split"},
		{"trial", `{"enabled":true,"days":3,"require_tg":true}`,
			"require_tg не читается parseTrialConfig"},
	}
	for _, tc := range dead {
		if validateConfigValue(tc.key, json.RawMessage(tc.value)) {
			t.Errorf("%s=%s принят, хотя код его не читает (%s)", tc.key, tc.value, tc.why)
		}
	}
	// А действующие имена по-прежнему принимаются — иначе мы бы сломали рабочие
	// настройки, убирая мёртвые.
	for _, ok := range []struct{ key, value string }{
		{"ab.price_month", `{"enabled":true,"control":299,"test":349,"split":50}`},
		{"trial", `{"enabled":true,"days":3}`},
	} {
		if !validateConfigValue(ok.key, json.RawMessage(ok.value)) {
			t.Errorf("рабочее значение %s=%s отвергнуто", ok.key, ok.value)
		}
	}
}
