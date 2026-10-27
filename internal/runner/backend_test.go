package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
)

func testSettings() *config.Settings {
	return &config.Settings{
		Port:     8788,
		DataDir:  "C:/tmp/bili",
		CodexBin: "codex",
	}
}

func testRequest() Request {
	return Request{
		Run:    model.Run{ID: "run-1234567890"},
		Token:  "bili_run_token",
		Prompt: "do the work",
	}
}

func TestBackendRegistry(t *testing.T) {
	names := Names()
	for _, expected := range []string{"codex", "hermes", "claude"} {
		if _, ok := Lookup(expected); !ok {
			t.Fatalf("backend %q not registered (have %v)", expected, names)
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("unknown backend was found")
	}
	status := Status(testSettings())
	if len(status) != len(All()) {
		t.Fatalf("Status covered %d backends, want %d", len(status), len(All()))
	}
	for name, info := range status {
		if info.Label == "" {
			t.Fatalf("%s: missing label", name)
		}
		if !info.Available && info.Reason == "" {
			t.Fatalf("%s: unavailable backend must explain why", name)
		}
	}
}

func TestCodexInvocation(t *testing.T) {
	invocation, err := buildCodexInvocation(testSettings(), testRequest(), "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(invocation.Args, " ")
	for _, expected := range []string{
		"exec --ephemeral --skip-git-repo-check",
		"--disable plugins",
		`sandbox_mode="read-only"`,
		`mcp_servers.bili_scroll.url="http://127.0.0.1:8788/api/mcp"`,
		"mcp_servers.bili_scroll.tool_timeout_sec=300",
		"--json -",
	} {
		if !strings.Contains(args, expected) {
			t.Fatalf("codex argv missing %q:\n%s", expected, args)
		}
	}
	if invocation.Stdin != "do the work" {
		t.Fatalf("prompt must go to stdin, got %q", invocation.Stdin)
	}
	if len(invocation.Files) != 0 {
		t.Fatalf("codex should not need files: %v", invocation.Files)
	}
	if !strings.HasSuffix(invocation.Env["CODEX_HOME"], "codex") {
		t.Fatalf("CODEX_HOME = %q", invocation.Env["CODEX_HOME"])
	}

	// provider/model/reasoning are passed through, TOML-escaped.
	request := testRequest()
	request.Provider = `custom"provider`
	request.Model = "gpt-5"
	request.Reasoning = "high"
	withRoute, err := buildCodexInvocation(testSettings(), request, "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(withRoute.Args, " ")
	if !strings.Contains(joined, `model_provider="custom\"provider"`) {
		t.Fatalf("provider not escaped: %s", joined)
	}
	if !strings.Contains(joined, "--model gpt-5") {
		t.Fatalf("model missing: %s", joined)
	}
	if !strings.Contains(joined, `model_reasoning_effort="high"`) {
		t.Fatalf("reasoning missing: %s", joined)
	}
}

func TestHermesInvocation(t *testing.T) {
	invocation, err := buildHermesInvocation(testSettings(), testRequest(), "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(invocation.Args, " ")
	for _, expected := range []string{"--usage-file", "chat --oneshot --quiet", "--query-file", "--source tool"} {
		if !strings.Contains(args, expected) {
			t.Fatalf("hermes argv missing %q:\n%s", expected, args)
		}
	}
	if invocation.Stdin != "" {
		t.Fatal("hermes should read the prompt from a file, not stdin")
	}
	if invocation.Files["prompt.txt"] != "do the work" {
		t.Fatalf("prompt file = %q", invocation.Files["prompt.txt"])
	}
}

func TestClaudeInvocation(t *testing.T) {
	request := testRequest()
	request.Model = "claude-sonnet-4-6"
	invocation, err := buildClaudeInvocation(testSettings(), request, "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(invocation.Args, " ")
	for _, expected := range []string{
		"-p do the work",
		"--output-format stream-json",
		"--strict-mcp-config",
		"--allowedTools mcp__bili_scroll",
		"--model claude-sonnet-4-6",
	} {
		if !strings.Contains(args, expected) {
			t.Fatalf("claude argv missing %q:\n%s", expected, args)
		}
	}
	var mcpConfig struct {
		Servers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(invocation.Files["mcp.json"]), &mcpConfig); err != nil {
		t.Fatalf("mcp.json: %v", err)
	}
	server, ok := mcpConfig.Servers["bili_scroll"]
	if !ok || server.Type != "http" {
		t.Fatalf("unexpected mcp.json: %s", invocation.Files["mcp.json"])
	}
	if server.URL != "http://127.0.0.1:8788/api/mcp" {
		t.Fatalf("url = %q", server.URL)
	}
	if server.Headers["Authorization"] != "Bearer bili_run_token" {
		t.Fatalf("run token not passed to MCP: %v", server.Headers)
	}
	if invocation.Env["CLAUDE_CONFIG_DIR"] == "" {
		t.Fatal("Claude Code must run with an isolated config dir")
	}
}

func TestClaudeProviderMapping(t *testing.T) {
	// A bare provider name is not a base URL and must be ignored with a warning.
	request := testRequest()
	request.Provider = "anthropic"
	invocation, err := buildClaudeInvocation(testSettings(), request, "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := invocation.Env["ANTHROPIC_BASE_URL"]; ok {
		t.Fatal("bare provider name must not be used as ANTHROPIC_BASE_URL")
	}
	if len(invocation.Warnings) == 0 {
		t.Fatal("expected a warning about the ignored provider")
	}

	request.Provider = "https://gateway.example.com"
	invocation, err = buildClaudeInvocation(testSettings(), request, "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Env["ANTHROPIC_BASE_URL"] != "https://gateway.example.com" {
		t.Fatalf("base URL not applied: %v", invocation.Env)
	}
}

func TestRunUnknownBackend(t *testing.T) {
	if _, err := Run(t.Context(), testSettings(), "nope", testRequest()); err == nil {
		t.Fatal("expected an error for an unknown backend")
	}
}

func TestUsageFromJSONL(t *testing.T) {
	raw := []byte(strings.Join([]string{
		`{"type":"thread.started","thread_id":"x"}`,
		`{"type":"turn.completed","usage":{"input_tokens":100,"output_tokens":20,"cached_input_tokens":40}}`,
		`not json`,
	}, "\n"))
	usage := usageFromJSONL(raw)
	if usage["input_tokens"] != float64(100) || usage["output_tokens"] != float64(20) {
		t.Fatalf("unexpected usage: %v", usage)
	}
	if usage["cache_read_tokens"] != float64(40) {
		t.Fatalf("cache alias not mapped: %v", usage)
	}
}

func TestNormalizeUsageAliases(t *testing.T) {
	usage := normalizeUsage(map[string]any{
		"prompt_tokens":     float64(5),
		"completion_tokens": float64(7),
		"total_cost_usd":    float64(0.25),
	})
	if usage["input_tokens"] != float64(5) || usage["output_tokens"] != float64(7) {
		t.Fatalf("openai aliases not mapped: %v", usage)
	}
	if usage["cost_usd"] != float64(0.25) {
		t.Fatalf("cost alias not mapped: %v", usage)
	}
}

func TestAdminRouteNormalization(t *testing.T) {
	settings := model.DefaultAdminSettings()
	//lint:ignore SA1019 exercising the deprecated compatibility fields on purpose
	settings.CodexProvider = "deepseek"
	settings.CodexModel = "deepseek-chat"
	settings.Normalize()
	route := settings.RouteFor("codex")
	if route.Provider != "deepseek" || route.Model != "deepseek-chat" {
		t.Fatalf("legacy fields not folded: %+v", route)
	}

	// An explicit map entry wins over the legacy fields.
	settings.RunnerRoutes["codex"] = model.RunnerRoute{Provider: "openrouter", Model: "x"}
	//lint:ignore SA1019 exercising the deprecated compatibility fields on purpose
	settings.CodexProvider = "stale"
	settings.Normalize()
	if settings.RouteFor("codex").Provider != "openrouter" {
		t.Fatalf("map entry was overwritten: %+v", settings.RouteFor("codex"))
	}

	// Saving mirrors the map back so an older UI still reads it.
	settings.SyncLegacy()
	//lint:ignore SA1019 the mirror is the behaviour under test
	if settings.CodexProvider != "openrouter" || settings.CodexModel != "x" {
		t.Fatalf("legacy mirror not updated: %+v", settings)
	}
}
