// Package upkeep runs periodic maintenance in every worker process. Each
// duty takes a Redis lease for its interval, so running many workers does
// not multiply the work; if the lease holder dies the next interval runs
// elsewhere. Job execution recovery lives in the orchestrator's sweeper.
package upkeep

import (
	"context"
	"database/sql"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"aigc-platform/internal/application/creditsvc"
	"aigc-platform/internal/infra/orchestrator"
	"aigc-platform/internal/infra/storage"
	"aigc-platform/internal/pkg/logger"
)

// TrashRetentionDays is how long a deleted asset stays restorable.
const TrashRetentionDays = 30

type Runner struct {
	db      *sql.DB
	redis   *redis.Client
	credits *creditsvc.Service
	orch    *orchestrator.Orchestrator
	objects *storage.Store
	owner   string

	trashRetentionDays int
}

func New(db *sql.DB, rdb *redis.Client, credits *creditsvc.Service, orch *orchestrator.Orchestrator, objects *storage.Store, owner string) *Runner {
	return &Runner{db: db, redis: rdb, credits: credits, orch: orch, objects: objects, owner: owner, trashRetentionDays: TrashRetentionDays}
}

// WithTrashRetentionDays overrides the retention for verification.
func (r *Runner) WithTrashRetentionDays(days int) *Runner {
	r.trashRetentionDays = days
	return r
}

// Start launches every duty; they stop when ctx ends.
func (r *Runner) Start(ctx context.Context) {
	go r.loop(ctx, "trash-purge", time.Hour, r.autoPurgeTrash)
	go r.loop(ctx, "credit-reconciliation", 6*time.Hour, r.checkReconciliation)
	go r.loop(ctx, "event-retention", time.Hour, r.purgeEvents)
}

func (r *Runner) loop(ctx context.Context, name string, interval time.Duration, fn func(context.Context)) {
	run := func() {
		ok, err := r.redis.SetNX(ctx, "maint:"+name, r.owner, interval-time.Second).Result()
		if err != nil || !ok {
			return
		}
		fn(ctx)
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

// autoPurgeTrash permanently removes assets deleted longer ago than the
// retention, object first so a failure never orphans storage.
func (r *Runner) autoPurgeTrash(ctx context.Context) {
	log := logger.From(ctx)
	cutoff := time.Now().UTC().AddDate(0, 0, -r.trashRetentionDays)
	rows, err := r.db.QueryContext(ctx, `SELECT id, storage_key, thumb_key FROM assets WHERE deleted_at IS NOT NULL AND deleted_at < ? LIMIT 1000`, cutoff)
	if err != nil {
		log.Error("upkeep: query trash-eligible assets failed", zap.Error(err))
		return
	}
	type purgeable struct {
		id                   uint64
		storageKey, thumbKey string
	}
	var assets []purgeable
	for rows.Next() {
		var p purgeable
		if err := rows.Scan(&p.id, &p.storageKey, &p.thumbKey); err == nil {
			assets = append(assets, p)
		}
	}
	rows.Close()
	for _, a := range assets {
		if a.storageKey != "" {
			if err := r.objects.Delete(ctx, a.storageKey); err != nil {
				log.Error("upkeep: purge asset object failed", zap.Uint64("asset_id", a.id), zap.Error(err))
				continue
			}
		}
		if a.thumbKey != "" {
			if err := r.objects.Delete(ctx, a.thumbKey); err != nil {
				log.Error("upkeep: purge asset thumb failed", zap.Uint64("asset_id", a.id), zap.Error(err))
				continue
			}
		}
		if _, err := r.db.ExecContext(ctx, `DELETE FROM assets WHERE id = ?`, a.id); err != nil {
			log.Error("upkeep: delete purged asset row failed", zap.Uint64("asset_id", a.id), zap.Error(err))
		}
	}
}

// checkReconciliation logs every user whose credits break an invariant.
func (r *Runner) checkReconciliation(ctx context.Context) {
	log := logger.From(ctx)
	rows, err := r.db.QueryContext(ctx, `
		SELECT a.user_id, a.balance, a.held,
		       (SELECT COALESCE(SUM(amount), 0) FROM credit_ledger l WHERE l.user_id = a.user_id)
		FROM credit_accounts a
		WHERE a.balance + a.held <> (SELECT COALESCE(SUM(amount), 0) FROM credit_ledger l WHERE l.user_id = a.user_id)`)
	if err != nil {
		log.Error("upkeep: reconciliation query failed", zap.Error(err))
		return
	}
	for rows.Next() {
		var userID uint64
		var balance, held, ledger int
		if err := rows.Scan(&userID, &balance, &held, &ledger); err == nil {
			log.Error("upkeep: credit ledger mismatch", zap.Uint64("user_id", userID), zap.Int("balance", balance), zap.Int("held", held), zap.Int("ledger_sum", ledger))
		}
	}
	rows.Close()
	mismatches, err := r.credits.AuditHolds(ctx)
	if err != nil {
		log.Error("upkeep: reservation audit failed", zap.Error(err))
		return
	}
	for _, m := range mismatches {
		log.Error("upkeep: reservation mismatch", zap.Uint64("user_id", m.UserID), zap.Int("held", m.Held), zap.Int("open_holds", m.OpenHoldSum))
	}
}

func (r *Runner) purgeEvents(ctx context.Context) {
	if _, err := r.orch.PurgeEvents(ctx); err != nil {
		logger.From(ctx).Error("upkeep: purge events failed", zap.Error(err))
	}
}
