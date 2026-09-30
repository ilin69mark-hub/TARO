// Package referral — рефералка v1 (см. docs/project-book/02-functional/06, T14).
//
// Флоу: apply {code} до 1-го done-чтения → pending; хук после 1-го чтения →
// проверки → completed. Бонус (config referral.bonus_days) обоим в
// subscriptions (plan referral_bonus), только при referee.tg_id NOT NULL.
//
// Лимиты (все из app_config 'referral', дефолты — константы ниже):
//
//	monthly_cap_days  — дней бонуса реферера за календарный месяц МСК;
//	lifetime_cap_days — за всю жизнь (0 = без потолка).
//
// Антиферма (миграция 037). Проверки идут по СНАПШОТУ, снятому в момент apply:
// fingerprint + IP-хэш обеих сторон. Снапшот нужен потому, что проверка в
// момент хука «есть ли tg_id» обходилась фермой: строка pending висела без
// проверок, а при merge аккаунтов переезжала на tg-юзера и там завершалась.
// Теперь завершение сверяет снапшот с текущим состоянием пользователей, поэтому
// и подмена личности через merge, и общий fingerprint ловятся на выдаче.
package referral

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"taro/api/internal/apierr"
	"taro/api/internal/auth"
	"taro/api/internal/entitlements"
)

// Дефолты конфига — используются, если строки в app_config нет или ключ пуст.
const (
	defaultBonusDays      = 3
	defaultMonthlyCapDays = 30
	// lifetimeCapUnlimited — sentinel «потолка нет» для условного upsert.
	lifetimeCapUnlimited = 1 << 30
	// defaultFPClusterLimit — порог «один браузер = много аккаунтов».
	//
	// ВАЖНО: это НЕ граница безопасности. Сервер видит одно и то же у «семьи за
	// одним ноутбуком» и у «фермы из одного браузера» — отличить их нельзя в
	// принципе, поэтому любой порог кого-то не пропустит. Выбран 3 как
	// разумный компромисс: пара/семья за одним браузером проходит, а ферма
	// упирается в 3 аккаунта на браузер. Настоящая граница ущерба — месячный
	// кэп реферера плюс цена Telegram-аккаунта, которая многократно выше
	// стоимости 3 дней безлимита.
	defaultFPClusterLimit = 3
	// pendingStaleAfter — с какого возраста pending считается зависшим и его
	// подбирает reconciler (т.к. хук fire-and-forget мог не отработать).
	pendingStaleAfter = 10 * time.Minute
	// reconcileBatch — потолок строк за один проход reconciler'а.
	reconcileBatch = 200
)

// Причины отказа. stored в referrals.reject_reason.
//
// Повторный apply разрешён ТОЛЬКО для reasonAnon: это единственный отказ, при
// котором виноват не реферер — человек просто применил код до входа через
// Telegram. Остальные причины — осознанный отказ по антиферме, и，允许
// переиграть его значило бы открыть перебор.
const (
	reasonNone         = ""
	reasonAnon         = "anon"
	reasonSelf         = "self"
	reasonFingerprint  = "fingerprint"
	reasonIP           = "ip"
	reasonIdentity     = "identity"
	reasonCluster      = "cluster"
	reasonMonthlyCap   = "monthly_cap"
	reasonLifetimeCap  = "lifetime_cap"
	reasonInternalFail = "internal"
)

