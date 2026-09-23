// Стоп-фильтр галлюцинаций (см. docs/project-book/04-architecture/06).
package ai

import "strings"

// stopWords — пугающие/запретные формулировки: заменяем весь ответ безопасным абзацем.
var stopWords = []string{
	"умрешь", "умрёшь", "умереть", "порча", "сглаз", "проклят",
	"развод неизбежен", "неизлечим", "приговор", "самоубийство",
}

// SafeReplacement — безопасный абзац вместо отфильтрованного ответа.
const SafeReplacement = "Карты подсвечивают напряжение в этой сфере. " +
	"Это не приговор, а приглашение разобраться: на что ты можешь опереться уже сейчас? " +
	"Что из происходящего зависит от тебя? Если тревожно — поговори с близким или специалистом."

// ContainsStopWords проверяет текст (нижний регистр, подстроки).
func ContainsStopWords(text string) bool {
	low := strings.ToLower(text)
	for _, w := range stopWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}
