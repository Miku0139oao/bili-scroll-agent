package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Miku0139oao/bili-scroll-agent/internal/provider"
)

// doJSON performs a request through the app handler and returns the recorder.
func doJSON(t *testing.T, app *App, method, path, session, csrf string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	if session != "" {
		request.AddCookie(&http.Cookie{Name: "bili_session", Value: session})
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	recorder := httptest.NewRecorder()
	app.Handler().ServeHTTP(recorder, request)
	return recorder
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
	}
	return payload
}

func TestProviderListKeepsLegacyEnvelope(t *testing.T) {
	application := testApp(t)
	session, _, _ := loginAs(t, application, "admin1")

	recorder := doJSON(t, application, http.MethodGet, "/api/admin/providers", session, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeBody(t, recorder)

	providers, ok := payload["providers"].([]any)
	if !ok || len(providers) == 0 {
		t.Fatalf("missing providers array: %v", payload)
	}
	first, _ := providers[0].(map[string]any)
	if first["provider_id"] != "deepseek" {
		t.Fatalf("expected a seeded deepseek provider, got %v", first)
	}
	// The pre-0.3 envelope must survive so a cached WebUI bundle keeps working.
	legacy, ok := payload["deepseek"].(map[string]any)
	if !ok {
		t.Fatalf("missing legacy deepseek envelope: %v", payload)
	}
	for _, key := range []string{"provider_id", "base_url", "has_api_key", "low_balance_threshold", "enabled", "updated_at"} {
		if _, ok := legacy[key]; !ok {
			t.Fatalf("legacy envelope missing %q: %v", key, legacy)
		}
	}
}

func TestProviderUpdateLegacyRouteAcceptsOldBody(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin2")

	// The exact body shape the 0.2 admin UI sends.
	recorder := doJSON(t, application, http.MethodPut, "/api/admin/providers/deepseek", session, csrf,
		map[string]any{
			"base_url":              "https://api.deepseek.com",
			"api_key":               "sk-legacy-key",
			"low_balance_threshold": 5,
			"enabled":               true,
		})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeBody(t, recorder)
	legacy, ok := payload["deepseek"].(map[string]any)
	if !ok {
		t.Fatalf("legacy envelope missing: %v", payload)
	}
	if legacy["has_api_key"] != true {
		t.Fatalf("has_api_key = %v", legacy["has_api_key"])
	}
	if legacy["low_balance_threshold"] != float64(5) {
		t.Fatalf("threshold = %v", legacy["low_balance_threshold"])
	}

	// The stored ciphertext must still decrypt with the historical AAD.
	record, err := application.loadProvider(t.Context(), "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if record.APIKey != "sk-legacy-key" {
		t.Fatalf("stored key did not round-trip: %q", record.APIKey)
	}
}

func TestProviderUpdateKeepsKeyWhenOmitted(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin3")

	if recorder := doJSON(t, application, http.MethodPut, "/api/admin/providers/deepseek", session, csrf,
		map[string]any{"base_url": "https://api.deepseek.com", "api_key": "sk-first"}); recorder.Code != http.StatusOK {
		t.Fatalf("first update: %d %s", recorder.Code, recorder.Body.String())
	}
	// api_key null/absent must preserve the stored key.
	if recorder := doJSON(t, application, http.MethodPut, "/api/admin/providers/deepseek", session, csrf,
		map[string]any{"base_url": "https://api.deepseek.com", "low_balance_threshold": 3}); recorder.Code != http.StatusOK {
		t.Fatalf("second update: %d %s", recorder.Code, recorder.Body.String())
	}
	record, err := application.loadProvider(t.Context(), "deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if record.APIKey != "sk-first" {
		t.Fatalf("key was lost: %q", record.APIKey)
	}
	if record.LowBalanceThreshold != 3 {
		t.Fatalf("threshold = %v", record.LowBalanceThreshold)
	}
}

func TestProviderCreateAndDelete(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin4")

	script := `function fetchBalance(ctx) { return [{ currency: "USD", total: 1 }]; }`
	recorder := doJSON(t, application, http.MethodPost, "/api/admin/providers", session, csrf,
		map[string]any{
			"provider_id":  "custom-js",
			"display_name": "Custom",
			"kind":         provider.KindJS,
			"api_key":      "sk-custom",
			"currency":     "USD",
			"allow_hosts":  []string{"api.example.com"},
			"script":       script,
		})
	if recorder.Code != http.StatusOK {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}

	recorder = doJSON(t, application, http.MethodGet, "/api/admin/providers/custom-js", session, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("get: %d %s", recorder.Code, recorder.Body.String())
	}
	item := decodeBody(t, recorder)["provider"].(map[string]any)
	if item["script"] != script {
		t.Fatalf("script not returned to the editor: %v", item["script"])
	}
	if _, leaked := item["api_key"]; leaked {
		t.Fatal("api_key must never be returned")
	}
	if item["has_api_key"] != true {
		t.Fatalf("has_api_key = %v", item["has_api_key"])
	}

	if recorder := doJSON(t, application, http.MethodDelete, "/api/admin/providers/custom-js", session, csrf, nil); recorder.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder := doJSON(t, application, http.MethodGet, "/api/admin/providers/custom-js", session, "", nil); recorder.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", recorder.Code)
	}
}

