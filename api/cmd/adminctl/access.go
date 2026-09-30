package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"taro/api/internal/access"
	"taro/api/internal/store"
)

// accessCmd — выдача/отзыв безлимита владельцу и тем, кто тестирует за него.
//
// Безлимит — активная подписка (internal/access), а не флаг роли. Роль
// admin в лимитах не участвует, поэтому «сделай меня админом» тут не помогает.
//
//	adminctl access grant  <user-id> [--days N] [--json]
//	adminctl access revoke <user-id> [--json]
//	adminctl access list   [--json]
func accessCmd(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "usage: adminctl access <grant|revoke|list> [flags]\n")
		return 1
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("access "+sub, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	days := fs.Int("days", access.DefaultDays, "срок доступа в днях (grant)")
	asJSON := fs.Bool("json", false, "machine readable output")
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	if sub == "list" {
		if fs.NArg() > 0 {
			fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", fs.Arg(0))
			return 1
		}
		return accessList(ctx, *asJSON)
	}
	if fs.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: adminctl access %s <user-id>\n", sub)
		return 1
	}
	userID := fs.Arg(0)
	if sub == "grant" {
		return accessGrant(ctx, userID, *days, *asJSON)
	}
	if sub == "revoke" {
		return accessRevoke(ctx, userID, *asJSON)
	}
	fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", sub)
	return 1
}

func accessGrant(ctx context.Context, userID string, days int, asJSON bool) int {
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		fatal("db: %v", err)
	}
	defer pg.Close()
	until, err := access.Grant(ctx, pg, userID, days)
	switch err {
	case access.ErrNoUser:
		fmt.Fprintf(os.Stderr, "пользователь %s не найден (проверь user_id, не anon_uuid)\n", userID)
		return 1
	case access.ErrNoPlan:
		fmt.Fprintf(os.Stderr, "в plans нет кода %s — seeds не применены\n", access.DefaultPlanCode)
		return 1
	case nil:
	default:
		fatal("grant: %v", err)
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"user_id": userID, "valid_until": until})
		return 0
	}
	fmt.Printf("безлимит выдан: %s\nдействует до %s\n", userID, until.Format("2006-01-02"))
	return 0
}

func accessRevoke(ctx context.Context, userID string, asJSON bool) int {
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		fatal("db: %v", err)
	}
	defer pg.Close()
	n, err := access.Revoke(ctx, pg, userID)
	if err != nil {
		fatal("revoke: %v", err)
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"user_id": userID, "revoked": n})
		return 0
	}
	if n == 0 {
		fmt.Printf("у %s не было безлимита\n", userID)
		return 0
	}
	fmt.Printf("безлимит отозван: %s (строк: %d)\n", userID, n)
	return 0
}

func accessList(ctx context.Context, asJSON bool) int {
	pg, err := store.ConnectPG(ctx)
	if err != nil {
		fatal("db: %v", err)
	}
	defer pg.Close()
	entries, err := access.List(ctx, pg)
	if err != nil {
		fatal("list: %v", err)
	}
	if asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(entries)
		return 0
	}
	if len(entries) == 0 {
		fmt.Println("ручного доступа нет")
		return 0
	}
	fmt.Println("ручной доступ:")
	for _, e := range entries {
		fmt.Println(" ", e.Describe())
	}
	return 0
}