// grantBonusDaysTx — то же, что entitlements.GrantBonusDays, но внутри tx (атомарно с completed).
func grantBonusDaysTx(ctx context.Context, tx pgx.Tx, userID, planCode string, days int) error {
	var planID string
	if err := tx.QueryRow(ctx,
		`SELECT id FROM (
			SELECT DISTINCT ON (code) id, code, is_active
			FROM plans WHERE code=$1
			ORDER BY code, valid_from DESC
		) latest WHERE latest.is_active`, planCode).Scan(&planID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO subscriptions (user_id, plan_id, plan_code, price_rub_snapshot, valid_until)
		 SELECT $1,$2,$3,0, GREATEST(COALESCE(MAX(valid_until), now()), now()) + make_interval(days => $4)
		   FROM subscriptions WHERE user_id=$1 AND status='active'`,
		userID, planID, planCode, days)
	return err
}

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // без похожих символов

// Service — рефералка.
type Service struct {
	pg *pgxpool.Pool
	en *entitlements.Service
}

// New возвращает сервис.
func New(pg *pgxpool.Pool, en *entitlements.Service) *Service {
	return &Service{pg: pg, en: en}
}

// genCode — случайный код 8 символов.
// 32 символа и 8 байт => 32^8 ≈ 1.1e12; int(v)%32 по байту РАВНОМЕРЕН
// (256 кратно 32), поэтому перебор бессмысленен.
func genCode() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	out := make([]byte, 8)
	for i, v := range b {
		out[i] = alphabet[int(v)%len(alphabet)]
	}
	return string(out)
}

// cfg — разобранный app_config 'referral'.
type cfg struct {
	// enabled — аварийный тормоз. Ключ сидился и валидировался, но НИ ОДНОГО
	// чтения не имел: выключить реферальную программу из панели было нельзя.
	// Это единственная настройка, которую нельзя было остановить при обнаружении
	// злоупотребления, поэтому она читается по-настоящему, а не удаляется.
	enabled        bool
	bonusDays      int
	monthlyCapDays int
	lifetimeCap    int
	antifarmFP     bool
	antifarmIP     bool
	antifarmIPFarm bool
	// fpClusterLimit — сколько УЖЕ существующих аккаунтов могут делить один
	// fingerprint, прежде чем новый приглашённый будет считаться фермой.
	// Семья за одним ноутбуком (3-4 человека) укладывается; 10 аккаунтов из
	// одного браузера — нет. 0 отключает проверку.
	fpClusterLimit int
	// ipClusterLimit — сколько разных рефёров могут прийти с одного адреса за
	// 30 дней. Выключен по умолчанию: CGNAT мобильных операторов делает его
	// источником ложных отказов.
	ipClusterLimit int
}

func (c cfg) lifetimeCapOrUnlimited() int {
	if c.lifetimeCap <= 0 {
		return lifetimeCapUnlimited
	}
	return c.lifetimeCap
}

// loadCfg читает app_config. Отсутствие строки или ключа = дефолт; битый JSON —
// тоже дефолт (фича не должна падать из-за мусора в конфиге).
func (s *Service) loadCfg(ctx context.Context) cfg {
	out := cfg{
		enabled:        true, // отсутствие ключа = программа работает
		bonusDays:      defaultBonusDays,
		monthlyCapDays: defaultMonthlyCapDays,
		antifarmFP:     true,
		// antifarm_ip ВЫКЛЮЧЕН по умолчанию, и это не осторожность, а требование
		// к окружению. IP берётся из X-Real-IP, который nginx ставит из
		// $remote_addr, а set_real_ip_from в deploy/nginx.conf НЕ настроен.
		// Значит стоит поставить Cloudflare (см. E04) — $remote_addr станет
		// edge-IP Cloudflare, одинаковым для всех, и проверка «реферер и рефёр за
		// одним адресом» начнёт отклонять почти всё. Второй источник ложных
		// отказов — CGNAT мобильных операторов.
		//
		// Включать только после того, как в nginx появится real_ip (Cloudflare
		// IP-ranges + real_ip_header CF-Connecting-IP) и это проверено.
		antifarmIP:     false,
		fpClusterLimit: defaultFPClusterLimit,
	}
	// value — JSONB, поэтому декодируем в []byte, а не в string.
	var raw []byte
	if err := s.pg.QueryRow(ctx, `SELECT value FROM app_config WHERE key='referral'`).Scan(&raw); err != nil {
		return out
	}
	var doc struct {
		Enabled            *bool `json:"enabled"`
		BonusDays          *int  `json:"bonus_days"`
		MonthlyCap         *int  `json:"monthly_cap"`
		LifetimeCapDays    *int  `json:"lifetime_cap_days"`
		AntifarmFP         *bool `json:"antifarm_fp"`
		AntifarmIP         *bool `json:"antifarm_ip"`
		FingerprintCluster *int  `json:"fingerprint_cluster_limit"`
		IPCluster          *int  `json:"ip_cluster_limit"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return out
	}
	if doc.Enabled != nil {
		out.enabled = *doc.Enabled
	}
	if doc.BonusDays != nil && *doc.BonusDays > 0 && *doc.BonusDays <= 365 {
		out.bonusDays = *doc.BonusDays
	}
	if doc.MonthlyCap != nil && *doc.MonthlyCap > 0 {
		out.monthlyCapDays = *doc.MonthlyCap
	}
	if doc.LifetimeCapDays != nil {
		out.lifetimeCap = *doc.LifetimeCapDays
	}
	if doc.AntifarmFP != nil {
		out.antifarmFP = *doc.AntifarmFP
	}
	if doc.AntifarmIP != nil {
		out.antifarmIP = *doc.AntifarmIP
	}
	if doc.FingerprintCluster != nil && *doc.FingerprintCluster >= 0 {
		out.fpClusterLimit = *doc.FingerprintCluster
	}
	if doc.IPCluster != nil && *doc.IPCluster >= 0 {
		out.ipClusterLimit = *doc.IPCluster
		out.antifarmIPFarm = *doc.IPCluster > 0
	}
	return out
}