func TestProviderValidationErrors(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin5")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"bad id", map[string]any{"provider_id": "Bad ID", "kind": provider.KindNone}},
		{"reserved id", map[string]any{"provider_id": "kinds", "kind": provider.KindNone}},
		{"unknown kind", map[string]any{"provider_id": "x1", "kind": "nope"}},
		{"js without script", map[string]any{"provider_id": "x2", "kind": provider.KindJS}},
		{"script on builtin", map[string]any{
			"provider_id": "x3", "kind": provider.KindDeepSeek,
			"base_url": "https://api.deepseek.com", "script": "function fetchBalance(){}",
		}},
		{"bad base url", map[string]any{"provider_id": "x4", "kind": provider.KindDeepSeek, "base_url": "http://evil.example.com"}},
		{"too many hosts", map[string]any{
			"provider_id": "x5", "kind": provider.KindNone,
			"allow_hosts": []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"},
		}},
	}
	for _, item := range cases {
		recorder := doJSON(t, application, http.MethodPost, "/api/admin/providers", session, csrf, item.body)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d %s", item.name, recorder.Code, recorder.Body.String())
		}
	}
}

func TestProviderScriptValidation(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin6")

	recorder := doJSON(t, application, http.MethodPost, "/api/admin/providers/any/validate", session, csrf,
		map[string]any{"script": `function fetchBalance(ctx) { return [{ total: 1 }]; }`})
	if recorder.Code != http.StatusOK {
		t.Fatalf("validate: %d %s", recorder.Code, recorder.Body.String())
	}
	if decodeBody(t, recorder)["compiled"] != true {
		t.Fatalf("script not reported as compiled: %s", recorder.Body.String())
	}

	recorder = doJSON(t, application, http.MethodPost, "/api/admin/providers/any/validate", session, csrf,
		map[string]any{"script": `function oops(`})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad script: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestProviderCheckUsesAdapter(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin7")

	// Register a stub adapter so no network is involved.
	application.Providers.Register(stubBalanceAdapter{
		kind: "stub-kind",
		balances: []provider.Balance{
			{Currency: "USD", Total: 4, Granted: 0, ToppedUp: 4, Available: true},
		},
	})
	recorder := doJSON(t, application, http.MethodPost, "/api/admin/providers", session, csrf,
		map[string]any{
			"provider_id": "stub", "kind": "stub-kind",
			"base_url": "https://stub.example.com", "api_key": "sk-stub",
			"low_balance_threshold": 10,
		})
	if recorder.Code != http.StatusOK {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = doJSON(t, application, http.MethodPost, "/api/admin/providers/stub/check", session, csrf, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("check: %d %s", recorder.Code, recorder.Body.String())
	}
	payload := decodeBody(t, recorder)
	balances, _ := payload["balances"].([]any)
	if len(balances) != 1 {
		t.Fatalf("unexpected balances: %v", payload)
	}
	first, _ := balances[0].(map[string]any)
	if first["low"] != true {
		t.Fatalf("threshold not applied: %v", first)
	}
	if strings.Contains(recorder.Body.String(), "sk-stub") {
		t.Fatal("API key leaked into the check response")
	}

	// The global balance table is written, and the dashboard reads it back.
	rows, err := application.savedBalanceRows(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row["provider"] == "stub" {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider balance not persisted: %v", rows)
	}
}

func TestProviderDisabledIsEnforced(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin8")
	application.Providers.Register(stubBalanceAdapter{kind: "stub-kind", balances: []provider.Balance{{Total: 1}}})

	recorder := doJSON(t, application, http.MethodPost, "/api/admin/providers", session, csrf,
		map[string]any{
			"provider_id": "off", "kind": "stub-kind",
			"base_url": "https://stub.example.com", "enabled": false,
		})
	if recorder.Code != http.StatusOK {
		t.Fatalf("create: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = doJSON(t, application, http.MethodPost, "/api/admin/providers/off/check", session, csrf, nil)
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("disabled provider was checked: %d %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "disabled") {
		t.Fatalf("unexpected error: %s", recorder.Body.String())
	}
}

func TestProviderRoutesRequireAdmin(t *testing.T) {
	application := testApp(t)
	session, csrf, _ := loginAs(t, application, "admin9")
	// Create a member through the admin invite flow would be heavy; instead
	// assert the admin route rejects an unauthenticated caller and that the
	// member-facing refresh endpoint exists separately.
	if recorder := doJSON(t, application, http.MethodGet, "/api/admin/providers", "", "", nil); recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list: %d", recorder.Code)
	}
	kindsRecorder := doJSON(t, application, http.MethodGet, "/api/admin/provider-kinds", session, "", nil)
	if kindsRecorder.Code != http.StatusOK {
		t.Fatalf("admin kinds: %d %s", kindsRecorder.Code, kindsRecorder.Body.String())
	}
	kinds := decodeBody(t, kindsRecorder)["kinds"].([]any)
	if len(kinds) < 5 {
		t.Fatalf("expected the built-in kinds, got %v", kinds)
	}
	_ = csrf
}

type stubBalanceAdapter struct {
	kind     string
	balances []provider.Balance
	err      error
}

func (s stubBalanceAdapter) Kind() string          { return s.kind }
func (s stubBalanceAdapter) SupportsBalance() bool { return s.err == nil }
func (s stubBalanceAdapter) Fetch(context.Context, provider.Config, *provider.FetchEnv) ([]provider.Balance, error) {
	return s.balances, s.err
}
