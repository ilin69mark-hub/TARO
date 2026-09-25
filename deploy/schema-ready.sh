#!/bin/sh
set -eu

fail() {
  printf '%s\n' "$1" >&2
  exit 1
}

decimal_value() {
  value=$1
  case "$value" in
    ''|*[!0-9]*) return 1 ;;
  esac
  while [ "$value" != "0" ] && [ "${value#0}" != "$value" ]; do
    value=${value#0}
  done
  printf '%s' "$value"
}

if [ -z "${DATABASE_URL:-}" ]; then
  [ -n "${POSTGRES_PASSWORD:-}" ] || fail 'DATABASE_URL or POSTGRES_PASSWORD is required'
  DATABASE_URL="postgres://taro:${POSTGRES_PASSWORD}@db:5432/taro?sslmode=disable"
  export DATABASE_URL
fi

preflight_migration() {
  case "${MIGRATION_DOWN_STEPS:-}" in
    ''|*[!0-9]*) fail 'MIGRATION_DOWN_STEPS must be a non-negative integer' ;;
  esac
  case "${MIGRATION_LATEST_VERSION:-}" in
    ''|*[!0-9]*) fail 'MIGRATION_LATEST_VERSION must be a positive integer' ;;
  esac
  steps=$(decimal_value "$MIGRATION_DOWN_STEPS")
  latest=$(decimal_value "$MIGRATION_LATEST_VERSION")
  [ "$latest" -gt 0 ] || fail 'MIGRATION_LATEST_VERSION must be positive'

  present="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT to_regclass('public.schema_migrations') IS NOT NULL")"
  [ "$present" = t ] || fail 'migration down preflight: schema_migrations is missing'
  rows="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM schema_migrations")"
  [ "$rows" = 1 ] || fail 'migration down preflight: schema_migrations must contain one row'
  state="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT version::text || '|' || dirty::text FROM schema_migrations")"
  version="${state%%|*}"
  dirty="${state#*|}"
  [ "$dirty" = false ] || fail 'migration down preflight: database is dirty'
  case "$version" in
    ''|*[!0-9]*) fail 'migration down preflight: current version is invalid' ;;
  esac
  current=$(decimal_value "$version")
  [ "$current" -gt 0 ] || fail 'migration down preflight: current version must be positive'
  [ "$current" -le "$latest" ] || fail "migration down preflight: current version $current exceeds manifest version $latest"
  target=$((current - steps))
  [ "$target" -ge 1 ] || fail "migration down preflight: target version $target is outside the schema"

  for forward_version in 27 28 31 32 33 34; do
    if [ "$current" -ge "$forward_version" ] && [ "$target" -lt "$forward_version" ]; then
      fail "migration down preflight refused: migration $forward_version is forward-only"
    fi
  done

  if [ "$current" -ge 30 ] && [ "$target" -lt 30 ]; then
    rotated="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT EXISTS (SELECT 1 FROM admin_accounts WHERE session_version <> 1)")"
    [ "$rotated" = f ] || fail 'migration down preflight refused: admin session versions have rotated'
  fi

  if [ "$current" -ge 29 ] && [ "$target" -lt 29 ]; then
    populated="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT EXISTS (SELECT 1 FROM payments) OR EXISTS (SELECT 1 FROM subscriptions) OR EXISTS (SELECT 1 FROM payment_webhook_events)")"
    [ "$populated" = f ] || fail 'migration down preflight refused: payment history or webhook audit is populated'
  fi

  printf 'migration down preflight: current version %s, target %s is safe\n' "$current" "$target"
}

if [ "${MIGRATION_PREFLIGHT:-0}" = 1 ]; then
  preflight_migration
  exit 0
fi

: "${EXPECTED_MIGRATION_VERSION:?EXPECTED_MIGRATION_VERSION is required}"
case "$EXPECTED_MIGRATION_VERSION" in
  ''|*[!0-9]*)
    fail 'EXPECTED_MIGRATION_VERSION must be an integer'
    ;;
