// Package provider implements balance adapters for the AI providers a
// deployment routes its agent runners through.
//
// It is deliberately independent of internal/app and internal/config: adapters
// receive decrypted credentials in Config and return balances. Admin-authored
// JavaScript adapters run in-process through goja with a narrow host API; see
// docs/providers.md for the trust model.
package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Well-known provider kinds. Any string is allowed as a kind; unknown kinds are
// rejected by the registry unless an adapter was registered for them.
const (
	KindDeepSeek         = "deepseek"
	KindOpenRouter       = "openrouter"
	KindOpenAICompatible = "openai-compatible"
	KindNone             = "none"
	KindJS               = "js"
)

var (
	// ErrNoBalanceAPI reports that a provider has no public balance endpoint.
	// It is a property of the provider, not an operational failure.
	ErrNoBalanceAPI = errors.New("provider does not expose a balance API")
	// ErrDisabled reports that the provider account is turned off.
	ErrDisabled = errors.New("provider is disabled")
	// ErrUnknownKind reports a kind with no registered adapter.
	ErrUnknownKind = errors.New("unknown provider kind")
)

// Config is everything an adapter needs for one fetch. APIKey is plaintext and
// must never be logged, persisted, or returned to a client.
type Config struct {
	ProviderID string
	Kind       string
	BaseURL    string
	APIKey     string
	Currency   string
	AllowHosts []string
	Settings   map[string]any
	Script     string
}

// Balance is one currency's worth of credit for a provider.
type Balance struct {
	Currency  string  `json:"currency"`
	Total     float64 `json:"total"`
	Granted   float64 `json:"granted"`
	ToppedUp  float64 `json:"topped_up"`
	Available bool    `json:"available"`
}

// Adapter fetches balances for one provider kind.
type Adapter interface {
	Kind() string
	// SupportsBalance reports whether Fetch can ever succeed. It lets the UI
	// display "no balance API" without performing a request.
	SupportsBalance() bool
	// Fetch receives a fetch environment (guarded HTTP, logging) that the
	// registry builds per call.
	Fetch(ctx context.Context, cfg Config, env *FetchEnv) ([]Balance, error)
}

// KindInfo describes a registered kind for the admin UI.
type KindInfo struct {
	Kind            string `json:"kind"`
	Label           string `json:"label"`
	SupportsBalance bool   `json:"supports_balance"`
	RequiresScript  bool   `json:"requires_script"`
	RequiresBaseURL bool   `json:"requires_base_url"`
	Description     string `json:"description"`
}

// Registry maps provider kinds to adapters and owns the JavaScript runtime.
type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
	js       *jsRunner
	opts     Options
}

// Options tunes outbound requests and script execution.
type Options struct {
	// HTTPClient overrides the guarded client used by built-in adapters. Tests
	// use it to point at httptest servers.
	HTTPClient HTTPDoer
	// Timeout bounds a whole fetch. Default 20s.
	Timeout time.Duration
	// ScriptTimeout bounds JavaScript execution. Default 15s.
	ScriptTimeout time.Duration
	// MaxResponseBytes caps one HTTP response body. Default 1 MiB.
	MaxResponseBytes int64
	// MaxTotalResponseBytes caps all bodies read during one fetch. Default 512 KiB.
	MaxTotalResponseBytes int64
	// UserAgent is sent by built-in adapters.
	UserAgent string
	// DisableJS turns off kind=js entirely (BILI_SCROLL_DISABLE_JS_ADAPTERS).
	DisableJS bool
	// MaxCallStackSize bounds adapter recursion. Default 2048.
	MaxCallStackSize int
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = 20 * time.Second
	}
	if o.ScriptTimeout <= 0 {
		o.ScriptTimeout = 15 * time.Second
	}
	if o.ScriptTimeout > o.Timeout {
		o.ScriptTimeout = o.Timeout
	}
	if o.MaxResponseBytes <= 0 {
		o.MaxResponseBytes = 1 << 20
	}
	if o.MaxTotalResponseBytes <= 0 {
		o.MaxTotalResponseBytes = 512 << 10
	}
	if o.UserAgent == "" {
		o.UserAgent = "bili-scroll-agent"
	}
	if o.MaxCallStackSize <= 0 {
		o.MaxCallStackSize = 2048
	}
	return o
}

