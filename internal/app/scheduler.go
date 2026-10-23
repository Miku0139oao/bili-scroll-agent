package app

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

func (a *App) Start(parent context.Context) {
	a.runMu.Lock()
	if a.runStop != nil {
		a.runMu.Unlock()
		return
	}
	a.runCtx, a.runStop = context.WithCancel(parent)
	ctx := a.runCtx
	a.runMu.Unlock()
	a.runWG.Add(1)
	go func() {
		defer a.runWG.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.scheduleTick(ctx)
			}
		}
	}()
}

func (a *App) scheduleTick(ctx context.Context) {
	admin := a.loadAdminSettings(ctx)
	var running int
	if err := a.Store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM runs WHERE status='running'`).Scan(&running); err != nil {
		return
	}
	if running >= admin.GlobalConcurrency {
		return
	}
	var userID, clock, timezone, nextRun string
	err := a.Store.DB.QueryRowContext(ctx, `
		SELECT user_id,local_time,timezone,next_run_at
		FROM schedules
		WHERE enabled=1 AND next_run_at IS NOT NULL AND next_run_at<=?
		ORDER BY next_run_at LIMIT 1`, store.Now()).
		Scan(&userID, &clock, &timezone, &nextRun)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return
	}
	nextAt, ok := store.ParseTime(nextRun)
	if !ok {
		_, _ = a.Store.DB.ExecContext(ctx,
			`UPDATE schedules SET next_run_at=NULL WHERE user_id=?`, userID)
		return
	}
	if nextAt.Before(time.Now().UTC().Add(-2 * time.Hour)) {
		_, _ = a.Store.DB.ExecContext(ctx, `
			UPDATE schedules SET next_run_at=?,updated_at=? WHERE user_id=?`,
			NextDaily(clock, timezone, time.Now().UTC()).Format(time.RFC3339Nano),
			store.Now(), userID)
		return
	}
	user, err := a.userByID(ctx, userID)
	if err != nil || user.Disabled {
		_, _ = a.Store.DB.ExecContext(ctx, `
			UPDATE schedules SET next_run_at=?,updated_at=? WHERE user_id=?`,
			NextDaily(clock, timezone, time.Now().UTC()).Format(time.RFC3339Nano),
			store.Now(), userID)
		return
	}
	run, token, err := a.createRun(ctx, user, "scheduled", nextAt)
	if err != nil {
		return
	}
	_, _ = a.Store.DB.ExecContext(ctx, `
		UPDATE schedules SET next_run_at=?,last_run_at=?,updated_at=? WHERE user_id=?`,
		NextDaily(clock, timezone, time.Now().UTC()).Format(time.RFC3339Nano),
		store.Now(), store.Now(), userID)
	a.scheduleRun(run, token, "")
}

func runStatus(run model.Run) string {
	return run.Status
}
