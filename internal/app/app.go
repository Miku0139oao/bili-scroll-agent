package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/bilibili"
	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
	"github.com/Miku0139oao/bili-scroll-agent/internal/provider"
	"github.com/Miku0139oao/bili-scroll-agent/internal/security"
	"github.com/Miku0139oao/bili-scroll-agent/internal/store"
	"github.com/Miku0139oao/bili-scroll-agent/internal/version"
	"github.com/Miku0139oao/bili-scroll-agent/internal/webui"
)

type App struct {
	Settings  *config.Settings
	Store     *store.Store
	Cipher    *security.Cipher
	Mux       *http.ServeMux
	Logger    *slog.Logger
	limiter   *rateLimiter
	broker    *loginBroker
	events    *eventHub
	Bili      *bilibili.Client
	Providers *provider.Registry
	WebFS     fs.FS
	runMu     sync.Mutex
	runTasks  map[string]context.CancelFunc
	runWG     sync.WaitGroup
	runCtx    context.Context
	runStop   context.CancelFunc
	// runSlots bounds concurrent agent subprocesses for every run path.
	runSlots chan struct{}
	// pendingRuns holds runs that were created while every slot was busy.
	pendingRuns []pendingRun
}

func New(ctx context.Context, settings *config.Settings) (*App, error) {
	return newApp(ctx, settings, true)
}

func NewCLI(ctx context.Context, settings *config.Settings) (*App, error) {
	return newApp(ctx, settings, false)
}

func newApp(ctx context.Context, settings *config.Settings, recoverRuns bool) (*App, error) {
	st, err := store.Open(settings.DatabasePath)
	if err != nil {
		return nil, err
	}
	cipher, err := security.NewCipher(settings.KeyBytes)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	a := &App{
		Settings: settings,
		Store:    st,
		Cipher:   cipher,
		Mux:      http.NewServeMux(),
		Logger:   slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		limiter:  newRateLimiter(),
		broker:   newLoginBroker(),
		events:   newEventHub(),
		Bili:     bilibili.New(),
		Providers: provider.NewRegistry(provider.Options{
			Timeout:   settings.ProviderTimeout,
			UserAgent: version.UserAgent(),
			DisableJS: settings.DisableJSAdapters,
		}),
		WebFS:    webui.FS,
		runTasks: make(map[string]context.CancelFunc),
	}
	a.routes()
	if recoverRuns {
		if err := a.recoverOrphanRuns(ctx); err != nil {
			_ = st.Close()
			return nil, err
		}
	}
	return a, nil
}

func (a *App) Close() error {
	a.runMu.Lock()
	if a.runStop != nil {
		a.runStop()
	}
	for _, cancel := range a.runTasks {
		cancel()
	}
	a.runMu.Unlock()
	a.runWG.Wait()
	return a.Store.Close()
}

func (a *App) Handler() http.Handler {
	return a.middleware(a.Mux)
}

