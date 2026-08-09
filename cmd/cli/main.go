// cmd/cli is F1.6's "CLI 手工发放积分" — the only credit-recharge path this
// POC needs (no payment integration in scope). Connects directly to MySQL;
// deliberately doesn't go through cmd/api since granting credits is an
// operator action, not a user-facing endpoint.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/infra/persistence"
	"aigc-platform/internal/pkg/config"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "grant-credits":
		cmdGrantCredits(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cli grant-credits -email=<email> -amount=<n> [-remark=<text>]")
}

func cmdGrantCredits(args []string) {
	fs := flag.NewFlagSet("grant-credits", flag.ExitOnError)
	email := fs.String("email", "", "user's login email")
	amount := fs.Int("amount", 0, "credits to grant (must be positive)")
	remark := fs.String("remark", "manual grant via cli", "audit remark stored in credit_ledger")
	_ = fs.Parse(args)

	if *email == "" || *amount <= 0 {
		usage()
		os.Exit(1)
	}

	db, err := persistence.Open(persistence.Config{DSN: config.MySQLDSN()})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open mysql: %v\n", err)
		os.Exit(1)
	}
	sqlDB, err := db.DB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "get sql.DB: %v\n", err)
		os.Exit(1)
	}

	var userID uint64
	if err := sqlDB.QueryRow(`SELECT id FROM users WHERE email = ?`, *email).Scan(&userID); err != nil {
		fmt.Fprintf(os.Stderr, "look up user %q: %v\n", *email, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	credits := creditsvc.New(sqlDB)
	// idem_key includes a timestamp: unlike hold/commit/refund (naturally
	// idempotent by job/task identity), a manual grant has no natural
	// dedupe key of its own — two genuinely separate grants to the same
	// user for the same amount must both go through, not collide.
	idemKey := fmt.Sprintf("recharge:%s:%d:%d", *email, *amount, time.Now().UnixNano())
	if err := credits.Recharge(ctx, userID, idemKey, *amount, *remark); err != nil {
		fmt.Fprintf(os.Stderr, "recharge: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("granted %d credits to %s (user_id=%d)\n", *amount, *email, userID)
}
