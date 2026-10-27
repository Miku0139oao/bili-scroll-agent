package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testRegistry(t *testing.T, opts Options) (*Registry, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no route", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return NewRegistry(opts), server
}

func TestValidateBaseURL(t *testing.T) {
	valid := []string{
		"https://api.deepseek.com",
		"https://openrouter.ai/api",
		"http://127.0.0.1:8788",
		"http://localhost:9000/",
		"http://[::1]:8080",
	}
	for _, raw := range valid {
		if _, err := ValidateBaseURL(raw); err != nil {
			t.Errorf("ValidateBaseURL(%q) = %v, want nil", raw, err)
		}
	}
	invalid := []string{
		"",
		"ftp://api.example.com",
		"http://api.example.com",
		"https://user:pass@api.example.com",
		"https://api.example.com?key=1",
		"not a url",
	}
	for _, raw := range invalid {
		if _, err := ValidateBaseURL(raw); err == nil {
			t.Errorf("ValidateBaseURL(%q) = nil, want error", raw)
		}
	}
}

func TestRegistryKinds(t *testing.T) {
	registry := NewRegistry(Options{})
	kinds := registry.Kinds()
	seen := map[string]KindInfo{}
	for _, info := range kinds {
		seen[info.Kind] = info
	}
	for _, kind := range []string{KindDeepSeek, KindOpenRouter, KindOpenAICompatible, KindNone, KindJS} {
		info, ok := seen[kind]
		if !ok {
			t.Fatalf("kind %q not registered", kind)
		}
		if kind == KindJS && !info.RequiresScript {
			t.Fatal("js kind must require a script")
		}
	}
	if !registry.SupportsBalance(KindDeepSeek) || registry.SupportsBalance(KindNone) {
		t.Fatal("SupportsBalance reported incorrectly")
	}
	if registry.HasKind("nope") {
		t.Fatal("unknown kind was reported as registered")
	}

	disabled := NewRegistry(Options{DisableJS: true})
	if disabled.HasKind(KindJS) {
		t.Fatal("DisableJS did not remove the js adapter")
	}
}

func TestRegistryUnknownKind(t *testing.T) {
	registry := NewRegistry(Options{})
	_, err := registry.Fetch(context.Background(), Config{ProviderID: "x", Kind: "nope"})
	if !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("err = %v, want ErrUnknownKind", err)
	}
}

func TestRegistryCustomAdapter(t *testing.T) {
	registry := NewRegistry(Options{})
	registry.Register(stubAdapter{kind: "stub", balances: []Balance{{Currency: "EUR", Total: 3}}})
	got, err := registry.Fetch(context.Background(), Config{ProviderID: "s", Kind: "stub"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Total != 3 || got[0].Currency != "EUR" {
		t.Fatalf("unexpected balances: %+v", got)
	}
}

func TestNoBalanceKinds(t *testing.T) {
	registry := NewRegistry(Options{})
	for _, kind := range []string{KindNone, KindOpenAICompatible} {
		_, err := registry.Fetch(context.Background(), Config{ProviderID: "x", Kind: kind})
		if !errors.Is(err, ErrNoBalanceAPI) {
			t.Fatalf("%s: err = %v, want ErrNoBalanceAPI", kind, err)
		}
	}
}

func TestDeepSeekAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/balance" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bad key"}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"is_available": true,
			"balance_infos": [
				{"currency": "CNY", "total_balance": "12.5", "granted_balance": 1, "topped_up_balance": 11.5},
				{"currency": "USD", "total_balance": 2.25, "granted_balance": 0, "topped_up_balance": 2.25}
			]}`))
	}))
	defer server.Close()

	registry := NewRegistry(Options{HTTPClient: server.Client()})
	balances, err := registry.Fetch(context.Background(), Config{
		ProviderID: "deepseek", Kind: KindDeepSeek, BaseURL: server.URL, APIKey: "sk-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 2 {
		t.Fatalf("got %d balances, want 2", len(balances))
	}
	if balances[0].Currency != "CNY" || balances[1].Currency != "USD" {
		t.Fatalf("unexpected currencies: %+v", balances)
	}
	// String amounts must still parse; the original DeepSeek payload uses them.
	if balances[0].Total != 12.5 || balances[0].ToppedUp != 11.5 {
		t.Fatalf("unexpected amounts: %+v", balances[0])
	}

	_, err = registry.Fetch(context.Background(), Config{
		ProviderID: "deepseek", Kind: KindDeepSeek, BaseURL: server.URL, APIKey: "wrong",
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected a 401 error, got %v", err)
	}
}

func TestDeepSeekAdapterErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"server error", http.StatusInternalServerError, `{}`, "500"},
		{"invalid json", http.StatusOK, `not json`, "invalid JSON"},
		{"unauthorized", http.StatusUnauthorized, `{}`, "401"},
	}
	for _, item := range cases {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(item.status)
			_, _ = w.Write([]byte(item.body))
		}))
		registry := NewRegistry(Options{HTTPClient: server.Client()})
		_, err := registry.Fetch(context.Background(), Config{
			ProviderID: "deepseek", Kind: KindDeepSeek, BaseURL: server.URL, APIKey: "k",
		})
		server.Close()
		if err == nil || !strings.Contains(err.Error(), item.want) {
			t.Fatalf("%s: err = %v, want %q", item.name, err, item.want)
		}
	}
}

func TestDeepSeekUnavailableFlag(t *testing.T) {
	balances, err := parseDeepSeekBalance(HTTPResponse{
		Status: 200,
		Body:   `{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":0}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 1 || balances[0].Available {
		t.Fatalf("is_available was not honoured: %+v", balances)
	}
}

