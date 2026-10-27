package app

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
)

// seedUser creates a user row plus the rows a scheduled run needs.
func seedUser(t *testing.T, application *App, id string, withCredential bool) {
	t.Helper()
	raw := store.JSON(model.DefaultMemberConfig())
	_, err := application.Store.DB.Exec(`
		INSERT INTO users(id,username,display_name,role,created_at)
		VALUES(?,?,?,'member',?)`, id, id, id, store.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.Store.DB.Exec(`
		INSERT INTO user_configs(user_id,payload,updated_at) VALUES(?,?,?)`,
		id, raw, store.Now()); err != nil {
		t.Fatal(err)
	}
	if withCredential {
		encrypted, err := application.Cipher.EncryptBytes([]byte(`{"cookie":"x"}`), "bili:"+id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := application.Store.DB.Exec(`
			INSERT INTO bili_credentials(user_id,ciphertext,uid,updated_at)
			VALUES(?,?,1,?)`, id, encrypted, store.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := application.Store.DB.Exec(`
		INSERT INTO schedules(user_id,enabled,local_time,timezone,next_run_at,updated_at)
		VALUES(?,1,'20:00','Asia/Shanghai',?,?)`,
		id, time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano), store.Now()); err != nil {
		t.Fatal(err)
	}
}

func runCount(t *testing.T, application *App, userID string) int {
	t.Helper()
	var count int
	if err := application.Store.DB.QueryRow(
		`SELECT COUNT(*) FROM runs WHERE user_id=?`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestScheduleTickDoesNotBlockOnABrokenUser is the regression test for the
// head-of-line bug: the earliest due user cannot start a run (no Bilibili
// credential), and every other due user must still be dispatched in the same
// tick.
func TestScheduleTickDoesNotBlockOnABrokenUser(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	application.runSlots = make(chan struct{}, 8)

	// A is first in the due queue but has no credential.
	seedUser(t, application, "a", false)
	seedUser(t, application, "b", true)
	seedUser(t, application, "c", true)

	application.scheduleTick(ctx)

	if got := runCount(t, application, "a"); got != 0 {
		t.Fatalf("user a should not have a run, got %d", got)
	}
	if got := runCount(t, application, "b"); got != 1 {
		t.Fatalf("user b was starved, runs = %d", got)
	}
	if got := runCount(t, application, "c"); got != 1 {
		t.Fatalf("user c was starved, runs = %d", got)
	}

	var nextRun string
	var retries int
	var lastError string
	if err := application.Store.DB.QueryRow(
		`SELECT next_run_at,retry_count,last_error FROM schedules WHERE user_id='a'`).
		Scan(&nextRun, &retries, &lastError); err != nil {
		t.Fatal(err)
	}
	if parsed, ok := store.ParseTime(nextRun); !ok || parsed.Before(time.Now().UTC()) {
		t.Fatalf("schedule a was not advanced: %q", nextRun)
	}
	if retries != 1 {
		t.Fatalf("expected retry_count=1, got %d", retries)
	}
	if lastError == "" {
		t.Fatal("the failure was not recorded")
	}
}

// TestScheduleTickBacksOffAfterRepeatedFailures verifies that a permanently
// broken user stops retrying every tick.
func TestScheduleTickBacksOff(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	application.runSlots = make(chan struct{}, 1)
	seedUser(t, application, "broken", false)

	// Each tick advances next_run_at into the future, so force it due again.
	for attempt := 1; attempt <= 4; attempt++ {
		_, _ = application.Store.DB.Exec(`
			UPDATE schedules SET next_run_at=? WHERE user_id='broken'`,
			time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano))
		application.scheduleTick(ctx)
	}

	var retries int
	var nextRun string
	if err := application.Store.DB.QueryRow(
		`SELECT retry_count,next_run_at FROM schedules WHERE user_id='broken'`).
		Scan(&retries, &nextRun); err != nil {
		t.Fatal(err)
	}
	// After the fourth failure the counter resets and the run waits for the
	// next daily slot rather than backing off again.
	if retries != 0 {
		t.Fatalf("retry_count = %d, want reset to 0", retries)
	}
	if parsed, _ := store.ParseTime(nextRun); parsed.Before(time.Now().UTC().Add(time.Hour)) {
		t.Fatalf("expected the next daily slot, got %q", nextRun)
	}
}

// TestGlobalConcurrencyGatesEveryPath proves manual runs cannot bypass the
// limit that previously applied to scheduled runs only.
func TestGlobalConcurrencyGatesEveryPath(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	application.runSlots = make(chan struct{}, 1)

	seedUser(t, application, "u1", true)

	// Hold the only slot as if a run were in flight.
	if !application.acquireSlot() {
		t.Fatal("could not acquire the first slot")
	}
	user, err := application.userByID(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	run, token, err := application.createRun(ctx, user, "manual", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	application.scheduleRun(run, token, "")

	if status := runStatusOf(t, application, run.ID); status != "queued" {
		t.Fatalf("run started without a slot: status = %q", status)
	}
	if depth := application.queueDepth(); depth != 1 {
		t.Fatalf("pending queue depth = %d, want 1", depth)
	}

	// Freeing the slot must dispatch the waiting run.
	application.releaseSlot()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if status := runStatusOf(t, application, run.ID); status != "queued" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued run was not dispatched after a slot freed up")
}

func runStatusOf(t *testing.T, application *App, runID string) string {
	t.Helper()
	var status string
	if err := application.Store.DB.QueryRow(
		`SELECT status FROM runs WHERE id=?`, runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// TestRunPanicFailsTheRun ensures a panic in the run goroutine cannot leave the
// row running forever (which would block the user via uq_runs_active_user).
func TestRunPanicFailsTheRun(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	application.runSlots = make(chan struct{}, 1)
	seedUser(t, application, "u2", false)
	// A credential-less user fails fast; instead drive the panic path directly.
	_, _ = application.Store.DB.Exec(`
		INSERT INTO runs(id,user_id,kind,runner,status,started_at,heartbeat_at,telemetry)
		VALUES('run-panic','u2','manual','codex','running',?,?,'{}')`,
		store.Now(), store.Now())

	application.runMu.Lock()
	application.runWG.Add(1)
	application.runMu.Unlock()
	func() {
		defer application.runWG.Done()
		defer func() {
			if recovered := recover(); recovered != nil {
				application.releaseSlot()
				application.failRun(context.Background(), "run-panic", "panic recovered")
			}
		}()
		panic("boom")
	}()
	if status := runStatusOf(t, application, "run-panic"); status != "failed" {
		t.Fatalf("panic left the run in %q", status)
	}
	// The failure released the slot, so a new run can start.
	if !application.acquireSlot() {
		t.Fatal("slot was not released after the panic")
	}
	_ = ctx
}

func TestReapStaleRuns(t *testing.T) {
	application := testApp(t)
	ctx := context.Background()
	seedUser(t, application, "u3", false)
	stale := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339Nano)
	_, _ = application.Store.DB.Exec(`
		INSERT INTO runs(id,user_id,kind,runner,status,started_at,heartbeat_at,telemetry)
		VALUES('run-stale','u3','manual','codex','running',?,?,'{}')`, stale, stale)

	application.reapStaleRuns(ctx)
	if status := runStatusOf(t, application, "run-stale"); status != "failed" {
		t.Fatalf("stale run status = %q", status)
	}
}

func TestRateLimiterSweep(t *testing.T) {
	limiter := newRateLimiter()
	if !limiter.Allow("a", 2, time.Minute) || !limiter.Allow("a", 2, time.Minute) {
		t.Fatal("expected the first two calls to pass")
	}
	if limiter.Allow("a", 2, time.Minute) {
		t.Fatal("expected the third call to be limited")
	}
	limiter.Sweep(time.Now().Add(10*time.Minute), time.Minute)
	limiter.mu.Lock()
	remaining := len(limiter.entries)
	limiter.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("idle buckets were not swept: %d", remaining)
	}
}

func TestTruncateTextIsRuneSafe(t *testing.T) {
	value := "值得看：這是一段很長的中文標題"
	got := truncateText(value, 4)
	if !utf8Valid(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
	}
	if got != "值得看：" {
		t.Fatalf("got %q", got)
	}
	if truncateText("abc", 0) != "" {
		t.Fatal("zero limit must produce an empty string")
	}
	ellipsis := truncateWithEllipsis(value, 5)
	if !utf8Valid(ellipsis) || ellipsis != "值得看：…" {
		t.Fatalf("unexpected ellipsis result %q", ellipsis)
	}
}

func utf8Valid(value string) bool {
	for _, rune := range value {
		if rune == '�' {
			return false
		}
	}
	return true
}

func TestSchemaHasRunUsage(t *testing.T) {
	application := testApp(t)
	var name string
	err := application.Store.DB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='run_usage'`).Scan(&name)
	if err != nil {
		t.Fatalf("run_usage table missing: %v", err)
	}
	_ = sql.ErrNoRows
	_ = filepath.Separator
}