// mskMonth — ключ календарного месяца в МСК. НЕ time.Now(): продукт везде
// считает периоды в Europe/Moscow (entitlements.mskDate, push), и локальная
// зона процесса сдвигала бы переключение месяца на часы (на проде UTC).
func mskMonth(now time.Time) string {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil || msk == nil {
		msk = time.FixedZone("MSK", 3*3600)
	}
	return now.In(msk).Format("2006-01")
}

// hashIP — sha256 от IP. Хранится хэш, а не сам адрес: он нужен только для
// сравнения «тот же адрес», а сырой IP в referrals — это лишние PII в таблице.
func hashIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])
}

// clientIP — X-Real-IP от nginx (тот же источник, что и в ratelimit).
func clientIP(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("X-Real-IP"))
}

// myCode возвращает или создает код юзера (колонка users.referral_code, см. D-баг 014).
func (s *Service) myCode(ctx context.Context, userID string) (string, error) {
	var code *string
	if err := s.pg.QueryRow(ctx,
		`SELECT referral_code FROM users WHERE id=$1`, userID).Scan(&code); err != nil {
		return "", err
	}
	if code != nil {
		return *code, nil
	}
	for range [5]struct{}{} {
		code := genCode()
		var got string
		err := s.pg.QueryRow(ctx,
			`UPDATE users SET referral_code=$1 WHERE id=$2 AND referral_code IS NULL RETURNING referral_code`,
			code, userID).Scan(&got)
		if err == nil {
			return got, nil
		}
	}
	return "", fmt.Errorf("code collision")
}

// HandleMe — GET /v1/referral/me: {code, invited, bonus_days}.
// Заодно запоминает IP, с которого юзер забрал/показал свой код: это «откуда
// инвайт используется» для антифермы (см. applyReferral).
func (s *Service) HandleMe(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	ctx := r.Context()
	code, err := s.myCode(ctx, uid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось получить код")
		return
	}
	if ip := hashIP(clientIP(r)); ip != "" {
		// Не фатально: антиферма по IP — дополнительный сигнал, его отсутствие
		// не должно ломать выдачу кода.
		_, _ = s.pg.Exec(ctx, `UPDATE users SET referral_ip=$1 WHERE id=$2 AND referral_ip IS DISTINCT FROM $1`, ip, uid)
	}
	var invited, bonus int
	_ = s.pg.QueryRow(ctx,
		`SELECT COUNT(*) FROM referrals WHERE referrer_id=$1 AND status='completed'`, uid).Scan(&invited)
	_ = s.pg.QueryRow(ctx,
		`SELECT COALESCE(SUM(bonus_days),0) FROM referrals WHERE (referrer_id=$1 OR referee_id=$1) AND status='completed'`, uid).Scan(&bonus)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "invited": invited, "bonus_days": bonus})
}

