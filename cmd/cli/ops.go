package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hibiken/asynq"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/application/media"
	"aigc-platform/internal/infra/cache"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/pkg/config"
)

func openDB() *sql.DB {
	db, err := sql.Open("mysql", config.MySQLDSN())
	if err != nil {
		fmt.Fprintf(os.Stderr, "open mysql: %v\n", err)
		os.Exit(1)
	}
	return db
}

func cmdBackfillThumbnails(args []string) {
	fs := flag.NewFlagSet("backfill-thumbnails", flag.ExitOnError)
	limit := fs.Int("limit", 10000, "maximum assets to queue")
	_ = fs.Parse(args)
	db := openDB()
	defer db.Close()
	client := asynq.NewClient(cache.AsynqRedisOpt(config.RedisAddr(), config.RedisURL()))
	defer client.Close()
	dispatcher := orchestrator.NewAsynqDispatcher(client)

	rows, err := db.Query(`SELECT biz_id FROM assets WHERE thumb_url = '' AND deleted_at IS NULL AND type IN ('image', 'video') ORDER BY id DESC LIMIT ?`, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query assets: %v\n", err)
		os.Exit(1)
	}
	defer rows.Close()
	queued := 0
	ctx := context.Background()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil && dispatcher.Enqueue(ctx, media.ThumbnailTask(id)) == nil {
			queued++
		}
	}
	fmt.Printf("queued %d thumbnails\n", queued)
}

// cmdAudit reports every broken invariant. Suitable for a periodic job.
func cmdAudit(_ []string) {
	db := openDB()
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	violations := 0
	report := func(format string, args ...any) {
		violations++
		fmt.Printf("VIOLATION: "+format+"\n", args...)
	}

	rows, err := db.QueryContext(ctx, `SELECT a.user_id, a.balance, a.held, COALESCE((SELECT SUM(amount) FROM credit_ledger l WHERE l.user_id = a.user_id), 0) AS ledger
		FROM credit_accounts a`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query accounts: %v\n", err)
		os.Exit(2)
	}
	for rows.Next() {
		var uid uint64
		var balance, held, ledger int
		if rows.Scan(&uid, &balance, &held, &ledger) == nil && balance+held != ledger {
			report("user %d balance %d + held %d != ledger %d", uid, balance, held, ledger)
		}
	}
	rows.Close()

	mismatches, err := creditsvc.New(db).AuditHolds(ctx)
	if err == nil {
		for _, m := range mismatches {
			if m.Held != m.OpenHoldSum {
				report("user %d held %d != open reservations %d", m.UserID, m.Held, m.OpenHoldSum)
			}
		}
	}

	checks := []struct {
		name, query string
	}{
		{"finished job with an open reservation", `SELECT j.biz_id FROM credit_holds h JOIN jobs j ON j.id = h.job_id
			WHERE h.status = 'open' AND j.status IN ('succeeded', 'partial', 'failed', 'cancelled')`},
		{"live job with nothing that can advance it", `SELECT j.biz_id FROM jobs j WHERE j.engine = 'v2'
			AND j.status IN ('queued', 'running', 'awaiting_review', 'cancelling')
			AND NOT EXISTS (SELECT 1 FROM job_nodes n WHERE n.job_id = j.id AND n.status IN ('ready', 'running', 'waiting', 'suspended', 'pending'))`},
		{"provider call started over an hour ago and never finished", `SELECT CAST(id AS CHAR) FROM provider_calls
			WHERE status = 'started' AND started_at < UTC_TIMESTAMP(3) - INTERVAL 1 HOUR`},
	}
	for _, c := range checks {
		rows, err := db.QueryContext(ctx, c.query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", c.name, err)
			os.Exit(2)
		}
		for rows.Next() {
			var ref string
			if rows.Scan(&ref) == nil {
				report("%s: %s", c.name, ref)
			}
		}
		rows.Close()
	}
	if violations > 0 {
		fmt.Printf("%d violation(s)\n", violations)
		os.Exit(1)
	}
	fmt.Println("audit passed")
}
