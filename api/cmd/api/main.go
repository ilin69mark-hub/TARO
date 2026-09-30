// Package main — api-public :8080 (см. docs/project-book/04-architecture/01).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
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

// referralReconcileTick — период подбора зависших referrals.status='pending'.
// Чаще 5 минут незачем: pending становится «зависшим» только через 10 минут
// (referral.pendingStaleAfter), то есть быстрее одного тика он всё равно не виден.
const referralReconcileTick = 5 * time.Minute

func validateOriginEnv(name string) error {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "?#") {
		return fmt.Errorf("%s must be an absolute HTTP(S) origin", name)
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery ||
		parsed.Fragment != "" || parsed.RawFragment != "" || strings.HasSuffix(parsed.Host, ":") {
		return fmt.Errorf("%s must be an absolute HTTP(S) origin", name)
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("%s must be an absolute HTTP(S) origin", name)
		}
	}
	return nil
}

func main() {
	if err := validateOriginEnv("PUBLIC_ORIGIN"); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pg, err := store.ConnectPG(ctx)
	if err != nil {
		log.Fatalf("pg connect: %v (DATABASE_URL обязателен)", err)
	}
	defer pg.Close()
	rd := store.ConnectRedis()
	defer func() { _ = rd.Close() }()

	// A17/F-43: bulk-кэш ответов AI получает СВОЮ роль и бюджет памяти.
	// Отсутствие REDIS_AI_ADDR — это деградация (кэш делит инстанс с лимитами и
	// сессиями), поэтому она громкая, а не молчаливая.
	cacheRd, cacheShared, cacheErr := store.ConnectRedisAI()
	if cacheErr != nil {
		log.Printf("WARN: %v — кэш ответов AI переключён на общий инстанс (A17/F-43)", cacheErr)
		cacheRd, cacheShared = rd, true
	}
	if cacheRd != rd {
		defer func() { _ = cacheRd.Close() }()
	} else if cacheShared {
		log.Printf("WARN: REDIS_AI_ADDR не задан — кэш ответов AI живёт на инстансе ключей лимитов и сессий; задайте REDIS_AI_ADDR (A17/F-43)")
	}

	sp := spreads.New(pg, rd)
	au := auth.New(pg, rd)
	en := entitlements.New(pg, rd)
	gw := ai.NewWithCache(pg, rd, cacheRd)
	rf := referral.New(pg, en)
	rd_ := readings.New(pg, en, gw, rf)
	mv := me.New(pg, rd)
	py := payments.New(pg, mv)
	pu := push.New(pg)
	dy := diary.New(pg)
	// worker дописывания pending_fallback (см. ai/worker.go, T12/T13)
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	shutdownSignalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	// Тик воркера настраивается (AI_WORKER_TICK_SECONDS, по умолчанию 2 с) —
	// раньше здесь стояло жёсткое 30*time.Second вместе с batch=1, и вместе они
	// давали потолок 2.00 readings/min: одно чтение за 30 секунд (A15/F-06).
	gw.StartWorker(workerCtx, pg, ai.WorkerTick())
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
					ctx, cancel := context.WithTimeout(workerCtx, 30*time.Second)
					defer cancel()
					_, _ = py.ExpirePending(ctx)
				}()
			}
		}
	}()
	// Reconciler рефералки. Хук CompleteOnFirstReading — горутина с таймаутом,
	// поэтому рестарт/деплой оставлял referrals.status='pending' навсегда, а
	// referee_id UNIQUE не давал применить код заново: бонус терялся безвозвратно.
	// Этот проход — единственный путь возврата таких строк в оборот.
	go func() {
		defer apierr.Recover() // S06
		t := time.NewTicker(referralReconcileTick)
		defer t.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-t.C:
				func() {
					defer apierr.Recover() // S06
					ctx, cancel := context.WithTimeout(workerCtx, 60*time.Second)
					defer cancel()
					if n, err := rf.ReconcilePending(ctx); err != nil {
						log.Printf("WARN: reconciler рефералки: %v", err)
					} else if n > 0 {
						log.Printf("reconciler рефералки: завершено %d", n)
					}
				}()
			}
		}
	}()

	r := chi.NewRouter()
	// Аудит D: лимит ПЕРВЫМ (иначе 403 от CSRF не двигали счётчик — бесплатные пробы).
	// Go rate limits — второй рубеж после nginx (см. V31, 04-api-spec.md).
	r.Use(ratelimit.New(rd).Middleware)
	// CSRF per-session (метод — нужен Redis, см. S07). Webhook исключен внутри.
	r.Use(au.RequireCSRF)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","api":"public"}`))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := validateOriginEnv("PUBLIC_ORIGIN"); err != nil {
			http.Error(w, "invalid PUBLIC_ORIGIN", http.StatusServiceUnavailable)
			return
		}
		probeCtx, probeCancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer probeCancel()
		if err := pg.Ping(probeCtx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := rd.Ping(probeCtx).Err(); err != nil {
			http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready","api":"public"}`))
	})
	r.Post("/v1/auth/telegram", au.HandleTelegram)
	r.Post("/v1/auth/anon", au.HandleAnon)
	r.With(au.RequireAuth).Post("/v1/auth/link", au.HandleLink)
	r.With(au.RequireAuth).Post("/v1/auth/handoff", au.HandleHandoff) // перенос покупки в TG (за флагом)
	r.With(au.RequireAuth).Post("/v1/auth/refresh", au.HandleRefresh)
	r.With(au.RequireAuth).Post("/v1/auth/logout", au.HandleLogout)
	r.Get("/v1/spreads", sp.HandleList)
	r.Get("/v1/cards/{id}", sp.HandleCard) // публичные значения для SEO (см. U16)
	r.Get("/v1/plans", en.HandlePlans)
	r.With(au.RequireAuth).Get("/v1/entitlements/me", en.HandleMe)
	r.With(au.RequireAuth).Post("/v1/readings", rd_.HandleCreate)
	r.With(au.RequireAuth).Get("/v1/readings", rd_.HandleList)
	r.With(au.RequireAuth).Get("/v1/readings/{id}", rd_.HandleGet)
	r.With(au.RequireAuth).Get("/v1/streak/me", rd_.HandleStreak)   // U20
	r.With(au.RequireAuth).Post("/v1/share", rd_.HandleCreateShare) // токен-шеринг (приватные ссылки)
	r.With(au.RequireAuth).Post("/v1/share/revoke", rd_.HandleRevokeShare)
	r.Get("/v1/share/{token}", rd_.HandleGetShare) // публично, превью без толкования
	r.With(au.RequireAuth).Get("/v1/referral/me", rf.HandleMe)
	r.With(au.RequireAuth).Post("/v1/referral/apply", rf.HandleApply)
	r.With(au.RequireAuth).Get("/v1/me", mv.HandleGet) // личность сессии: user_id, tg-привязка, has_payment
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
	server := &http.Server{Addr: addr, Handler: r}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()
	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-shutdownSignalCtx.Done():
		stopWorker()
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer drainCancel()
		if err := server.Shutdown(drainCtx); err != nil {
			log.Printf("api-public shutdown: %v", err)
			_ = server.Close()
		}
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("api-public server: %v", err)
		}
	}
}
