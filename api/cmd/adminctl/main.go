package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"taro/api/internal/admin"
	"taro/api/internal/store"
)

const (
	adminProvisionAction      = "admin:provision"
	adminPasswordRotateAction = "admin:password:rotate"
	adminReactivateAction     = "admin:reactivate"
)

func main() {
	usernameFlag := flag.String("username", "", "admin username")
	userIDFlag := flag.String("user-id", "00000000-0000-0000-0000-000000000001", "admin user UUID")
	flag.Parse()
	if !admin.ValidateUsername(*usernameFlag) {
		fatal("invalid username")
	}
	userID := strings.TrimSpace(*userIDFlag)
	if userID == "" {
		fatal("user-id is required")
	}
	if !admin.ValidUserID(userID) {
		fatal("invalid user-id")
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1024))
	if err != nil {
		fatal("read password: %v", err)
	}
	password := strings.TrimSuffix(string(raw), "\n")
	password = strings.TrimSuffix(password, "\r")
	hash, err := admin.HashPassword(password)
	if err != nil {
		fatal("invalid password: %v", err)
	}
	username := admin.NormalizeUsername(*usernameFlag)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		fatal("database: %v", err)
	}
	defer pg.Close()
	rd := store.ConnectRedis()
	defer func() { _ = rd.Close() }()
	if err := provision(ctx, pg, userID, username, hash); err != nil {
		fatal("provision: %v", err)
	}
	if err := revokeAdminSessions(ctx, rd, userID); err != nil {
		fmt.Fprintf(os.Stderr, "admin account %s committed; session cleanup pending: %v\n", username, err)
		return
	}
	fmt.Printf("admin account %s provisioned\n", username)
}

func revokeAdminSessions(ctx context.Context, rd *redis.Client, userID string) error {
	if rd == nil {
		return errors.New("redis unavailable")
	}
	if !admin.ValidUserID(userID) {
		return errors.New("invalid user-id")
	}
	if err := rd.Del(ctx, "sess:admin:"+userID).Err(); err != nil {
		return err
	}
	pattern := "sess:admin:" + userID + ":*"
	var cursor uint64
	for {
		keys, next, err := rd.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := rd.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func writeAdminAudit(ctx context.Context, tx pgx.Tx, userID, username, action string) error {
	diff, err := json.Marshal(map[string]string{"username": username})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_audit (admin_id, action, diff) VALUES ($1,$2,$3)`, userID, action, diff)
	return err
}

func provision(ctx context.Context, db interface {
	Begin(context.Context) (pgx.Tx, error)
}, userID, username, hash string) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var role, status string
	if err := tx.QueryRow(ctx, `SELECT role, status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&role, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("admin user does not exist")
		}
		return err
	}
	if role != "admin" {
		return errors.New("user is not an admin")
	}
	if status != "active" {
		return errors.New("admin user is not active")
	}
	var currentUsername string
	var currentActive bool
	err = tx.QueryRow(ctx, `SELECT username, is_active FROM admin_accounts WHERE user_id=$1 FOR UPDATE`, userID).Scan(&currentUsername, &currentActive)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO admin_accounts (user_id, username, password_hash)
			VALUES ($1, $2, $3)`, userID, username, hash); err != nil {
			return err
		}
		if err := writeAdminAudit(ctx, tx, userID, username, adminProvisionAction); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if currentUsername != username {
		return errors.New("username is already assigned to another account")
	}
	if _, err := tx.Exec(ctx, `
		UPDATE admin_accounts
		SET password_hash=$1, is_active=true, session_version=session_version+1, updated_at=now()
		WHERE user_id=$2`, hash, userID); err != nil {
		return err
	}
	action := adminPasswordRotateAction
	if !currentActive {
		action = adminReactivateAction
	}
	if err := writeAdminAudit(ctx, tx, userID, username, action); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func fatal(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