// identity — снимок антифермы одной стороны.
type identity struct {
	fp string
	ip string
}

// loadIdentity читает fingerprint и IP-хэш юзера.
func loadIdentity(ctx context.Context, q pgxQuerier, userID string) (identity, error) {
	var id identity
	var fp, ip *string
	err := q.QueryRow(ctx,
		`SELECT fingerprint, referral_ip FROM users WHERE id=$1`, userID).Scan(&fp, &ip)
	if err != nil {
		return id, err
	}
	if fp != nil {
		id.fp = *fp
	}
	if ip != nil {
		id.ip = *ip
	}
	return id, nil
}

type pgxQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// HandleApply — POST /v1/referral/apply {code} до 1-го done-чтения → pending.
//
// Контракт (docs/project-book/02-functional/06): у реферера может быть РОВНО
// одна привязка. Повторный apply с другим кодом — 409 ALREADY_REFERRED, а не
// тихий 200: раньше ON CONFLICT DO NOTHING проглатывал запрос, юзеру уходило
// «applied: pending», а привязка оставалась к первому коду.
//
// Исключение — переигровка отказа reasonAnon: человек применил код до входа
// через Telegram, отказ был не по его вине, и после входа он должен получить
// бонус. Отказы по антиферме и самому себе переигрывать нельзя.
func (s *Service) HandleApply(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r.Context())
	var req struct {
		Code string `json:"code"`
	}
	if !apierr.Decode(w, r, &req) || req.Code == "" || len(req.Code) > 16 || strings.ContainsRune(req.Code, 0) {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Нужен код")
		return
	}
	ctx := r.Context()
	c := s.loadCfg(ctx)
	// Аварийный тормоз: новые приглашения не создаются. Уже созданные pending
	// при этом доходят до конца (см. complete) — иначе выключатель отбирал бы
	// бонус у тех, кто честно применил код за минуту до остановки.
	if !c.enabled {
		apierr.Write(w, http.StatusServiceUnavailable, "REFERRAL_DISABLED",
			"Реферальная программа временно приостановлена")
		return
	}

	// status='active': код забаненного/удалённого юзера приниматься не должен
	// (раньше фильтра не было — бан не мешал аккаунту качать бонусы).
	var referrer string
	var refIdent identity
	if err := s.pg.QueryRow(ctx,
		`SELECT id FROM users WHERE referral_code=$1 AND status='active'`, req.Code).Scan(&referrer); err != nil {
		apierr.Write(w, http.StatusUnprocessableEntity, apierr.CodeValidation, "Неверный код")
		return
	}
	if referrer == uid {
		apierr.Write(w, http.StatusConflict, "SELF", "Свой код применить нельзя")
		return
	}
	refIdent, err := loadIdentity(ctx, s.pg, referrer)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
		return
	}

	// уже есть done-чтение? apply только до 1-го. Считаем status='done', а не все
	// строки: неудачное/прерванное чтение не должно сжигать право на бонус
	// (именно по нему срабатывает и хук завершения).
	var readings int
	_ = s.pg.QueryRow(ctx, `SELECT COUNT(*) FROM readings WHERE user_id=$1 AND status='done'`, uid).Scan(&readings)
	if readings > 0 {
		apierr.Write(w, http.StatusConflict, "ALREADY_REFERRED", "Бонус только для новичков")
		return
	}

	refereeIdent, err := loadIdentity(ctx, s.pg, uid)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
		return
	}
	refereeIP := hashIP(clientIP(r))
	if refereeIP == "" {
		refereeIP = refereeIdent.ip
	}

	tx, err := s.pg.Begin(ctx)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
		return
	}
	defer tx.Rollback(ctx)

	// Блокируем существующую строку: без FOR UPDATE параллельные apply с разными
	// кодами могли бы решить, что «привязки нет», и обе вставить.
	var existingID, existingStatus, existingReason string
	err = tx.QueryRow(ctx,
		`SELECT id::text, status, COALESCE(reject_reason,'') FROM referrals WHERE referee_id=$1 FOR UPDATE`, uid).
		Scan(&existingID, &existingStatus, &existingReason)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// привязки нет — вставляем.
	case err != nil:
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
		return
	case existingStatus == "rejected" && existingReason == reasonAnon:
		// Переиграть можно только если юзер уже вошёл через Telegram: иначе он
		// получил бы тот же отказ, и мы бы жгли попытку впустую.
		var tg *int64
		_ = tx.QueryRow(ctx, `SELECT tg_id FROM users WHERE id=$1`, uid).Scan(&tg)
		if tg == nil {
			apierr.Write(w, http.StatusConflict, "ALREADY_REFERRED",
				"Войдите через Telegram и примените код ещё раз")
			return
		}
		if _, err := tx.Exec(ctx, `
			UPDATE referrals SET
			  referrer_id=$1, applied_code=$2, bonus_days=$3, status='pending', reject_reason=NULL,
			  referee_fp=$4, referee_ip=$5, referrer_fp=$6, referrer_ip=$7, created_at=now()
			WHERE id=$8`,
			referrer, req.Code, c.bonusDays, refereeIdent.fp, refereeIP,
			refIdent.fp, refIdent.ip, existingID); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
			return
		}
		if err := tx.Commit(ctx); err != nil {
			apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"applied": "pending"})
		return
	default:
		// pending или completed — привязка уже есть, второй код не принимаем.
		msg := "Код уже применён"
		if existingStatus == "completed" {
			msg = "Бонус уже начислен"
		} else if existingReason != "" && existingReason != reasonAnon {
			msg = "Этот код уже использован и не может быть применён повторно"
		}
		apierr.Write(w, http.StatusConflict, "ALREADY_REFERRED", msg)
		return
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO referrals (referrer_id, referee_id, code, applied_code, status, bonus_days,
		                       referee_fp, referee_ip, referrer_fp, referrer_ip)
		VALUES ($1,$2,$3,$4,'pending',$5,$6,$7,$8,$9)`,
		referrer, uid, genCode(), req.Code, c.bonusDays,
		refereeIdent.fp, refereeIP, refIdent.fp, refIdent.ip); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "Не удалось применить код")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"applied": "pending"})
}

// rejectTx помечает строку отказом с причиной. Раньше причина не хранилась,
// поэтому «anon без TG» и «антиферма» были неразличимы, а значит нельзя было
// решить, кому положен переигров.
func rejectTx(ctx context.Context, tx pgx.Tx, refID, reason string) {
	_, _ = tx.Exec(ctx,
		`UPDATE referrals SET status='rejected', reject_reason=$1 WHERE id=$2 AND status='pending'`, reason, refID)
}

// CompleteOnFirstReading — хук после 1-го done-чтения (вызывать горутиной, не блокирует).
func (s *Service) CompleteOnFirstReading(ctx context.Context, refereeID string) {
	if err := s.complete(ctx, refereeID); err != nil {
		log.Printf("referral: complete %s: %v", refereeID, err)
	}
}

// complete — вся логика завершения в одной tx. Все ранние выходы коммитят
// (чтобы строка не осталась pending навсегда) либо откатывают (чтобы не
// списать кап без выдачи).
func (s *Service) complete(ctx context.Context, refereeID string) error {
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c := s.loadCfg(ctx)

	var refID, referrer, refFP, refIP, reason string
	var bonus int
	err = tx.QueryRow(ctx,
		`SELECT id::text, referrer_id, bonus_days,
		        COALESCE(referee_fp,''), COALESCE(referee_ip,''), COALESCE(reject_reason,'')
		   FROM referrals WHERE referee_id=$1 AND status='pending' FOR UPDATE`, refereeID).
		Scan(&refID, &referrer, &bonus, &refFP, &refIP, &reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // apply не было (или уже завершено) — нечего завершать
	}
	if err != nil {
		return err
	}

	if referrer == refereeID {
		// Аудит D: self-referral (артефакт мержа) — бонуса нет.
		rejectTx(ctx, tx, refID, reasonSelf)
		return tx.Commit(ctx)
	}
	var tg *int64
	if err := tx.QueryRow(ctx, `SELECT tg_id FROM users WHERE id=$1`, refereeID).Scan(&tg); err != nil {
		return err
	}
	if tg == nil {
		// anon-ферма отрезана (см. 02-functional/06). Причина reasonAnon —
		// единственная, которую можно переиграть после входа через Telegram.
		rejectTx(ctx, tx, refID, reasonAnon)
		return tx.Commit(ctx)
	}

	// Антиферма по снапшоту из apply. Проверяем ДО списания капа: отказ не должен
	// стоить рефереру месячного лимита.
	refereeIP := refIP
	if refereeIP == "" {
		// Исторические строки (до миграции 037) снапшота не имеют — берём IP
		// юзера, иначе проверка по IP молча выродилась бы в no-op.
		var curIP string
		_ = tx.QueryRow(ctx, `SELECT COALESCE(referral_ip,'') FROM users WHERE id=$1`, refereeID).Scan(&curIP)
		refereeIP = curIP
	}
	if r := s.antifarmReason(ctx, tx, c, refFP, refereeIP, referrer, refereeID); r != reasonNone {
		rejectTx(ctx, tx, refID, r)
		return tx.Commit(ctx)
	}
	// Кластерная проверка. Парные сигналы выше ловят «реферер и рефёр из одного
	// браузера», но НЕ ловят главный вектор: атакующий берёт ЧУЖОЙ код (тогда
	// fingerprint реферера другой) и плодит рефёров-однофамильцев из одного
	// браузера. Здесь считаем, сколько аккаунтов уже делят этот fingerprint.
	if r := s.clusterReason(ctx, tx, c, refereeID, refFP, refereeIP); r != reasonNone {
		rejectTx(ctx, tx, refID, r)
		return tx.Commit(ctx)
	}

	// кап реферера. A10/F-24: счётчик нельзя читать-compare-инкрементить, два
	// конкурентных завершения оба читали бы одно значение. Референс
	// сериализуется advisory-блокировкой, а сам кап проверяется условным
	// upsert'ом — решение атомарно на уровне БД.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "referral-cap:"+referrer); err != nil {
		return err
	}
	capApplied, capReason, err := applyReferralCapTx(ctx, tx, referrer, bonus, mskMonth(time.Now()), c)
	if err != nil {
		return err
	}
	if !capApplied {
		rejectTx(ctx, tx, refID, capReason)
		return tx.Commit(ctx)
	}
	if err := grantBonusDaysTx(ctx, tx, referrer, "referral_bonus", bonus); err != nil {
		return err
	}
	if err := grantBonusDaysTx(ctx, tx, refereeID, "referral_bonus", bonus); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE referrals SET status='completed', reject_reason=NULL WHERE id=$1 AND status='pending'`, refID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// antifarmReason сверяет снапшот из apply с текущим состоянием.