esac
EXPECTED_MIGRATION_VERSION=$(decimal_value "$EXPECTED_MIGRATION_VERSION")
export EXPECTED_MIGRATION_VERSION
SCHEMA_READINESS_MODE="${SCHEMA_READINESS_MODE:-schema}"
case "$SCHEMA_READINESS_MODE" in
  schema|deploy) ;;
  *) fail 'SCHEMA_READINESS_MODE must be schema or deploy' ;;
esac

present="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT to_regclass('public.schema_migrations') IS NOT NULL")"
[ "$present" = t ] || fail 'migration readiness: schema_migrations is missing'
rows="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM schema_migrations")"
[ "$rows" = 1 ] || fail 'migration readiness: schema_migrations must contain one row'
state="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT version::text || '|' || dirty::text FROM schema_migrations")"
version="${state%%|*}"
dirty="${state#*|}"
[ "$dirty" = false ] || fail 'migration readiness: database is dirty'
[ "$version" = "$EXPECTED_MIGRATION_VERSION" ] || fail "migration readiness: version $version, expected $EXPECTED_MIGRATION_VERSION"

missing="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT COALESCE(string_agg(required.name, ', ' ORDER BY required.name), '') FROM (VALUES ('users'), ('plans'), ('spreads'), ('readings'), ('push_subscriptions'), ('push_preferences'), ('push_logs'), ('trial_grants'), ('share_tokens'), ('admin_accounts'), ('payment_webhook_events'), ('reading_quota_repair_031'), ('reading_authorization_receipts'), ('reading_quota_quarantine_034')) AS required(name) WHERE to_regclass('public.' || required.name) IS NULL")"
[ -z "$missing" ] || fail "migration readiness: missing tables: $missing"

