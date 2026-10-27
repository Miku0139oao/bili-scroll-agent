package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/runner"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

func (a *App) loadConfig(ctx context.Context, userID string) (model.MemberConfig, error) {
	config := model.DefaultMemberConfig()
	var payload string
	err := a.Store.DB.QueryRowContext(ctx,
		`SELECT payload FROM user_configs WHERE user_id=?`, userID).Scan(&payload)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return config, err
	}
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &config); err != nil {
			return config, err
		}
	}
	if config.Runner == "" {
		config.Runner = "codex"
	}
	if config.Keywords == nil {
		config.Keywords = []string{}
	}
	if config.PerDay < 0 {
		config.PerDay = 0
	}
	if !validTimezone(config.Schedule.Timezone) {
		config.Schedule.Timezone = "Asia/Shanghai"
	}
	if !validClock(config.Schedule.Time) {
		config.Schedule.Time = "20:00"
	}
	return config, nil
}

func (a *App) saveConfig(ctx context.Context, userID string, patch map[string]any) (model.MemberConfig, error) {
	current, err := a.loadConfig(ctx, userID)
	if err != nil {
		return current, err
	}
	raw := marshalString(current)
	var merged map[string]any
	if err := json.Unmarshal([]byte(raw), &merged); err != nil {
		return current, err
	}
	for key, value := range patch {
		if key == "schedule" {
			var schedulePatch map[string]any
			if rawSchedule, ok := value.(map[string]any); ok {
				schedulePatch = rawSchedule
			} else {
				encoded, _ := json.Marshal(value)
				_ = json.Unmarshal(encoded, &schedulePatch)
			}
			if merged["schedule"] == nil {
				merged["schedule"] = map[string]any{}
			}
			schedule := merged["schedule"].(map[string]any)
			for childKey, childValue := range schedulePatch {
				schedule[childKey] = childValue
			}
			continue
		}
		merged[key] = value
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return current, err
	}
	var next model.MemberConfig
	if err := json.Unmarshal(encoded, &next); err != nil {
		return current, err
	}
	if _, ok := runner.Lookup(next.Runner); !ok {
		return current, errors.New("runner must be codex or hermes")
	}
	if next.PerDay < 0 || next.PerDay > 50 {
		return current, errors.New("perDay must be between 0 and 50")
	}
	if !validTimezone(next.Schedule.Timezone) {
		return current, errors.New("unknown timezone")
	}
	if !validClock(next.Schedule.Time) {
		return current, errors.New("schedule time must be HH:MM")
	}
	if next.DMMode != "off" && next.DMMode != "only" && next.DMMode != "both" {
		return current, errors.New("invalid DM mode")
	}
	now := time.Now().UTC()
	err = a.Store.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`
			INSERT INTO user_configs(user_id,payload,updated_at) VALUES(?,?,?)
			ON CONFLICT(user_id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`,
			userID, store.JSON(next), now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		nextRun := any(nil)
		if next.Schedule.Enabled {
			nextRun = NextDaily(next.Schedule.Time, next.Schedule.Timezone, now).Format(time.RFC3339Nano)
		}
		_, err = tx.Exec(`
			INSERT INTO schedules(user_id,enabled,local_time,timezone,next_run_at,updated_at)
			VALUES(?,?,?,?,?,?)
			ON CONFLICT(user_id) DO UPDATE SET
				enabled=excluded.enabled,local_time=excluded.local_time,timezone=excluded.timezone,
				next_run_at=excluded.next_run_at,updated_at=excluded.updated_at`,
			userID, boolInt(next.Schedule.Enabled), next.Schedule.Time, next.Schedule.Timezone,
			nextRun, now.Format(time.RFC3339Nano))
		return err
	})
	return next, err
}

func (a *App) loadAdminSettings(ctx context.Context) model.AdminSettings {
	settings := model.DefaultAdminSettings()
	settings.ManualRunsPerDay = a.Settings.ManualRunsPerDay
	settings.ScheduledRunsPerDay = a.Settings.ScheduledRunsPerDay
	settings.GlobalConcurrency = a.Settings.GlobalConcurrency
	settings.RetentionDays = a.Settings.RetentionDays
	settings.OutboundEnabled = a.Settings.BindOutboundEnabled
	var payload string
	if err := a.Store.DB.QueryRowContext(ctx,
		`SELECT value FROM system_settings WHERE key='admin'`).Scan(&payload); err == nil {
		_ = json.Unmarshal([]byte(payload), &settings)
	}
	settings.Normalize()
	if settings.GlobalConcurrency < 1 {
		settings.GlobalConcurrency = 1
	}
	return settings
}

func (a *App) saveAdminSettings(ctx context.Context, patch map[string]any) (model.AdminSettings, error) {
	current := a.loadAdminSettings(ctx)
	raw := marshalString(current)
	var merged map[string]any
	if err := json.Unmarshal([]byte(raw), &merged); err != nil {
		return current, err
	}
	for key, value := range patch {
		if value != nil {
			merged[key] = value
		}
	}
	encoded, _ := json.Marshal(merged)
	var next model.AdminSettings
	if err := json.Unmarshal(encoded, &next); err != nil {
		return current, err
	}
	if next.GlobalConcurrency < 1 || next.GlobalConcurrency > 8 {
		return current, errors.New("global concurrency must be between 1 and 8")
	}
	next.Normalize()
	next.SyncLegacy()
	_, err := a.Store.DB.ExecContext(ctx, `
		INSERT INTO system_settings(key,value,updated_at) VALUES('admin',?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`,
		store.JSON(next), store.Now())
	return next, err
}

func NextDaily(clock, timezone string, after time.Time) time.Time {
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location = time.UTC
	}
	localNow := after.In(location)
	var hour, minute int
	_, _ = fmt.Sscanf(clock, "%d:%d", &hour, &minute)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC()
}

func LocalDay(config model.MemberConfig, now time.Time) string {
	location, err := time.LoadLocation(config.Schedule.Timezone)
	if err != nil {
		location = time.UTC
	}
	return now.In(location).Format("2006-01-02")
}

func validTimezone(value string) bool {
	_, err := time.LoadLocation(strings.TrimSpace(value))
	return err == nil
}

func validClock(value string) bool {
	_, err := time.Parse("15:04", value)
	return err == nil
}
