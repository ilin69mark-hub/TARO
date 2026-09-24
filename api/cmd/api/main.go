// Package main — api-public :8080 (см. docs/project-book/04-architecture/01).
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"taro/api/internal/ai"
	"taro/api/internal/apierr"
	"taro/api/internal/auth"
	"taro/api/internal/diary"
	"taro/api/internal/entitlements"
	"taro/api/internal/me"
	"taro/api/internal/payments"
	"taro/api/internal/push"
	"taro/api/internal/ratelimit"
	"taro/api/internal/readings"
	"taro/api/internal/referral"
	"taro/api/internal/spreads"
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

	sp := spreads.New(pg, rd)
	au := auth.New(pg, rd)
	en := entitlements.New(pg, rd)
	gw := ai.New(pg, rd)
	rf := referral.New(pg, en)
	rd_ := readings.New(pg, en, gw, rf)
	mv := me.New(pg, rd)
	py := payments.New(pg, mv)
	pu := push.New(pg)
	dy := diary.New(pg)

	// worker дописывания pending_fallback (см. ai/worker.go, T12/T13)
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	gw.StartWorker(workerCtx, pg, 30*time.Second)
	// тик протухания pending-платежей 15м (см. T29)
	go func() {
		defer apierr.Recover() // S06
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-t.C:
				func() {
					defer apierr.Recover() // S06
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					_, _ = py.ExpirePending(ctx)
				}()
			}
		}
	}()

	r := chi.NewRouter()
	// CSRF per-session (метод — нужен Redis, см. S07). Webhook исключен внутри.
	r.Use(au.RequireCSRF)
	// Go rate limits — второй рубеж после nginx (см. V31, 04-api-spec.md).
	r.Use(ratelimit.New(rd).Middleware)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","api":"public"}`))
	})
	r.Post("/v1/auth/telegram", au.HandleTelegram)
	r.Post("/v1/auth/anon", au.HandleAnon)
	r.With(au.RequireAuth).Post("/v1/auth/link", au.HandleLink)
	r.With(au.RequireAuth).Post("/v1/auth/refresh", au.HandleRefresh)
	r.With(au.RequireAuth).Post("/v1/auth/logout", au.HandleLogout)
	r.Get("/v1/spreads", sp.HandleList)
	r.Get("/v1/cards/{id}", sp.HandleCard) // публичные значения для SEO (см. U16)
	r.Get("/v1/plans", en.HandlePlans)
	r.With(au.RequireAuth).Get("/v1/entitlements/me", en.HandleMe)
	r.With(au.RequireAuth).Post("/v1/readings", rd_.HandleCreate)
	r.With(au.RequireAuth).Get("/v1/readings", rd_.HandleList)
	r.With(au.RequireAuth).Get("/v1/readings/{id}", rd_.HandleGet)
	r.With(au.RequireAuth).Get("/v1/streak/me", rd_.HandleStreak) // U20
	r.With(au.RequireAuth).Get("/v1/referral/me", rf.HandleMe)
	r.With(au.RequireAuth).Post("/v1/referral/apply", rf.HandleApply)
	r.With(au.RequireAuth).Delete("/v1/me", mv.HandleDelete)
	r.With(au.RequireAuth).Post("/v1/me/age", mv.HandleAge)
	r.With(au.RequireAuth).Post("/v1/payments/stars/invoice", py.HandleInvoice)
	r.Post("/v1/payments/stars/webhook", py.HandleWebhook) // Secret-Token, без CSRF (см. T29)
	r.With(au.RequireAuth).Post("/v1/payments/stars/verify", py.HandleVerify)
	r.With(au.RequireAuth).Get("/v1/ab/me", py.HandleVariant) // U22
	r.Get("/v1/push/public", pu.HandlePublicKey)
	r.With(au.RequireAuth).Post("/v1/push/subscribe", pu.HandleSubscribe)
	r.With(au.RequireAuth).Delete("/v1/push/unsubscribe", pu.HandleUnsubscribe)
	r.With(au.RequireAuth).Get("/v1/push/prefs", pu.HandleGetPrefs)
	r.With(au.RequireAuth).Post("/v1/push/prefs", pu.HandleSetPrefs)
	r.With(au.RequireAuth).Post("/v1/diary", dy.HandleCreate)
	r.With(au.RequireAuth).Get("/v1/diary", dy.HandleList)
	r.With(au.RequireAuth).Post("/v1/diary/export", dy.HandleExport) // POST+CSRF: GET-ссылкой триггерился скачивание (см. аудит B)
	r.With(au.RequireAuth).Get("/v1/diary/{id}", dy.HandleGet)
	r.With(au.RequireAuth).Put("/v1/diary/{id}", dy.HandleUpdate)
	r.With(au.RequireAuth).Delete("/v1/diary/{id}", dy.HandleDelete)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Не найдено")
	})

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("api-public listening on %s", addr)
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatal(err)
	}
}