//
// Три независимых сигнала:
//
//	identity    — fingerprint рефера сменился между apply и complete. Это
//	              ровно тот обход, который был при merge: строка pending
//	              переезжала на другого юзера и завершалась у него;
//	fingerprint — реферер и рефёр пришли из одного браузера;
//	ip          — с одного адреса (за nginx).
//
// Сигналы ip/fingerprint выключаются конфигом (antifarm_fp/antifarm_ip): у
// мобильных операторов CGNAT даёт много честных юзеров с одним адресом, и
// владелец должен иметь возможность ослабить проверку без правки кода.
func (s *Service) antifarmReason(ctx context.Context, tx pgx.Tx, c cfg, refFP, refIP, referrer, refereeID string) string {
	// 1) Непрерывность личности. Проверяем всегда, независимо от конфига:
	// fingerprint рефера не должен смениться между apply и complete. Именно
	// этим отличается одно устройство от «одного человека»: подмена личности
	// через merge — не «мягкий» признак, а подмена субъекта.
	if refFP != "" {
		var curFP string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(fingerprint,'') FROM users WHERE id=$1`, refereeID).Scan(&curFP); err == nil {
			if curFP != "" && curFP != refFP {
				return reasonIdentity
			}
		}
	}
	if !c.antifarmFP && !c.antifarmIP {
		return reasonNone
	}
	// 2) Один браузер: реферер и рефёр с одним fingerprint.
	if c.antifarmFP && refFP != "" {
		var refFPNow string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(fingerprint,'') FROM users WHERE id=$1`, referrer).Scan(&refFPNow); err == nil {
			if refFPNow != "" && refFPNow == refFP {
				return reasonFingerprint
			}
		}
	}
	// 3) Один адрес. Сравниваем с referral_ip реферера — это адрес, с которого он
	// забрал/показал свой код, то есть откуда инвайт реально распространяется.
	if c.antifarmIP && refIP != "" {
		var refIPNow string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(referral_ip,'') FROM users WHERE id=$1`, referrer).Scan(&refIPNow); err == nil {
			if refIPNow != "" && refIPNow == refIP {
				return reasonIP
			}
		}
	}
	return reasonNone
}

// clusterReason — «один браузер/адрес = много аккаунтов».
//
// Парные проверки (antifarmReason) сравнивают реферера с рефёром. Эта —
// про количество: сколько ВООБЩЕ аккаунтов уже сидят на том же fingerprint.
// Нужна потому, что иначе вектор «чужой код + 10 своих аккаунтов из одного
// браузера» не ловится ничем: fingerprint реферера чужой, совпадения нет,
// а бонусы получает атакующий — по 3 дня на каждый из 10 аккаунтов.
//
// Порог fpClusterLimit подобран так, чтобы семья за одним ноутбуком (3–4
// человека, один fingerprint) проходила, а ферма из 10 аккаунтов — нет.
func (s *Service) clusterReason(ctx context.Context, tx pgx.Tx, c cfg, refereeID, refFP, refIP string) string {
	if c.fpClusterLimit > 0 && refFP != "" {
		var n int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE fingerprint=$1 AND id<>$2`, refFP, refereeID).Scan(&n); err == nil {
			if n >= c.fpClusterLimit {
				return reasonCluster
			}
		}
	}
	if c.antifarmIPFarm && c.ipClusterLimit > 0 && refIP != "" {
		var n int
		if err := tx.QueryRow(ctx, `
			SELECT COUNT(DISTINCT referee_id) FROM referrals
			 WHERE referee_ip=$1 AND created_at > now() - interval '30 days'`, refIP).Scan(&n); err == nil {
			if n >= c.ipClusterLimit {
				return reasonCluster
			}
		}
	}
	return reasonNone
}

