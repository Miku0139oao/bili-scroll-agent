package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
)

// Invocation is everything a backend contributes to one subprocess call.
type Invocation struct {
	Args  []string
	Stdin string
	// Files are written 0600 into the per-run work directory before execution.
	Files map[string]string
	// Env is merged over the run-token environment.
	Env map[string]string
	// Usage is called after the process exits, with the work directory still
	// present, so a backend can read a usage file it asked the CLI to write.
	Usage func(work string, result *Result)
	// Warnings are surfaced in run telemetry (e.g. an ignored option).
	Warnings []string
}

// Backend describes one agent CLI.
//
// A backend can only finish a run if its CLI can call the service's HTTP MCP
// endpoint and invoke bili_finish_run / bili_finish_dm_reply. There is
// deliberately no stdout-based completion path: that would let an agent claim
// success without the candidate, tier, and quota validation in finishRun.
type Backend struct {
	Name string
	// Label is the human-facing name used by the UI.
	Label string
	// Bin reads the configured executable from settings.
	Bin func(*config.Settings) string
	// Candidates are fallback install paths probed when the configured binary
	// is not found on PATH.
	Candidates func(*config.Settings) []string
	// HomeEnv is the environment variable that relocates the CLI's config dir.
	HomeEnv string
	// HomeFor resolves the isolated config directory for a run.
	HomeFor func(*config.Settings) string
	// Build produces the argv, files, and env for one run.
	Build func(*config.Settings, Request, string) (Invocation, error)
	// Probe optionally checks CLI capabilities beyond "binary exists".
	Probe func(executable string) (string, bool)
}

var (
	backendsOnce sync.Once
	backendList  []*Backend
)

// All returns every registered backend in display order.
func All() []*Backend {
	backendsOnce.Do(func() {
		backendList = []*Backend{codexBackend(), hermesBackend(), claudeBackend()}
	})
	return backendList
}

// Lookup finds a backend by name.
func Lookup(name string) (*Backend, bool) {
	for _, backend := range All() {
		if backend.Name == name {
			return backend, true
		}
	}
	return nil, false
}

// Names returns the registered backend names.
func Names() []string {
	result := make([]string, 0, len(All()))
	for _, backend := range All() {
		result = append(result, backend.Name)
	}
	return result
}

// Info describes a backend's availability for the UI.
type Info struct {
	Available bool   `json:"available"`
	Command   string `json:"command"`
	Home      string `json:"home"`
	Label     string `json:"label"`
	Reason    string `json:"reason,omitempty"`
}

// Status reports every registered backend.
func Status(settings *config.Settings) map[string]Info {
	result := make(map[string]Info, len(All()))
	for _, backend := range All() {
		result[backend.Name] = inspect(settings, backend)
	}
	return result
}

func inspect(settings *config.Settings, backend *Backend) Info {
	info := Info{Label: backend.Label}
	if backend.HomeFor != nil {
		info.Home = backend.HomeFor(settings)
	}
	executable := ResolveExecutable(backend.Bin(settings), backend.Candidates(settings))
	if executable == "" {
		info.Reason = "not installed"
		return info
	}
	info.Available = true
	info.Command = executable
	if backend.Probe != nil {
		if reason, ok := backend.Probe(executable); !ok {
			info.Available = false
			info.Reason = reason
		}
	}
	return info
}

func codexBackend() *Backend {
	return &Backend{
		Name:  "codex",
		Label: "Codex",
		Bin:   func(settings *config.Settings) string { return settings.CodexBin },
		Candidates: func(*config.Settings) []string {
			return codexCandidates()
		},
		HomeEnv: "CODEX_HOME",
		HomeFor: codexHome,
		Build:   buildCodexInvocation,
	}
}

func hermesBackend() *Backend {
	return &Backend{
		Name:  "hermes",
		Label: "Hermes Agent",
		Bin:   func(settings *config.Settings) string { return settings.HermesBin },
		Candidates: func(*config.Settings) []string {
			return hermesCandidates()
		},
		HomeEnv: "HERMES_HOME",
		HomeFor: hermesHome,
		Build:   buildHermesInvocation,
	}
}

