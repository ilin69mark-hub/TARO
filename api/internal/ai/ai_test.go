// Тесты AI-фильтра и хэша (см. 04-architecture/06, T13).
package ai

import "testing"

func TestStopWords(t *testing.T) {
	if !ContainsStopWords("Тебя ждет порча и гибель") {
		t.Fatal("stop-word missed")
	}
	if ContainsStopWords("Карты подсвечивают напряжение. На что опереться?") {
		t.Fatal("false positive")
	}
}

func TestPromptHashNoPII(t *testing.T) {
	cards := []CardValue{{CardID: 1, Reversed: true, Position: 0}}
	a := PromptHash("m", "daily", cards)
	b := PromptHash("m", "daily", cards)
	if a != b || a == "" {
		t.Fatal("hash not deterministic")
	}
	// хэш не зависит от вопроса: вопрос в PromptHash не входит по построению
	c := PromptHash("m", "three", cards)
	if a == c {
		t.Fatal("hash ignores spread")
	}
}

func TestCacheKeyFormat(t *testing.T) {
	k := CacheKey("openai/gpt-4o-mini", "daily", []CardValue{{CardID: 0}})
	if len(k) < len("ai:cache:") {
		t.Fatal("bad cache key")
	}
}