// applyReferralCapTx резервирует бонус в месячном и lifetime-капе referrer.
// Возвращает applied=false и причину, если какой-либо потолок исчерпан.
//
// Вызывается под advisory-блокировкой referrer, поэтому чтение счётчика безопасно:
// все завершения рефералов одного referrer выстроены в очередь до конца транзакции.
// Дополнительно счётчик инкрементируется условно (WHERE в DO UPDATE) — страховка
// на случай, если кап начнут править из другого места кода: пропущенный UPDATE
// ничего не меняет, поэтому компенсирующего UPDATE не требуется.
func applyReferralCapTx(ctx context.Context, tx pgx.Tx, referrer string, bonus int, monthKey string, c cfg) (bool, string, error) {
	var used, lifetime int
	err := tx.QueryRow(ctx, `
		SELECT COALESCE(referral_bonus_month, 0), COALESCE(referral_bonus_lifetime, 0)
		  FROM entitlements WHERE user_id=$1`, referrer).Scan(&used, &lifetime)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, reasonInternalFail, err
	}
	// Счётчик месячный — за календарный месяц, поэтому при смене ключа он
	// обнуляется. Lifetime — общий и не сбрасывается никогда.
	if lastKey := currentMonthKey(ctx, tx, referrer); lastKey != "" && lastKey != monthKey {
		used = 0
	}
	if used+bonus > c.monthlyCapDays {
		return false, reasonMonthlyCap, nil
	}
	if lifetime+bonus > c.lifetimeCapOrUnlimited() {
		return false, reasonLifetimeCap, nil
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO entitlements (user_id, referral_bonus_month, referral_bonus_month_key, referral_bonus_lifetime)
		VALUES ($1,$2,$3,$2)
		ON CONFLICT (user_id) DO UPDATE SET
		  referral_bonus_month = CASE WHEN entitlements.referral_bonus_month_key=$3
		    THEN entitlements.referral_bonus_month + $2 ELSE $2 END,
		  referral_bonus_month_key = $3,
		  referral_bonus_lifetime = entitlements.referral_bonus_lifetime + $2
		WHERE (CASE WHEN entitlements.referral_bonus_month_key=$3
		      THEN entitlements.referral_bonus_month + $2 ELSE $2 END) <= $4
		  AND (entitlements.referral_bonus_lifetime + $2) <= $5`,
		referrer, bonus, monthKey, c.monthlyCapDays, c.lifetimeCapOrUnlimited())
	if err != nil {
		return false, reasonInternalFail, err
	}
	if tag.RowsAffected() != 1 {
		// Достижимо только гонкой с правкой капа извне — выясняем, какой именно.
		var m, lt int
		_ = tx.QueryRow(ctx, `SELECT COALESCE(referral_bonus_month,0), COALESCE(referral_bonus_lifetime,0)
			FROM entitlements WHERE user_id=$1`, referrer).Scan(&m, &lt)
		if lt+bonus > c.lifetimeCapOrUnlimited() {
			return false, reasonLifetimeCap, nil
		}
		return false, reasonMonthlyCap, nil
	}
	return true, reasonNone, nil
}

// currentMonthKey — ключ месяца, за который уже засчитан бонус.
func currentMonthKey(ctx context.Context, tx pgx.Tx, referrer string) string {
	var key *string
	if err := tx.QueryRow(ctx, `SELECT referral_bonus_month_key FROM entitlements WHERE user_id=$1`, referrer).Scan(&key); err != nil {
		return ""
	}
	if key == nil {
		return ""
	}
	return *key
}

// ReconcilePending — подбирает зависшие pending и завершает их.
//
// Зачем: хук fire-and-forget (горутина, ctx с таймаутом, все ошибки молча
// проглатываются). Рестарт, деплой или таймаут оставляли строку pending
// навсегда, а referee_id UNIQUE не давал применить код заново — бонус терялся
// безвозвратно. Этот проход — единственный путь, который такие строки возвращает
// в оборот. Совпадений с живым хуком не будет: FOR UPDATE в complete плюс
// предикат status='pending' делают повторный вызов no-op.
func (s *Service) ReconcilePending(ctx context.Context) (int, error) {
	rows, err := s.pg.Query(ctx, `
		SELECT r.referee_id::text
		  FROM referrals r
		  JOIN users u ON u.id = r.referee_id
		 WHERE r.status='pending'
		   AND r.created_at < now() - $1::interval
		   AND u.tg_id IS NOT NULL
		   AND EXISTS (SELECT 1 FROM readings rd WHERE rd.user_id=r.referee_id AND rd.status='done')
		 ORDER BY r.created_at
		 LIMIT $2`, pendingStaleAfter.String(), reconcileBatch)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if err := s.complete(ctx, id); err != nil {
			log.Printf("referral: reconcile %s: %v", id, err)
			continue
		}
		n++
	}
	return n, nil
}
