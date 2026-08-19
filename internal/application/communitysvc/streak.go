// Package communitysvc implements the daily-publish streak reward mechanic
// (product-confirmed rules): publishing at least one asset to the community
// on a calendar day extends a running streak; missing a day resets it to 0.
// Reaching 3/10/30 consecutive days grants a one-time credit bonus
// (10/50/100) — "one-time" per occurrence, where re-earning the 3-day bonus
// specifically requires the streak to have broken and been rebuilt from
// scratch (an unbroken 30-day streak crosses day 3 exactly once, not every
// 3rd day — RecordPublish's exact-equality check on current_streak is what
// gives this for free: once past 3 without a break, current_streak keeps
// climbing and never equals 3 again until a reset). Both the 3-day and
// 10-day bonuses are additionally capped per calendar month (4x and 1x) to
// bound how much an aggressive break-and-restart pattern can farm; 30-day
// is left uncapped since crossing it twice inside one calendar month isn't
// reachable in practice.
package communitysvc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"aigc-platform/internal/application/creditsvc"
)

type streakMilestone struct {
	days       int
	credits    int
	monthlyCap int // 0 = uncapped
}

var streakMilestones = []streakMilestone{
	{days: 3, credits: 10, monthlyCap: 4},
	{days: 10, credits: 50, monthlyCap: 1},
	{days: 30, credits: 100, monthlyCap: 0},
}

type Service struct {
	db      *sql.DB
	credits *creditsvc.Service
}

func New(db *sql.DB, credits *creditsvc.Service) *Service {
	return &Service{db: db, credits: credits}
}

// Awarded is one milestone bonus granted by a single RecordPublish call.
type Awarded struct {
	Days    int
	Credits int
}

// Status is GET /community/streak's own read model — current_streak plus
// enough about each milestone's this-month usage for the frontend to show
// "3/4 used this month" instead of just a number.
type Status struct {
	CurrentStreak int
	Milestones    []MilestoneStatus
}

type MilestoneStatus struct {
	Days       int
	Credits    int
	MonthlyCap int // 0 = uncapped
	UsedThisMonth int
}

// RecordPublish is called once per successful "publish to community"
// action (handleUpdateAsset, is_public: true → true transition included —
// this is idempotent per calendar day regardless of how many times it's
// called). Returns the milestone(s) just crossed, if any; almost always
// empty or a single element, since current_streak only ever advances by 1
// per call.
func (s *Service) RecordPublish(ctx context.Context, userID uint64) ([]Awarded, error) {
	today, yesterday := todayAndYesterday()

	var awarded []Awarded
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var currentStreak int
		var lastPublish sql.NullTime
		err := tx.QueryRowContext(ctx,
			`SELECT current_streak, last_publish_date FROM community_streaks WHERE user_id = ? FOR UPDATE`,
			userID).Scan(&currentStreak, &lastPublish)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read community_streaks: %w", err)
		}
		lastPublishDate := ""
		if lastPublish.Valid {
			lastPublishDate = lastPublish.Time.Format("2006-01-02")
		}

		if lastPublishDate == today {
			return nil // already counted today — no-op
		}
		if lastPublishDate == yesterday {
			currentStreak++
		} else {
			currentStreak = 1 // first publish ever, or the streak broke
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO community_streaks (user_id, current_streak, last_publish_date)
			VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE current_streak = VALUES(current_streak), last_publish_date = VALUES(last_publish_date)`,
			userID, currentStreak, today); err != nil {
			return fmt.Errorf("upsert community_streaks: %w", err)
		}

		for _, m := range streakMilestones {
			if currentStreak != m.days {
				continue
			}
			if m.monthlyCap > 0 {
				var count int
				if err := tx.QueryRowContext(ctx, `
					SELECT COUNT(*) FROM community_streak_rewards
					WHERE user_id = ? AND milestone = ? AND awarded_date >= DATE_FORMAT(?, '%Y-%m-01')`,
					userID, m.days, today).Scan(&count); err != nil {
					return fmt.Errorf("count community_streak_rewards: %w", err)
				}
				if count >= m.monthlyCap {
					continue
				}
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO community_streak_rewards (user_id, milestone, credits, awarded_date) VALUES (?, ?, ?, ?)`,
				userID, m.days, m.credits, today); err != nil {
				return fmt.Errorf("insert community_streak_rewards: %w", err)
			}
			awarded = append(awarded, Awarded{Days: m.days, Credits: m.credits})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Crediting happens after the streak tx commits, one creditsvc call per
	// milestone crossed — each already idempotent on its own
	// "community_streak:{userID}:{days}:{date}" key, so a caller retrying
	// RecordPublish after a partial failure here can't double-grant even
	// though this step isn't in the same transaction as the audit row above.
	for _, a := range awarded {
		idemKey := fmt.Sprintf("community_streak:%d:%d:%s", userID, a.Days, today)
		if err := s.credits.GrantStreak(ctx, userID, idemKey, a.Days, a.Credits); err != nil {
			return awarded, fmt.Errorf("grant streak bonus (%d days): %w", a.Days, err)
		}
	}
	return awarded, nil
}

// GetStatus is handleCommunityStreak's read side — current streak length
// plus each milestone's this-calendar-month usage, so the frontend can show
// progress ("已连续发布 5 天", "本月还可再拿 2 次3天奖励") without the
// client having to know the reward table's own cap rules.
func (s *Service) GetStatus(ctx context.Context, userID uint64) (Status, error) {
	today, yesterday := todayAndYesterday()

	var currentStreak int
	var lastPublish sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT current_streak, last_publish_date FROM community_streaks WHERE user_id = ?`, userID).
		Scan(&currentStreak, &lastPublish)
	if err != nil && err != sql.ErrNoRows {
		return Status{}, fmt.Errorf("read community_streaks: %w", err)
	}
	// A streak displayed to the user has to reflect "today" even before
	// they've published anything today — GetStatus is a pure read, so it
	// can't upsert the way RecordPublish does. If the last publish was
	// neither today nor yesterday, the streak has already lapsed even
	// though the row hasn't been touched since the last publish.
	if lastPublish.Valid {
		d := lastPublish.Time.Format("2006-01-02")
		if d != today && d != yesterday {
			currentStreak = 0
		}
	}

	out := Status{CurrentStreak: currentStreak}
	for _, m := range streakMilestones {
		used := 0
		if m.monthlyCap > 0 {
			if err := s.db.QueryRowContext(ctx, `
				SELECT COUNT(*) FROM community_streak_rewards
				WHERE user_id = ? AND milestone = ? AND awarded_date >= DATE_FORMAT(?, '%Y-%m-01')`,
				userID, m.days, today).Scan(&used); err != nil {
				return Status{}, fmt.Errorf("count community_streak_rewards: %w", err)
			}
		}
		out.Milestones = append(out.Milestones, MilestoneStatus{
			Days: m.days, Credits: m.credits, MonthlyCap: m.monthlyCap, UsedThisMonth: used,
		})
	}
	return out, nil
}

// todayAndYesterday returns both dates as "2006-01-02" strings, computed in
// UTC to match config.MySQLDSN()'s "loc=UTC" — the driver interprets every
// DATE column (last_publish_date, awarded_date) as UTC, so the "calendar
// day" a streak advances on has to use that same basis rather than the
// process's local time zone, or a publish near local midnight could
// straddle two different "today"s between this and what's stored.
func todayAndYesterday() (today, yesterday string) {
	now := time.Now().UTC()
	return now.Format("2006-01-02"), now.AddDate(0, 0, -1).Format("2006-01-02")
}

func (s *Service) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