func buildCodexInvocation(settings *config.Settings, request Request, work string) (Invocation, error) {
	args := []string{
		"exec", "--ephemeral", "--skip-git-repo-check", "-C", work,
		"-c", `approval_policy="never"`,
		"--disable", "plugins",
		"-c", `sandbox_mode="read-only"`,
		"-c", `mcp_servers.bili_scroll.url="http://127.0.0.1:` + fmt.Sprint(settings.Port) + `/api/mcp"`,
		"-c", `mcp_servers.bili_scroll.bearer_token_env_var="BILI_SCROLL_TOKEN"`,
		"-c", `mcp_servers.bili_scroll.default_tools_approval_mode="auto"`,
		"-c", `mcp_servers.bili_scroll.startup_timeout_sec=20`,
		"-c", `mcp_servers.bili_scroll.tool_timeout_sec=300`,
	}
	if request.Provider != "" {
		args = append(args, "-c", fmt.Sprintf(`model_provider="%s"`, tomlEscape(request.Provider)))
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if request.Reasoning != "" {
		args = append(args, "-c", fmt.Sprintf(
			`model_reasoning_effort="%s"`, tomlEscape(request.Reasoning)))
	}
	args = append(args, "--json", "-")
	return Invocation{
		Args:  args,
		Stdin: request.Prompt,
		Env: map[string]string{
			"CODEX_HOME": codexHome(settings),
		},
		Usage: func(_ string, result *Result) {
			result.Usage = usageFromJSONL([]byte(result.Stdout))
		},
	}, nil
}

func buildHermesInvocation(settings *config.Settings, request Request, work string) (Invocation, error) {
	promptFile := filepath.Join(work, "prompt.txt")
	usageFile := filepath.Join(work, "usage.json")
	args := []string{
		"--usage-file", usageFile,
		"chat", "--oneshot", "--quiet",
		"--query-file", promptFile,
		"--source", "tool",
	}
	if request.Provider != "" {
		args = append(args, "--provider", request.Provider)
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	if request.Reasoning != "" {
		args = append(args, "--reasoning", request.Reasoning)
	}
	return Invocation{
		Args:  args,
		Files: map[string]string{"prompt.txt": request.Prompt},
		Env:   map[string]string{"HERMES_HOME": hermesHome(settings)},
		Usage: func(work string, result *Result) {
			raw, err := os.ReadFile(filepath.Join(work, "usage.json"))
			if err != nil {
				return
			}
			var usage map[string]any
			if json.Unmarshal(raw, &usage) == nil {
				result.Usage = normalizeUsage(usage)
			}
		},
	}, nil
}

// claudeBackend runs Claude Code in non-interactive print mode and points it at
// the service's MCP endpoint through a per-run config file.
func claudeBackend() *Backend {
	return &Backend{
		Name:  "claude",
		Label: "Claude Code",
		Bin:   func(settings *config.Settings) string { return settings.ClaudeBin },
		Candidates: func(*config.Settings) []string {
			return claudeCandidates()
		},
		HomeEnv: "CLAUDE_CONFIG_DIR",
		HomeFor: claudeHome,
		Build:   buildClaudeInvocation,
		Probe:   probeClaude,
	}
}

func buildClaudeInvocation(settings *config.Settings, request Request, work string) (Invocation, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/api/mcp", settings.Port)
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"bili_scroll": map[string]any{
				"type":    "http",
				"url":     url,
				"headers": map[string]string{"Authorization": "Bearer " + request.Token},
			},
		},
	}
	encoded, err := json.MarshalIndent(mcpConfig, "", "  ")
	if err != nil {
		return Invocation{}, err
	}
	args := []string{
		"-p", request.Prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--mcp-config", filepath.Join(work, "mcp.json"),
		"--strict-mcp-config",
		"--allowedTools", "mcp__bili_scroll",
	}
	if request.Model != "" {
		args = append(args, "--model", request.Model)
	}
	env := map[string]string{
		"CLAUDE_CONFIG_DIR":     claudeHome(settings),
		"BILI_SCROLL_RUN_TOKEN": request.Token,
		"BILI_SCROLL_TOKEN":     request.Token,
		"BILI_SCROLL_API_URL":   fmt.Sprintf("http://127.0.0.1:%d", settings.Port),
	}
	invocation := Invocation{
		Args:  args,
		Files: map[string]string{"mcp.json": string(encoded)},
		Env:   env,
		Usage: func(_ string, result *Result) {
			result.Usage = usageFromJSONL([]byte(result.Stdout))
		},
	}
	// Claude Code routes through its own provider config; a provider name is
	// mapped onto ANTHROPIC_BASE_URL only when it is a valid base URL.
	if request.Provider != "" {
		if _, err := validateBaseURL(request.Provider); err == nil {
			invocation.Env["ANTHROPIC_BASE_URL"] = request.Provider
		} else {
			invocation.Warnings = append(invocation.Warnings,
				"provider ignored: set a full base URL (ANTHROPIC_BASE_URL) for the claude backend")
		}
	}
	if request.Reasoning != "" {
		invocation.Warnings = append(invocation.Warnings,
			"reasoning is not supported by the claude backend")
	}
	return invocation, nil
}