func (a *App) routes() {
	a.Mux.HandleFunc("GET /api/health", a.health)
	a.Mux.HandleFunc("POST /api/auth/login", a.login)
	a.Mux.HandleFunc("GET /api/auth/me", a.me)
	a.Mux.HandleFunc("POST /api/auth/csrf", a.rotateCSRF)
	a.Mux.HandleFunc("POST /api/auth/logout", a.logout)
	a.Mux.HandleFunc("GET /api/auth/invite/{token}", a.inviteInfo)
	a.Mux.HandleFunc("POST /api/auth/invite/accept", a.inviteAccept)
	a.Mux.HandleFunc("POST /api/auth/invite/verify", a.inviteVerify)
	a.Mux.HandleFunc("POST /api/auth/step-up", a.stepUp)
	a.Mux.HandleFunc("POST /api/auth/change-password", a.changePassword)

	a.Mux.HandleFunc("GET /api/me/status", a.status)
	a.Mux.HandleFunc("GET /api/me/config", a.getConfig)
	a.Mux.HandleFunc("PATCH /api/me/config", a.patchConfig)
	a.Mux.HandleFunc("POST /api/me/runs", a.runNow)
	a.Mux.HandleFunc("GET /api/me/runs", a.listRuns)
	a.Mux.HandleFunc("POST /api/me/runs/{runID}/cancel", a.cancelRun)
	a.Mux.HandleFunc("GET /api/me/watched", a.watched)
	a.Mux.HandleFunc("GET /api/me/report", a.report)
	a.Mux.HandleFunc("GET /api/me/deliveries", a.deliveries)
	a.Mux.HandleFunc("GET /api/me/usage", a.usage)
	a.Mux.HandleFunc("POST /api/me/balance/refresh", a.refreshBalance)
	a.Mux.HandleFunc("POST /api/me/verify", a.verifyDeliveries)
	a.Mux.HandleFunc("POST /api/me/login/start", a.biliLoginStart)
	a.Mux.HandleFunc("POST /api/me/login/poll", a.biliLoginPoll)
	a.Mux.HandleFunc("GET /api/me/fav-folders", a.favoriteFolders)
	a.Mux.HandleFunc("POST /api/me/fav-today", a.favoriteToday)
	a.Mux.HandleFunc("GET /api/me/tokens", a.listTokens)
	a.Mux.HandleFunc("POST /api/me/tokens", a.createToken)
	a.Mux.HandleFunc("DELETE /api/me/tokens/{prefix}", a.revokeToken)
	a.Mux.HandleFunc("GET /api/me/diagnostics", a.diagnostics)
	a.Mux.HandleFunc("GET /api/me/export", a.exportData)
	a.Mux.HandleFunc("GET /api/me/cover", a.cover)
	a.Mux.HandleFunc("GET /api/me/events", a.streamEvents)

	a.Mux.HandleFunc("GET /api/admin/users", a.adminUsers)
	a.Mux.HandleFunc("POST /api/admin/invites", a.adminCreateInvite)
	a.Mux.HandleFunc("GET /api/admin/invites", a.adminInvites)
	a.Mux.HandleFunc("PATCH /api/admin/users/{userID}", a.adminPatchUser)
	a.Mux.HandleFunc("POST /api/admin/users/{userID}/reset-totp", a.adminResetTOTP)
	a.Mux.HandleFunc("GET /api/admin/settings", a.adminGetSettings)
	a.Mux.HandleFunc("PUT /api/admin/settings", a.adminPutSettings)
	a.Mux.HandleFunc("GET /api/admin/audit", a.adminAudit)
	a.Mux.HandleFunc("GET /api/admin/health", a.adminHealth)
	a.Mux.HandleFunc("POST /api/admin/backup", a.adminBackup)
	a.Mux.HandleFunc("GET /api/admin/providers", a.adminProviders)
	a.Mux.HandleFunc("POST /api/admin/providers", a.adminCreateProvider)
	a.Mux.HandleFunc("GET /api/admin/provider-kinds", a.adminProviderKinds)
	a.Mux.HandleFunc("GET /api/admin/providers/{providerID}", a.adminGetProvider)
	a.Mux.HandleFunc("PUT /api/admin/providers/{providerID}", a.adminUpdateProvider)
	a.Mux.HandleFunc("DELETE /api/admin/providers/{providerID}", a.adminDeleteProvider)
	a.Mux.HandleFunc("POST /api/admin/providers/{providerID}/check", a.adminCheckBalance)
	a.Mux.HandleFunc("POST /api/admin/providers/{providerID}/validate", a.adminValidateProviderScript)

	a.Mux.HandleFunc("POST /api/ingest/usage", a.ingestUsage)
	a.Mux.HandleFunc("POST /api/ingest/balance", a.ingestBalance)

	a.Mux.HandleFunc("/api/mcp", a.handleMCP)
	a.Mux.HandleFunc("/api/docs", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "API docs are available in the Go source")
	})
	a.Mux.HandleFunc("/", a.spa)
}

func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data: https://*.hdslb.com; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
		if a.Settings.SecureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			!strings.HasPrefix(r.URL.Path, "/api/auth/login") &&
			!strings.HasPrefix(r.URL.Path, "/api/auth/invite") {
			if !a.limiter.Allow("api:"+clientIP(r), a.Settings.APIRatePerMinute, time.Minute) {
				writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}
		next.ServeHTTP(w, r)
		a.Logger.Info("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"ip", clientIP(r),
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func (a *App) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "service": "bili-scroll-agent", "version": version.String(),
	})
}

func (a *App) spa(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if clean == "." {
		clean = "index.html"
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	target := "dist/" + filepath.ToSlash(clean)
	if raw, err := fs.ReadFile(a.WebFS, target); err == nil {
		w.Header().Set("Cache-Control", "public,max-age=3600")
		http.ServeContent(w, r, clean, time.Time{}, bytes.NewReader(raw))
		return
	}
	index, err := fs.ReadFile(a.WebFS, "dist/index.html")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"detail": "WebUI is not built; run `bun run build` in web/",
		})
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