missing_objects="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "
SELECT COALESCE(string_agg(required.name, ', ' ORDER BY required.name), '')
FROM (
  VALUES
    ('admin_accounts.session_version', EXISTS (
      SELECT 1
      FROM pg_attribute a
      JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
      WHERE a.attrelid = to_regclass('public.admin_accounts')
        AND a.attname = 'session_version'
        AND a.attnum > 0
        AND NOT a.attisdropped
        AND a.atttypid = 'bigint'::regtype
        AND a.attnotnull
        AND pg_get_expr(d.adbin, d.adrelid) = '1'
    )),
    ('admin_accounts.session_version_check', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.admin_accounts')
        AND c.contype = 'c'
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%session_version%'
        AND pg_get_constraintdef(c.oid) ILIKE '%> 0%'
    )),
    ('admin_accounts.user_id_fk', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.admin_accounts')
        AND c.contype = 'f'
        AND c.confrelid = to_regclass('public.users')
        AND c.confdeltype = 'c'
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%user_id%'
    )),
    ('readings.quota_state', EXISTS (
      SELECT 1
      FROM pg_attribute a
      JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
      WHERE a.attrelid = to_regclass('public.readings')
        AND a.attname = 'quota_state'
        AND a.attnum > 0
        AND NOT a.attisdropped
        AND a.atttypid = 'text'::regtype
        AND a.attnotnull
        AND pg_get_expr(d.adbin, d.adrelid) LIKE '%unchecked%'
    )),
    ('readings.quota_state_check', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.readings')
        AND c.contype = 'c'
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%quota_state%'
        AND pg_get_constraintdef(c.oid) ILIKE '%unchecked%'
        AND pg_get_constraintdef(c.oid) ILIKE '%allowed%'
        AND pg_get_constraintdef(c.oid) ILIKE '%denied%'
        AND pg_get_constraintdef(c.oid) ILIKE '%error%'
    )),
    ('idx_readings_worker_quota', EXISTS (
      SELECT 1
      FROM pg_index x
      WHERE x.indexrelid = to_regclass('public.idx_readings_worker_quota')
        AND x.indisvalid
        AND x.indisready
        AND x.indislive
    )),
    ('reading_quota_repair_031.reading_id', EXISTS (
      SELECT 1
      FROM pg_attribute
      WHERE attrelid = to_regclass('public.reading_quota_repair_031')
        AND attname = 'reading_id'
        AND attnum > 0
        AND NOT attisdropped
        AND atttypid = 'uuid'::regtype
    )),
    ('reading_quota_repair_031.reading_fk', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.reading_quota_repair_031')
        AND c.contype = 'f'
        AND c.confrelid = to_regclass('public.readings')
        AND c.confdeltype = 'c'
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%reading_id%'
    )),
    ('reading_quota_repair_031.primary_key', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.reading_quota_repair_031')
        AND c.contype = 'p'
        AND NOT c.condeferrable
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%PRIMARY KEY (reading_id)%'
    )),
    ('reading_authorization_receipts.reading_id', EXISTS (
      SELECT 1
      FROM pg_attribute
      WHERE attrelid = to_regclass('public.reading_authorization_receipts')
        AND attname = 'reading_id'
        AND attnum > 0
        AND NOT attisdropped
        AND atttypid = 'uuid'::regtype
    )),
    ('reading_authorization_receipts.user_id', EXISTS (
      SELECT 1
      FROM pg_attribute
      WHERE attrelid = to_regclass('public.reading_authorization_receipts')
        AND attname = 'user_id'
        AND attnum > 0
        AND NOT attisdropped
        AND atttypid = 'uuid'::regtype
    )),
    ('reading_authorization_receipts.entitlement_id', EXISTS (
      SELECT 1
      FROM pg_attribute
      WHERE attrelid = to_regclass('public.reading_authorization_receipts')
        AND attname = 'entitlement_id'
        AND attnum > 0
        AND NOT attisdropped
        AND atttypid = 'uuid'::regtype
    )),
    ('reading_authorization_receipts.primary_key', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.reading_authorization_receipts')
        AND c.contype = 'p'
        AND NOT c.condeferrable
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%PRIMARY KEY (reading_id)%'
    )),
    ('reading_authorization_receipts.reading_fk', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.reading_authorization_receipts')
        AND c.contype = 'f'
        AND c.confrelid = to_regclass('public.readings')
        AND c.confdeltype = 'c'
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%reading_id%'
    )),
    ('reading_authorization_receipts.user_fk', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.reading_authorization_receipts')
        AND c.contype = 'f'
        AND c.confrelid = to_regclass('public.users')
        AND c.confdeltype = 'c'
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%user_id%'
    )),
    ('reading_authorization_receipts.entitlement_fk', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.reading_authorization_receipts')
        AND c.contype = 'f'
        AND c.confrelid = to_regclass('public.single_entitlements')
        AND c.confdeltype = 'n'
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%entitlement_id%'
    )),
    ('idx_reading_authorization_receipts_entitlement', EXISTS (
      SELECT 1
      FROM pg_index x
      WHERE x.indexrelid = to_regclass('public.idx_reading_authorization_receipts_entitlement')
        AND x.indisvalid
        AND x.indisready
        AND x.indislive
    )),
    ('idx_reading_authorization_receipts_user_kind', EXISTS (
      SELECT 1
      FROM pg_index x
      WHERE x.indexrelid = to_regclass('public.idx_reading_authorization_receipts_user_kind')
        AND x.indisvalid
        AND x.indisready
        AND x.indislive
    )),
    ('idx_readings_authorization_recovery', EXISTS (
      SELECT 1
      FROM pg_index x
      WHERE x.indexrelid = to_regclass('public.idx_readings_authorization_recovery')
        AND x.indisvalid
        AND x.indisready
        AND x.indislive
    )),
    ('reading_terminal_quota_guard_function', to_regprocedure('public.reading_terminal_quota_guard()') IS NOT NULL),
    ('reading_terminal_quota_guard_034_function', to_regprocedure('public.reading_terminal_quota_guard_034()') IS NOT NULL),
    ('reading_terminal_quota_guard_trigger', EXISTS (
      SELECT 1
      FROM pg_trigger t
      WHERE t.tgrelid = to_regclass('public.readings')
        AND t.tgname = 'readings_terminal_quota_guard'
        AND t.tgenabled IN ('O', 'A')
        AND pg_get_triggerdef(t.oid) ILIKE '%BEFORE INSERT OR UPDATE%'
        AND pg_get_triggerdef(t.oid) ILIKE '%reading_terminal_quota_guard_034%'
    )),
    ('reading_quota_quarantine_034.primary_key', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.reading_quota_quarantine_034')
        AND c.contype = 'p'
        AND NOT c.condeferrable
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%PRIMARY KEY (reading_id)%'
    )),
    ('idx_reading_quota_quarantine_034_user', EXISTS (
      SELECT 1
      FROM pg_index x
      WHERE x.indexrelid = to_regclass('public.idx_reading_quota_quarantine_034_user')
        AND x.indisvalid
        AND x.indisready
        AND x.indislive
    )),
    ('payments_snapshot_guard_function', to_regprocedure('public.payments_snapshot_guard()') IS NOT NULL),
    ('payments_snapshot_guard_trigger', EXISTS (
      SELECT 1
      FROM pg_trigger t
      WHERE t.tgrelid = to_regclass('public.payments')
        AND t.tgname = 'payments_snapshot_guard'
        AND t.tgenabled IN ('O', 'A')
        AND pg_get_triggerdef(t.oid) ILIKE '%BEFORE INSERT OR UPDATE%'
        AND pg_get_triggerdef(t.oid) ILIKE '%payments_snapshot_guard%'
        AND pg_get_triggerdef(t.oid) ILIKE '%plan_id%'
        AND pg_get_triggerdef(t.oid) ILIKE '%purchase_fingerprint%'
        AND pg_get_triggerdef(t.oid) ILIKE '%duration_days_snapshot%'
        AND pg_get_triggerdef(t.oid) ILIKE '%idempotency_key%'
        AND pg_get_triggerdef(t.oid) ILIKE '%refund_state%'
    )),
    ('payment_webhook_events.payment_id', EXISTS (
      SELECT 1
      FROM pg_attribute
      WHERE attrelid = to_regclass('public.payment_webhook_events')
        AND attname = 'payment_id'
        AND attnum > 0
        AND NOT attisdropped
        AND atttypid = 'uuid'::regtype
        AND attnotnull
    )),
    ('payment_webhook_events.event_hash', EXISTS (
      SELECT 1
      FROM pg_attribute
      WHERE attrelid = to_regclass('public.payment_webhook_events')
        AND attname = 'event_hash'
        AND attnum > 0
        AND NOT attisdropped
        AND atttypid = 'text'::regtype
        AND attnotnull
    )),
    ('payment_webhook_events.primary_key', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.payment_webhook_events')
        AND c.contype = 'p'
        AND NOT c.condeferrable
        AND c.convalidated
        AND pg_get_constraintdef(c.oid) ILIKE '%PRIMARY KEY (id)%'
    )),
    ('payment_webhook_events.unique_payment_event', EXISTS (
      SELECT 1
      FROM pg_index x
      WHERE x.indrelid = to_regclass('public.payment_webhook_events')
        AND x.indisunique
        AND x.indisvalid
        AND x.indisready
        AND x.indislive
        AND x.indimmediate
        AND x.indnkeyatts = 2
        AND x.indnatts = 2
        AND x.indkey[0] = (SELECT attnum FROM pg_attribute WHERE attrelid = to_regclass('public.payment_webhook_events') AND attname = 'payment_id' AND attnum > 0 AND NOT attisdropped)
        AND x.indkey[1] = (SELECT attnum FROM pg_attribute WHERE attrelid = to_regclass('public.payment_webhook_events') AND attname = 'event_hash' AND attnum > 0 AND NOT attisdropped)
        AND x.indpred IS NULL
        AND x.indexprs IS NULL
    )),
    ('payment_webhook_events.event_hash_check', EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.payment_webhook_events')
        AND c.contype = 'c'
        AND c.convalidated
        AND array_length(c.conkey, 1) = 1
        AND c.conkey[1] = (SELECT attnum FROM pg_attribute WHERE attrelid = to_regclass('public.payment_webhook_events') AND attname = 'event_hash' AND attnum > 0 AND NOT attisdropped)
        AND pg_get_expr(c.conbin, c.conrelid) ILIKE '%event_hash%'
        AND pg_get_expr(c.conbin, c.conrelid) ILIKE '%[0-9a-f]%'
        AND pg_get_expr(c.conbin, c.conrelid) ILIKE '%64%'
        AND pg_get_expr(c.conbin, c.conrelid) !~* '(^|[^a-z])or([^a-z]|$)'
    )),
    ('idx_payment_webhook_events_payment_created', EXISTS (
      SELECT 1
      FROM pg_index x
      WHERE x.indexrelid = to_regclass('public.idx_payment_webhook_events_payment_created')
        AND x.indisvalid
        AND x.indisready
        AND x.indislive
    )),
    ('payment_webhook_events.no_payment_fk', NOT EXISTS (
      SELECT 1
      FROM pg_constraint c
      WHERE c.conrelid = to_regclass('public.payment_webhook_events')
        AND c.contype = 'f'
        AND c.confrelid = to_regclass('public.payments')
    ))
) AS required(name, present)
WHERE NOT required.present")"
[ -z "$missing_objects" ] || fail "migration readiness: missing or invalid objects: $missing_objects"