// NewRegistry returns a registry with all built-in adapters registered.
func NewRegistry(opts Options) *Registry {
	opts = opts.withDefaults()
	registry := &Registry{adapters: map[string]Adapter{}, opts: opts}
	registry.js = newJSRunner(opts)
	registry.Register(deepSeekAdapter{})
	registry.Register(openRouterAdapter{})
	registry.Register(noBalanceAdapter{kind: KindOpenAICompatible})
	registry.Register(noBalanceAdapter{kind: KindNone})
	if !opts.DisableJS {
		registry.Register(scriptAdapter{runner: registry.js})
	}
	return registry
}

// Register adds or replaces an adapter. Safe for tests and future plugins.
func (r *Registry) Register(adapter Adapter) {
	if adapter == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.Kind()] = adapter
}

// Kinds returns the registered kinds sorted by name.
func (r *Registry) Kinds() []KindInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]KindInfo, 0, len(r.adapters))
	for kind, adapter := range r.adapters {
		info := KindInfo{
			Kind:            kind,
			Label:           kindLabel(kind),
			SupportsBalance: adapter.SupportsBalance(),
		}
		switch kind {
		case KindJS:
			info.RequiresScript = true
			info.RequiresBaseURL = false
			info.Description = "Admin-authored JavaScript adapter; defines fetchBalance(ctx)."
		case KindNone, KindOpenAICompatible:
			info.RequiresBaseURL = true
			info.Description = "No public balance API; usage appears in the dashboard only."
		case KindDeepSeek:
			info.RequiresBaseURL = true
			info.Description = "Native GET /user/balance."
		case KindOpenRouter:
			info.RequiresBaseURL = true
			info.Description = "Native GET /credits."
		default:
			info.RequiresBaseURL = true
		}
		result = append(result, info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Kind < result[j].Kind })
	return result
}

// HasKind reports whether an adapter is registered for kind.
func (r *Registry) HasKind(kind string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.adapters[kind]
	return ok
}

// SupportsBalance reports whether the kind has a balance API at all.
func (r *Registry) SupportsBalance(kind string) bool {
	r.mu.RLock()
	adapter, ok := r.adapters[kind]
	r.mu.RUnlock()
	return ok && adapter.SupportsBalance()
}

// Fetch runs the adapter for cfg.Kind.
func (r *Registry) Fetch(ctx context.Context, cfg Config) ([]Balance, error) {
	if strings.TrimSpace(cfg.ProviderID) == "" {
		return nil, errors.New("provider id is required")
	}
	kind := cfg.Kind
	if kind == "" {
		kind = KindDeepSeek
	}
	r.mu.RLock()
	adapter, ok := r.adapters[kind]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownKind, kind)
	}
	ctx, cancel := context.WithTimeout(ctx, r.opts.Timeout)
	defer cancel()
	env := r.newFetchEnv(ctx, cfg)
	balances, err := adapter.Fetch(ctx, cfg, env)
	if err != nil {
		return nil, redactError(err, cfg.APIKey)
	}
	normalized, err := NormalizeBalances(balances, cfg.Currency)
	if err != nil {
		return nil, redactError(err, cfg.APIKey)
	}
	return normalized, nil
}