func (a *App) recoverOrphanRuns(ctx context.Context) error {
	result, err := a.Store.DB.ExecContext(ctx, `
		UPDATE runs
		SET status='failed',
			error='service restarted before the run completed; start a new run',
			finished_at=?
		WHERE status IN ('queued','running')`, store.Now())
	if err != nil {
		return err
	}
	_, _ = a.Store.DB.ExecContext(ctx, `
		UPDATE api_tokens SET revoked_at=?
		WHERE kind='run' AND revoked_at IS NULL
			AND run_id IN (SELECT id FROM runs WHERE status='failed')`, store.Now())
	_, _ = result.RowsAffected()
	return nil
}

// maxRateLimiterKeys bounds the in-memory limiter so a scan of many source
// IPs cannot grow it without limit.
const maxRateLimiterKeys = 100_000

type rateBucket struct {
	times []time.Time
	seen  time.Time
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateBucket
	window  time.Duration
	calls   int
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{entries: make(map[string]*rateBucket), window: time.Minute}
}

func (l *rateLimiter) Allow(key string, limit int, window time.Duration) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= maxRateLimiterKeys {
			l.sweepLocked(now, window)
			if len(l.entries) >= maxRateLimiterKeys {
				l.evictOldestLocked(maxRateLimiterKeys / 100)
			}
		}
		bucket = &rateBucket{}
		l.entries[key] = bucket
	}
	bucket.seen = now
	cutoff := now.Add(-window)
	first := 0
	for first < len(bucket.times) && bucket.times[first].Before(cutoff) {
		first++
	}
	bucket.times = bucket.times[first:]
	if len(bucket.times) >= limit {
		return false
	}
	bucket.times = append(bucket.times, now)
	// Opportunistic cleanup keeps the map small without a background task.
	l.calls++
	if l.calls%4096 == 0 {
		l.sweepLocked(now, window)
	}
	return true
}

func (l *rateLimiter) Sweep(now time.Time, window time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepLocked(now, window)
}

func (l *rateLimiter) sweepLocked(now time.Time, window time.Duration) {
	cutoff := now.Add(-2 * window)
	for key, bucket := range l.entries {
		if bucket.seen.Before(cutoff) {
			delete(l.entries, key)
		}
	}
}

func (l *rateLimiter) evictOldestLocked(count int) {
	if count < 1 {
		count = 1
	}
	type entry struct {
		key  string
		seen time.Time
	}
	entries := make([]entry, 0, len(l.entries))
	for key, bucket := range l.entries {
		entries = append(entries, entry{key, bucket.seen})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].seen.Before(entries[j].seen) })
	for i := 0; i < count && i < len(entries); i++ {
		delete(l.entries, entries[i].key)
	}
}

type loginBroker struct {
	mu     sync.Mutex
	owners map[string]string
}

func newLoginBroker() *loginBroker {
	return &loginBroker{owners: make(map[string]string)}
}

func (b *loginBroker) Claim(key, userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.owners[key] = userID
}

func (b *loginBroker) Owner(key string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.owners[key]
}

func (b *loginBroker) Forget(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.owners, key)
}

type eventHub struct {
	mu      sync.Mutex
	clients map[string]map[chan eventMessage]struct{}
}

type eventMessage struct {
	Name string
	Data any
}

func newEventHub() *eventHub {
	return &eventHub{clients: make(map[string]map[chan eventMessage]struct{})}
}

func (h *eventHub) Subscribe(userID string) (<-chan eventMessage, func()) {
	ch := make(chan eventMessage, 16)
	h.mu.Lock()
	if h.clients[userID] == nil {
		h.clients[userID] = make(map[chan eventMessage]struct{})
	}
	h.clients[userID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.clients[userID], ch)
		close(ch)
		h.mu.Unlock()
	}
}

func (h *eventHub) Publish(userID, name string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients[userID] {
		select {
		case ch <- eventMessage{Name: name, Data: data}:
		default:
		}
	}
}

func marshalString(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullTime(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func intFromAny(value any, fallback int) int {
	switch typed := value.(type) {
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return int(n)
		}
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		if n, err := strconv.Atoi(typed); err == nil {
			return n
		}
	}
	return fallback
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case float32:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	default:
		return ""
	}
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