// probeClaude rejects a Claude Code build that lacks the flags this backend
// depends on. CLI flags move between releases, so an availability check that
// only looked for the binary would fail at run time instead.
func probeClaude(executable string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*1e9)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		return "could not read claude --help", false
	}
	help := string(output)
	for _, flag := range []string{"--mcp-config", "--strict-mcp-config", "--output-format"} {
		if !strings.Contains(help, flag) {
			return "missing " + flag, false
		}
	}
	return "", true
}

func claudeCandidates() []string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		return []string{
			filepath.Join(local, "Programs", "claude", "claude.exe"),
			filepath.Join(home, ".local", "bin", "claude.exe"),
		}
	}
	return []string{
		filepath.Join(home, ".local", "bin", "claude"),
		"/usr/local/bin/claude",
		"/usr/bin/claude",
	}
}

func claudeHome(settings *config.Settings) string {
	if settings.ClaudeHome != "" {
		absolute, _ := filepath.Abs(settings.ClaudeHome)
		return absolute
	}
	return filepath.Join(settings.DataDir, "agents", "claude")
}

// Run executes the named backend for one run.
func Run(ctx context.Context, settings *config.Settings, name string, request Request) (Result, error) {
	backend, ok := Lookup(name)
	if !ok {
		return Result{}, errors.New("unknown runner: " + name)
	}
	return runBackend(ctx, settings, backend, request)
}

func runBackend(
	ctx context.Context,
	settings *config.Settings,
	backend *Backend,
	request Request,
) (Result, error) {
	executable := ResolveExecutable(backend.Bin(settings), backend.Candidates(settings))
	if executable == "" {
		return Result{}, fmt.Errorf("%s executable not found", backend.Name)
	}
	work, err := os.MkdirTemp("", "bili-scroll-"+shortID(request.Run.ID)+"-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(work)
	home := backend.HomeFor(settings)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return Result{}, err
	}
	invocation, err := backend.Build(settings, request, work)
	if err != nil {
		return Result{}, err
	}
	for name, content := range invocation.Files {
		target := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
			return Result{}, err
		}
	}
	env := map[string]string{
		"BILI_SCROLL_RUN_TOKEN": request.Token,
		"BILI_SCROLL_TOKEN":     request.Token,
		"BILI_SCROLL_API_URL":   fmt.Sprintf("http://127.0.0.1:%d", settings.Port),
	}
	for key, value := range invocation.Env {
		env[key] = value
	}
	result, err := execute(ctx, settings, executable, invocation.Args, work, childEnv(env), invocation.Stdin)
	if err != nil {
		return result, err
	}
	if invocation.Usage != nil {
		invocation.Usage(work, &result)
	}
	if len(invocation.Warnings) > 0 {
		result.Warnings = invocation.Warnings
	}
	return result, nil
}

// validateBaseURL is a small local check so the runner package does not import
// internal/provider (which would pull the JS runtime into the CLI binaries).
func validateBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "https://") && !strings.HasPrefix(trimmed, "http://") {
		return "", errors.New("not an http(s) URL")
	}
	return trimmed, nil
}