check_behavior() {
  if ! psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
BEGIN;
DO $probe$
DECLARE
  probe_user uuid := gen_random_uuid();
  probe_plan uuid := gen_random_uuid();
  probe_allowed_reading uuid := gen_random_uuid();
  probe_denied_reading uuid := gen_random_uuid();
  probe_payment uuid := gen_random_uuid();
  probe_payment_two uuid := gen_random_uuid();
  probe_spread text;
  error_message text;
  denied_rejected boolean := false;
  update_rejected boolean := false;
  receipt_rejected boolean := false;
  quarantine_rejected boolean := false;
  payment_rejected boolean := false;
  webhook_hash_rejected boolean := false;
  webhook_short_hash_rejected boolean := false;
  webhook_cross_payment_accepted boolean := false;
  receipt_count integer;
  quarantine_count integer;
  webhook_count integer;
  probe_price integer;
BEGIN
  INSERT INTO users (id) VALUES (probe_user);
  SELECT code INTO probe_spread FROM spreads ORDER BY code LIMIT 1;
  IF probe_spread IS NULL THEN
    INSERT INTO spreads (code, name_ru) VALUES ('daily', 'readiness') RETURNING code INTO probe_spread;
  END IF;
  INSERT INTO plans (id, code, price_rub, stars_amount, duration_days)
    VALUES (probe_plan, 'single_99', 99, 99, 1);

  INSERT INTO readings (id, user_id, spread_code, interpretation, seed, status, quota_state)
    VALUES (probe_allowed_reading, probe_user, probe_spread, 'readiness-allowed', 1, 'done', 'allowed');
  UPDATE readings
     SET interpretation = 'readiness-allowed-updated'
   WHERE id = probe_allowed_reading;
  IF NOT EXISTS (SELECT 1 FROM readings WHERE id = probe_allowed_reading AND quota_state = 'allowed') THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: allowed reading update was rejected';
  END IF;

  BEGIN
    INSERT INTO readings (id, user_id, spread_code, interpretation, seed, status, quota_state)
      VALUES (probe_denied_reading, probe_user, probe_spread, 'readiness-denied', 2, 'done', 'denied');
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS error_message = MESSAGE_TEXT;
    IF position('reading result requires allowed quota' IN error_message) = 0 THEN
      RAISE;
    END IF;
    denied_rejected := true;
  END;
  IF NOT denied_rejected OR EXISTS (SELECT 1 FROM readings WHERE id = probe_denied_reading) THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: unauthorized reading insert was accepted';
  END IF;

  BEGIN
    UPDATE readings
       SET quota_state = 'denied'
     WHERE id = probe_allowed_reading;
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS error_message = MESSAGE_TEXT;
    IF position('reading result requires allowed quota' IN error_message) = 0 THEN
      RAISE;
    END IF;
    update_rejected := true;
  END;
  IF NOT update_rejected OR NOT EXISTS (SELECT 1 FROM readings WHERE id = probe_allowed_reading AND quota_state = 'allowed') THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: unauthorized reading update was accepted';
  END IF;

  INSERT INTO reading_authorization_receipts (reading_id, user_id, kind)
    VALUES (probe_allowed_reading, probe_user, 'legacy');
  BEGIN
    INSERT INTO reading_authorization_receipts (reading_id, user_id, kind)
      VALUES (probe_allowed_reading, probe_user, 'daily');
  EXCEPTION WHEN unique_violation THEN
    receipt_rejected := true;
  END;
  SELECT count(*) INTO receipt_count
    FROM reading_authorization_receipts
   WHERE reading_id = probe_allowed_reading;
  IF NOT receipt_rejected OR receipt_count <> 1 THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: receipt primary key did not reject a duplicate';
  END IF;

  INSERT INTO reading_quota_quarantine_034
    (reading_id, user_id, original_status, original_quota_state, original_interpretation)
    VALUES (probe_allowed_reading, probe_user, 'done', 'allowed', 'readiness-allowed-updated');
  BEGIN
    INSERT INTO reading_quota_quarantine_034
      (reading_id, user_id, original_status, original_quota_state, original_interpretation)
      VALUES (probe_allowed_reading, probe_user, 'done', 'allowed', 'readiness-duplicate');
  EXCEPTION WHEN unique_violation THEN
    quarantine_rejected := true;
  END;
  SELECT count(*) INTO quarantine_count
    FROM reading_quota_quarantine_034
   WHERE reading_id = probe_allowed_reading;
  IF NOT quarantine_rejected OR quarantine_count <> 1 THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: quarantine primary key did not reject a duplicate';
  END IF;

  INSERT INTO payments
    (id, user_id, plan_id, plan_code, price_rub_snapshot, amount_rub, stars,
     provider_payment_id, purchase_fingerprint, duration_days_snapshot)
    VALUES
      (probe_payment, probe_user, probe_plan, 'single_99', 99, 99, 99,
       'readiness-' || probe_user::text,
       encode(digest('single_99|99|99', 'sha256'), 'hex'), 1);
  BEGIN
    UPDATE payments
       SET price_rub_snapshot = 100
     WHERE id = probe_payment;
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS error_message = MESSAGE_TEXT;
    IF position('payment purchase snapshot is immutable' IN error_message) = 0 THEN
      RAISE;
    END IF;
    payment_rejected := true;
  END;
  SELECT price_rub_snapshot INTO probe_price FROM payments WHERE id = probe_payment;
  IF NOT payment_rejected OR probe_price <> 99 THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: payment snapshot update was accepted';
  END IF;

  BEGIN
    INSERT INTO payment_webhook_events
      (payment_id, event_hash, reason, currency, total_amount, telegram_charge_id, provider_charge_id, owner_tg_id)
    VALUES
      (probe_payment, repeat('g', 64), 'readiness', 'XTR', 1, 'readiness-invalid-hash', 'readiness-invalid-provider', 1);
  EXCEPTION WHEN check_violation THEN
    webhook_hash_rejected := true;
  END;
  IF NOT webhook_hash_rejected THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: malformed webhook hash was accepted';
  END IF;
  BEGIN
    INSERT INTO payment_webhook_events
      (payment_id, event_hash, reason, currency, total_amount, telegram_charge_id, provider_charge_id, owner_tg_id)
    VALUES
      (probe_payment, repeat('a', 63), 'readiness', 'XTR', 1, 'readiness-short-hash', 'readiness-short-provider', 1);
  EXCEPTION WHEN check_violation THEN
    webhook_short_hash_rejected := true;
  END;
  IF NOT webhook_short_hash_rejected THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: short webhook hash was accepted';
  END IF;

  INSERT INTO payment_webhook_events
    (payment_id, event_hash, reason, currency, total_amount, telegram_charge_id, provider_charge_id, owner_tg_id)
  VALUES
    (probe_payment, repeat('a', 64), 'readiness', 'XTR', 1, 'readiness-hash', 'readiness-hash-provider', 1);
  INSERT INTO payment_webhook_events
    (payment_id, event_hash, reason, currency, total_amount, telegram_charge_id, provider_charge_id, owner_tg_id)
  VALUES
    (probe_payment, repeat('a', 64), 'readiness-duplicate', 'XTR', 1, 'readiness-duplicate', 'readiness-duplicate-provider', 1)
  ON CONFLICT (payment_id, event_hash) DO NOTHING;
  SELECT count(*) INTO webhook_count
    FROM payment_webhook_events
   WHERE payment_id = probe_payment;
  IF webhook_count <> 1 THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: webhook uniqueness is not exactly payment_id,event_hash';
  END IF;

  INSERT INTO payments
    (id, user_id, plan_id, plan_code, price_rub_snapshot, amount_rub, stars,
     provider_payment_id, purchase_fingerprint, duration_days_snapshot)
  VALUES
    (probe_payment_two, probe_user, probe_plan, 'single_99', 99, 99, 99,
     'readiness-2-' || probe_user::text,
     encode(digest('single_99|99|99', 'sha256'), 'hex'), 1);
  BEGIN
    INSERT INTO payment_webhook_events
      (payment_id, event_hash, reason, currency, total_amount, telegram_charge_id, provider_charge_id, owner_tg_id)
    VALUES
      (probe_payment_two, repeat('a', 64), 'readiness-cross-payment', 'XTR', 1, 'readiness-cross', 'readiness-cross-provider', 1);
    webhook_cross_payment_accepted := true;
  EXCEPTION WHEN unique_violation THEN
    NULL;
  END;
  IF NOT webhook_cross_payment_accepted THEN
    RAISE EXCEPTION 'schema readiness behavior probe failed: webhook hash is unique across payments';
  END IF;
END
$probe$;
ROLLBACK;
SQL
  then
    fail 'migration readiness: behavioral probe failed'
  fi
}

check_behavior

active_admins="$(psql "$DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c "SELECT COUNT(*) FROM admin_accounts a JOIN users u ON u.id=a.user_id WHERE a.is_active AND u.role='admin' AND u.status='active'")"
if [ "$active_admins" -eq 0 ]; then
  printf '%s\n' "migration readiness: schema version $version is structurally clean"
  if [ "$SCHEMA_READINESS_MODE" = deploy ]; then
    fail 'admin readiness: active admin accounts: 0; provision an administrator before deployment'
  fi
  printf '%s\n' 'admin readiness: active admin accounts: 0 (provisioning required)'
  exit 0
fi
if [ "$SCHEMA_READINESS_MODE" = deploy ]; then
  printf 'deployment readiness: schema version %s is clean; active admin accounts: %s\n' "$version" "$active_admins"
else
  printf '%s\n' "migration readiness: schema version $version is structurally clean"
  printf 'admin readiness: active admin accounts: %s\n' "$active_admins"
fi
