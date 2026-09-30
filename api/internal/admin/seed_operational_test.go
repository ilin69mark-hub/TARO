package admin

import (
	"context"
	"encoding/json"
	"testing"

	"taro/api/internal/testutil"
)

// Посеянные значения (миграция 041) обязаны быть валидными и выключенными.
//
// Проверяем ровно то, из-за чего эти строки и посеялись: раньше ключи были в
// allowlist и в панели, но строк в базе не было, поэтому панель показывала
// пустую textarea и администратору приходилось угадывать схему JSON. Если
// посеянное значение не пройдёт валидатор, админ не сможет его даже
// переоткрыть для правки.
func TestSeededOperationalConfigValidates(t *testing.T) {
	seeds := map[string]string{
		"ab.price_month":   `{"enabled":false,"control":299,"test":349,"split":50}`,
		"offers.winback":   `{"enabled":false,"pct":20}`,
		"spreads.seasonal": `[]`,
		"auth":             `{"handoff_enabled":false}`,
		"safety.crisis":    `{"crisis_resource_text":"обратись в местные экстренные службы"}`,
	}
	for key, val := range seeds {
		if !validateConfigValue(key, json.RawMessage(val)) {
			t.Errorf("посеянное значение %s=%s не проходит валидацию", key, val)
		}
	}
}

// Всё посеянное выключено. Проверка по списку, а не по факту: цена, winback,
// перенос покупки и сезонная ротация не должны включиться сами при миграции.
func TestSeededOperationalConfigIsDisabledByDefault(t *testing.T) {
	ctx := context.Background()
	_, pg, _ := testutil.Live(t)
	rows, err := pg.Query(ctx, `
		SELECT key, value FROM app_config
		WHERE key IN ('ab.price_month','offers.winback','auth')`)
	if err != nil {
		t.Skip("миграция 041 не применена")
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			t.Fatal(err)
		}
		var cfg map[string]any
		if json.Unmarshal(raw, &cfg) != nil {
			t.Fatalf("%s не разбирается: %s", key, raw)
		}
		for field, v := range cfg {
			if on, isBool := v.(bool); isBool && on {
				t.Errorf("%s.%s включён миграцией — должно быть выключено", key, field)
			}
		}
	}
}
