// Package main — api-admin 127.0.0.1:8081, наружу не публикуется
// (см. ADR-06, docs/project-book/02-functional/07).
// Доступ — только SSH-туннель: ssh -L 8081:127.0.0.1:8081 vps
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/admin"
	"taro/api/internal/apierr"
	"taro/api/internal/me"
	"taro/api/internal/payments"
	"taro/api/internal/push"
	"taro/api/internal/ratelimit"
	"taro/api/internal/store"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pg, err := store.ConnectPG(ctx)
	if err != nil {
		log.Fatalf("pg connect: %v (DATABASE_URL обязателен)", err)
	}
	defer pg.Close()
	rd := store.ConnectRedis()
	defer func() { _ = rd.Close() }()

	ad := admin.New(pg, rd)
	mv := me.New(pg, rd)
	py := payments.New(pg, mv)
	pu := push.New(pg)

	r := chi.NewRouter()
	// Лимиты и Origin-гейт — как на public (см. S10): login без троттлинга брутфорсится.
	r.Use(ratelimit.New(rd).Middleware)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","api":"admin"}`))
	})
	r.Post("/v1/admin/login", ad.HandleLogin) // вход по TG + whitelist (см. D1)
	r.With(ad.RequireAdmin).Get("/v1/admin/config", ad.HandleGetConfig)
	r.With(ad.RequireAdmin).Post("/v1/admin/config/publish", ad.HandlePublish)
	r.With(ad.RequireAdmin).Post("/v1/admin/refund", py.HandleRefund)                  // ручной возврат Stars (см. T29)
	r.With(ad.RequireAdmin).Post("/v1/admin/remind-expiring", pu.HandleRemindExpiring) // пуши за 72ч (см. U25)
	r.With(ad.RequireAdmin).Post("/v1/admin/push-evening", pu.HandleEvening)           // вечерняя рассылка (см. V23/V24)
	r.With(ad.RequireAdmin).Post("/v1/admin/push-streak-risk", pu.HandleStreakRisk)    // риск обрыва стрика (см. V25)
	r.With(ad.RequireAdmin).Get("/v1/admin/push-stats", pu.HandlePushStats)            // статистика 7д (см. V26)
	r.With(ad.RequireAdmin).Get("/v1/admin/payments", py.HandleAdminList)              // список платежей (см. V19)
	r.With(ad.RequireAdmin).Post("/v1/admin/rotate-seasonal", ad.HandleRotateSeasonal) // сезоны по датам (см. V22)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Не найдено")
	})

	const addr = "127.0.0.1:8081" // НЕ менять на :8081 — наружу нельзя (см. Книгу)
	log.Printf("api-admin listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
