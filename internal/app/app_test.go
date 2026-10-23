package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
	"github.com/Miku0139oao/bili-scroll-agent/internal/security"
)

func testApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	settings := &config.Settings{
		AppName:             "test",
		Environment:         "test",
		Host:                "127.0.0.1",
		Port:                0,
		PublicURL:           "http://testserver",
		DataDir:             root,
		DatabasePath:        filepath.Join(root, "bili-scroll.db"),
		KeyBytes:            bytes.Repeat([]byte{7}, 32),
		SessionDuration:     time.Hour,
		SessionIdleDuration: 30 * time.Minute,
		InviteDuration:      24 * time.Hour,
		RunTokenDuration:    45 * time.Minute,
		GlobalConcurrency:   1,
		ManualRunsPerDay:    3,
		ScheduledRunsPerDay: 1,
		RetentionDays:       365,
		LoginRatePerMinute:  100,
		APIRatePerMinute:    10000,
		BindOutboundEnabled: true,
		AgentTimeout:        30 * time.Second,
		WebDist:             filepath.Join(root, "web"),
	}
	application, err := New(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application
}

func loginAs(t *testing.T, application *App, username string) (string, string, string) {
	t.Helper()
	password := "a correct horse battery"
	secret, _, err := CreateAdmin(context.Background(), application, username, username, password)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"username": username,
		"password": password,
		"totp":     security.TOTPCode(secret, time.Now()),
	})
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	application.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		CSRF string `json:"csrf"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("session cookie missing")
	}
	return cookies[0].Value, response.CSRF, secret
}

func TestHealthAndUnauthenticatedProtection(t *testing.T) {
	application := testApp(t)
	health := httptest.NewRecorder()
	application.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status %d", health.Code)
	}
	status := httptest.NewRecorder()
	application.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/me/status", nil))
	if status.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", status.Code)
	}
}

func TestLoginTokenAndMCP(t *testing.T) {
	application := testApp(t)
	cookie, csrf, _ := loginAs(t, application, "owner")

	tokenRequest := httptest.NewRequest(http.MethodPost, "/api/me/tokens",
		bytes.NewReader([]byte(`{"label":"test"}`)))
	tokenRequest.Header.Set("Content-Type", "application/json")
	tokenRequest.Header.Set("X-CSRF-Token", csrf)
	tokenRequest.AddCookie(&http.Cookie{Name: "bili_session", Value: cookie})
	tokenResponse := httptest.NewRecorder()
	application.Handler().ServeHTTP(tokenResponse, tokenRequest)
	if tokenResponse.Code != http.StatusOK {
		t.Fatalf("token creation failed: %d %s", tokenResponse.Code, tokenResponse.Body.String())
	}
	var tokenBody struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(tokenResponse.Body.Bytes(), &tokenBody)

	mcpRequest := func(id int, method string, params any) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": method, "params": params,
		})
		request := httptest.NewRequest(http.MethodPost, "/api/mcp", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+tokenBody.Token)
		recorder := httptest.NewRecorder()
		application.Handler().ServeHTTP(recorder, request)
		return recorder
	}
	initialize := mcpRequest(1, "initialize", map[string]any{"protocolVersion": "2025-03-26"})
	if initialize.Code != http.StatusOK {
		t.Fatalf("MCP initialize failed: %d %s", initialize.Code, initialize.Body.String())
	}
	listed := mcpRequest(2, "tools/list", map[string]any{})
	if listed.Code != http.StatusOK {
		t.Fatalf("MCP tools/list failed: %d", listed.Code)
	}
	var listBody map[string]any
	_ = json.Unmarshal(listed.Body.Bytes(), &listBody)
	result, _ := listBody["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) < 15 {
		t.Fatalf("expected MCP tool catalog, got %d", len(tools))
	}
	called := mcpRequest(3, "tools/call", map[string]any{
		"name": "bili_status", "arguments": map[string]any{},
	})
	if called.Code != http.StatusOK {
		t.Fatalf("MCP tools/call failed: %d %s", called.Code, called.Body.String())
	}
	if bytes.Contains(called.Body.Bytes(), []byte(`"isError":true`)) {
		t.Fatalf("bili_status returned tool error: %s", called.Body.String())
	}
}

func TestTenantIsolation(t *testing.T) {
	application := testApp(t)
	ownerCookie, _, _ := loginAs(t, application, "owner-a")
	otherCookie, _, _ := loginAs(t, application, "owner-b")

	request := func(cookie string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/me/status", nil)
		req.AddCookie(&http.Cookie{Name: "bili_session", Value: cookie})
		recorder := httptest.NewRecorder()
		application.Handler().ServeHTTP(recorder, req)
		return recorder
	}
	first := request(ownerCookie)
	second := request(otherCookie)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("status endpoints failed: %d %d", first.Code, second.Code)
	}
	var firstBody, secondBody map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &firstBody)
	_ = json.Unmarshal(second.Body.Bytes(), &secondBody)
	firstUser := firstBody["user"].(map[string]any)
	secondUser := secondBody["user"].(map[string]any)
	if firstUser["id"] == secondUser["id"] {
		t.Fatal("tenant identities leaked across sessions")
	}
}
