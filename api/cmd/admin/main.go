// Package main — api-admin слушает ADMIN_LISTEN_ADDR (по умолчанию 0.0.0.0:8081)
// и наружу не публикуется: единственная точка входа — admin-access на loopback
// хоста (см. ADR-06, docs/project-book/02-functional/07).
// Доступ — только SSH-туннель: ssh -L 8081:127.0.0.1:8081 vps
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

	"taro/api/internal/admin"
	"taro/api/internal/ai"
	"taro/api/internal/apierr"
	"taro/api/internal/me"
	"taro/api/internal/payments"
	"taro/api/internal/push"
	"taro/api/internal/ratelimit"
	"taro/api/internal/store"
)

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
	if err := validateOriginEnv("ADMIN_ORIGIN"); err != nil {
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
	// A17/F-43: админке нужен и клиент роли ai_cache, иначе отчёт cache_bytes
	// показывал бы только critical. Ошибка здесь не фатальна: админка обязана
	// подниматься даже если кэш недоступен.
	aiCacheRd, _, aiCacheErr := store.ConnectRedisAI()
	if aiCacheErr != nil {
		log.Printf("WARN: %v — роль ai_cache не будет показана в /v1/admin/cache", aiCacheErr)
		aiCacheRd = nil
	}
	if aiCacheRd != nil && aiCacheRd != rd {
		defer func() { _ = aiCacheRd.Close() }()
	}

	ad := admin.New(pg, rd)
	mv := me.New(pg, rd)
	py := payments.New(pg, mv)
	pu := push.New(pg)
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	shutdownSignalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	r := chi.NewRouter()
	// Лимиты и Origin-гейт — как на public (см. S10).
	r.Use(ratelimit.New(rd).Middleware)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","api":"admin"}`))
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := validateOriginEnv("ADMIN_ORIGIN"); err != nil {
			http.Error(w, "invalid ADMIN_ORIGIN", http.StatusServiceUnavailable)
			return
		}
		probeCtx, probeCancel := context.WithTimeout(req.Context(), 2*time.Second)
		stopProbeOnWorkerStop := context.AfterFunc(workerCtx, probeCancel)
		defer stopProbeOnWorkerStop()
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
		_, _ = w.Write([]byte(`{"status":"ready","api":"admin"}`))
	})
	r.Post("/v1/admin/login", ad.HandleLogin)                         // password login (см. D1)
	r.With(ad.RequireAdmin).Post("/v1/admin/logout", ad.HandleLogout) // отзыв сессии (см. аудит B)
	r.With(ad.RequireAdmin).Get("/v1/admin/config", ad.HandleGetConfig)
	r.With(ad.RequireAdmin).Post("/v1/admin/config/publish", ad.HandlePublish)
	r.With(ad.RequireAdmin).Post("/v1/admin/refund", py.HandleRefund)                   // ручной возврат Stars (см. T29)
	r.With(ad.RequireAdmin).Post("/v1/admin/remind-expiring", pu.HandleRemindExpiring)  // пуши за 72ч (см. U25)
	r.With(ad.RequireAdmin).Post("/v1/admin/push-evening", pu.HandleEvening)            // вечерняя рассылка (см. V23/V24)
	r.With(ad.RequireAdmin).Post("/v1/admin/push-streak-risk", pu.HandleStreakRisk)     // риск обрыва стрика (см. V25)
	r.With(ad.RequireAdmin).Get("/v1/admin/push-stats", pu.HandlePushStats)             // статистика 7д (см. V26)
	r.With(ad.RequireAdmin).Get("/v1/admin/payments", py.HandleAdminList)               // список платежей (см. V19)
	r.With(ad.RequireAdmin).Get("/v1/admin/payments/audit", py.HandleAdminWebhookAudit) // A09/F-09: читатель payment_webhook_events
	// A17/F-43: cache_bytes{role} — единственная доступная форма метрики без
	// prometheus: роли ai_cache и critical считаются отдельно.
	r.With(ad.RequireAdmin).Get("/v1/admin/cache", ai.HandleCacheStats(rd, aiCacheRd))
	r.With(ad.RequireAdmin).Post("/v1/admin/rotate-seasonal", ad.HandleRotateSeasonal) // сезоны по датам (см. V22)
	ad.RegisterAccessRoutes(r)                                                         // безлимит владельцу: grant/revoke/list (внутреннее/access)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		apierr.Write(w, http.StatusNotFound, apierr.CodeNotFound, "Не найдено")
	})

	// Наружу не публикуется: единственная точка входа — admin-access на
	// loopback хоста. Слушать нужно 0.0.0.0, потому что admin-access больше не
	// делит с нами network namespace (A02/F-02) и ходит по DNS-имени
	// api-admin из compose-сети admin. Для одиночного запуска на VPS:
	// ADMIN_LISTEN_ADDR=127.0.0.1:8081.
	addr := os.Getenv("ADMIN_LISTEN_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8081"
	}
	log.Printf("api-admin listening on %s", addr)
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
			log.Printf("api-admin shutdown: %v", err)
			_ = server.Close()
		}
		if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("api-admin server: %v", err)
		}
	}
}