// newFetchEnv builds the per-call adapter environment: an allowlisted HTTP
// client plus a capped log buffer.
func (r *Registry) newFetchEnv(ctx context.Context, cfg Config) *FetchEnv {
	total := &atomic.Int64{}
	guard := newGuard(cfg.AllowHosts, cfg.BaseURL)
	return &FetchEnv{
		ctx:   ctx,
		guard: guard,
		http: &guardedClient{
			guard:      guard,
			client:     r.opts.HTTPClient,
			maxBytes:   r.opts.MaxResponseBytes,
			totalLimit: r.opts.MaxTotalResponseBytes,
			total:      total,
			userAgent:  r.opts.UserAgent,
		},
	}
}

// FetchEnv is the per-call environment handed to adapters.
type FetchEnv struct {
	ctx   context.Context
	guard *guard
	http  *guardedClient
	logs  strings.Builder
}

// HTTP performs a guarded request for the adapter.
func (e *FetchEnv) HTTP(ctx context.Context, opts fetchOptions) (HTTPResponse, error) {
	return e.http.do(ctx, opts)
}

// Log appends a capped message for the admin check response.
func (e *FetchEnv) Log(message string) {
	const limit = 8 << 10
	if e.logs.Len() >= limit {
		return
	}
	remaining := limit - e.logs.Len()
	if len(message)+1 > remaining {
		message = message[:max(0, remaining-1)]
	}
	e.logs.WriteString(message)
	e.logs.WriteString("\n")
}

// Logs returns the captured adapter log.
func (e *FetchEnv) Logs() string { return e.logs.String() }

// Hosts returns the hosts the adapter actually contacted.
func (e *FetchEnv) Hosts() []string { return e.guard.Hosts() }

// ValidateScript compiles a JavaScript adapter without performing network I/O.
func (r *Registry) ValidateScript(source string) (ScriptCheck, error) {
	return r.js.Validate(source)
}

// kindLabel returns the human label for a well-known kind.
func kindLabel(kind string) string {
	switch kind {
	case KindDeepSeek:
		return "DeepSeek"
	case KindOpenRouter:
		return "OpenRouter"
	case KindOpenAICompatible:
		return "OpenAI-compatible"
	case KindNone:
		return "No balance API"
	case KindJS:
		return "JavaScript adapter"
	default:
		return kind
	}
}

// NormalizeBalances validates adapter output: at most 32 entries, finite
// numbers, and a short currency code.
func NormalizeBalances(items []Balance, fallbackCurrency string) ([]Balance, error) {
	if len(items) > 32 {
		return nil, fmt.Errorf("adapter returned %d balances; at most 32 are supported", len(items))
	}
	fallback := strings.TrimSpace(fallbackCurrency)
	if fallback == "" {
		fallback = "USD"
	}
	result := make([]Balance, 0, len(items))
	for index, item := range items {
		if !isFinite(item.Total) {
			return nil, fmt.Errorf("balance %d has a non-finite total", index)
		}
		if !isFinite(item.Granted) || !isFinite(item.ToppedUp) {
			return nil, fmt.Errorf("balance %d has a non-finite amount", index)
		}
		currency := strings.TrimSpace(item.Currency)
		if currency == "" {
			currency = fallback
		}
		if len(currency) > 8 {
			return nil, fmt.Errorf("balance %d has an invalid currency %q", index, currency)
		}
		result = append(result, Balance{
			Currency:  currency,
			Total:     item.Total,
			Granted:   item.Granted,
			ToppedUp:  item.ToppedUp,
			Available: item.Available,
		})
	}
	return result, nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// redactError removes the API key from an error message so a careless adapter
// cannot leak it into HTTP responses, MCP output, or telemetry.
func redactError(err error, apiKey string) error {
	if err == nil {
		return nil
	}
	key := strings.TrimSpace(apiKey)
	if key == "" || !strings.Contains(err.Error(), key) {
		// Preserve sentinel identity (ErrNoBalanceAPI, ErrUnknownKind, ...)
		// for errors.Is checks in callers.
		return err
	}
	return fmt.Errorf("%w", errors.New(strings.ReplaceAll(err.Error(), key, "[redacted]")))
}
