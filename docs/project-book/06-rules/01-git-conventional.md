# Git-стратегия (solo, но как команда)

> Статус: `draft`.
- Ветки: `main` (прод), `dev` (интеграция), `feat/<task>` — правило «1 фича = 1 ветка = 1 деплой».
- `main` защищена, только через PR `feat→dev→main`, сам себе ревьюер: чеклист в PR (линт, билд, скрин).
- Conventional Commits: `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`. Пример: `feat(readings): sse stream`.
- Теги: `v0.1.0-mvp`, релиз-ноуты в GH Releases.