func TestOpenRouterAdapter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/credits" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"total_credits":20,"total_usage":7.5}}`))
	}))
	defer server.Close()

	registry := NewRegistry(Options{HTTPClient: server.Client()})
	balances, err := registry.Fetch(context.Background(), Config{
		ProviderID: "openrouter", Kind: KindOpenRouter, BaseURL: server.URL, APIKey: "sk-or",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 1 || balances[0].Total != 12.5 || balances[0].Currency != "USD" {
		t.Fatalf("unexpected balances: %+v", balances)
	}
}

func TestNormalizeBalances(t *testing.T) {
	if _, err := NormalizeBalances([]Balance{{Total: 1}}, "CNY"); err != nil {
		t.Fatal(err)
	}
	got, _ := NormalizeBalances([]Balance{{Total: 1}}, "CNY")
	if got[0].Currency != "CNY" {
		t.Fatalf("fallback currency not applied: %+v", got)
	}
	if _, err := NormalizeBalances([]Balance{{Currency: "TOOLONGXX", Total: 1}}, ""); err == nil {
		t.Fatal("expected a long currency to be rejected")
	}
	many := make([]Balance, 33)
	if _, err := NormalizeBalances(many, ""); err == nil {
		t.Fatal("expected more than 32 balances to be rejected")
	}
}

type stubAdapter struct {
	kind     string
	balances []Balance
	err      error
}

func (s stubAdapter) Kind() string          { return s.kind }
func (s stubAdapter) SupportsBalance() bool { return true }
func (s stubAdapter) Fetch(context.Context, Config, *FetchEnv) ([]Balance, error) {
	return s.balances, s.err
}

func TestRegistryRedactsAPIKeyFromErrors(t *testing.T) {
	registry := NewRegistry(Options{})
	registry.Register(stubAdapter{kind: "leaky", err: errors.New("key sk-secret-123 is bad")})
	_, err := registry.Fetch(context.Background(), Config{
		ProviderID: "leaky", Kind: "leaky", APIKey: "sk-secret-123",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "sk-secret-123") {
		t.Fatalf("API key leaked into error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("expected a redaction marker, got %v", err)
	}
}

func TestGuardBlocksNonAllowlistedHost(t *testing.T) {
	allowed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer allowed.Close()

	guard := newGuard([]string{"127.0.0.1"}, allowed.URL)
	client := &guardedClient{guard: guard, client: allowed.Client(), maxBytes: 1 << 16, userAgent: "test"}
	if _, err := client.do(context.Background(), fetchOptions{URL: allowed.URL}); err != nil {
		t.Fatalf("allowed host was rejected: %v", err)
	}
	// The allowlist is host-based, so a different host is refused before dialing.
	if _, err := client.do(context.Background(), fetchOptions{URL: "https://collector.example.net/steal"}); err == nil {
		t.Fatal("non-allowlisted host was accepted")
	} else if !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if hosts := guard.Hosts(); len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("contacted hosts = %v", hosts)
	}
}

func TestGuardedClientRejectsNonHTTPSScheme(t *testing.T) {
	client := &guardedClient{guard: newGuard(nil, ""), client: http.DefaultClient, maxBytes: 1 << 10}
	for _, target := range []string{"file:///etc/passwd", "gopher://example.com/", "data:text/plain,x"} {
		if _, err := client.do(context.Background(), fetchOptions{URL: target}); err == nil {
			t.Fatalf("%s was accepted", target)
		}
	}
}

func TestGuardedClientLimitsBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer server.Close()
	client := &guardedClient{
		guard: newGuard(nil, server.URL), client: server.Client(),
		maxBytes: 128, userAgent: "test",
	}
	response, err := client.do(context.Background(), fetchOptions{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Body) > 128 {
		t.Fatalf("body not limited: %d bytes", len(response.Body))
	}
}

func TestGuardedClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := &guardedClient{
		guard: newGuard(nil, server.URL),
		client: &http.Client{
			Timeout: 50 * time.Millisecond,
		},
		maxBytes: 1 << 10,
	}
	if _, err := client.do(context.Background(), fetchOptions{URL: server.URL}); err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestJSONResponseParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"total":1}}`))
	}))
	defer server.Close()
	client := &guardedClient{guard: newGuard(nil, server.URL), client: server.Client(), maxBytes: 1 << 10}
	response, err := client.do(context.Background(), fetchOptions{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	object, ok := response.JSON.(map[string]any)
	if !ok {
		t.Fatalf("JSON not decoded: %#v", response.JSON)
	}
	if _, ok := object["data"]; !ok {
		t.Fatalf("unexpected JSON: %#v", object)
	}
	_ = json.Valid([]byte(response.Body))
}
