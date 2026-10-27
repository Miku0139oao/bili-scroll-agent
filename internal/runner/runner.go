package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Miku0139oao/bili-scroll-agent/internal/config"
	"github.com/Miku0139oao/bili-scroll-agent/internal/model"
)

type Result struct {
	OK       bool
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
	Usage    map[string]any
	// Warnings records backend options that were accepted but not applied.
	Warnings []string
}

type Request struct {
	Run       model.Run
	Token     string
	Prompt    string
	Provider  string
	Model     string
	Reasoning string
}

func execute(
	ctx context.Context,
	settings *config.Settings,
	executable string,
	args []string,
	work string,
	env []string,
	stdin string,
) (Result, error) {
	runCtx, cancel := context.WithTimeout(ctx, settings.AgentTimeout)
	defer cancel()
	command := exec.CommandContext(runCtx, executable, args...)
	command.Dir = work
	command.Env = env
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &limitedWriter{Writer: &stdout, Limit: settings.AgentOutputLimit}
	command.Stderr = &limitedWriter{Writer: &stderr, Limit: settings.AgentOutputLimit}
	err := command.Run()
	result := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		OK:     err == nil,
	}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		result.TimedOut = true
		result.OK = false
		return result, nil
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return result, err
		}
	}
	return result, nil
}

type limitedWriter struct {
	Writer interface{ Write([]byte) (int, error) }
	Limit  int64
	used   int64
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	originalLength := len(p)
	if w.used >= w.Limit {
		return originalLength, nil
	}
	remaining := w.Limit - w.used
	if int64(len(p)) > remaining {
		p = p[:int(remaining)]
	}
	n, err := w.Writer.Write(p)
	w.used += int64(n)
	if err != nil {
		return n, err
	}
	return originalLength, nil
}

func ResolveExecutable(configured string, candidates []string) string {
	if configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			absolute, _ := filepath.Abs(configured)
			return absolute
		}
	}
	if found, err := exec.LookPath(configured); err == nil {
		absolute, _ := filepath.Abs(found)
		return absolute
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			absolute, _ := filepath.Abs(candidate)
			return absolute
		}
	}
	return ""
}

func codexCandidates() []string {
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		return []string{
			filepath.Join(local, "Programs", "OpenAI", "Codex", "bin", "codex.exe"),
			filepath.Join(local, "OpenAI", "Codex", "bin", "codex.exe"),
		}
	}
	return []string{"/usr/local/bin/codex", "/usr/bin/codex"}
}

func hermesCandidates() []string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		return []string{
			filepath.Join(local, "hermes", "bin", "hermes.exe"),
			filepath.Join(home, ".local", "bin", "hermes.exe"),
		}
	}
	return []string{
		filepath.Join(home, ".local", "bin", "hermes"),
		"/usr/local/bin/hermes",
		"/usr/bin/hermes",
	}
}

func codexHome(settings *config.Settings) string {
	if settings.CodexHome != "" {
		absolute, _ := filepath.Abs(settings.CodexHome)
		return absolute
	}
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	if info, err := os.Stat(filepath.Join(home, ".codex")); err == nil && info.IsDir() {
		return filepath.Join(home, ".codex")
	}
	return filepath.Join(settings.DataDir, "agents", "codex")
}

func hermesHome(settings *config.Settings) string {
	if settings.HermesHome != "" {
		absolute, _ := filepath.Abs(settings.HermesHome)
		return absolute
	}
	if value := os.Getenv("HERMES_HOME"); value != "" {
		return value
	}
	if runtime.GOOS == "windows" {
		candidate := filepath.Join(os.Getenv("LOCALAPPDATA"), "hermes")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	home, _ := os.UserHomeDir()
	candidate := filepath.Join(home, ".hermes")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return filepath.Join(settings.DataDir, "agents", "hermes")
}

func childEnv(overrides map[string]string) []string {
	env := append([]string(nil), os.Environ()...)
	index := make(map[string]int)
	for i, item := range env {
		key, _, _ := strings.Cut(item, "=")
		index[key] = i
	}
	for key, value := range overrides {
		if i, ok := index[key]; ok {
			env[i] = key + "=" + value
		} else {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func shortID(value string) string {
	if len(value) > 8 {
		return value[:8]
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func tomlEscape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`)
}

func usageFromJSONL(raw []byte) map[string]any {
	var combined map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64*1024), len(raw)+1)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if found := findUsage(event); found != nil {
			if combined == nil {
				combined = map[string]any{}
			}
			for key, value := range found {
				combined[key] = value
			}
		}
	}
	return combined
}

func findUsage(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", "cost_usd"} {
			if _, ok := typed[key]; ok {
				return normalizeUsage(typed)
			}
		}
		for _, child := range typed {
			if found := findUsage(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findUsage(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func normalizeUsage(value map[string]any) map[string]any {
	if nested, ok := value["usage"].(map[string]any); ok {
		value = nested
	}
	return map[string]any{
		"input_tokens":          number(value, "input_tokens", "prompt_tokens", "inputTokens"),
		"output_tokens":         number(value, "output_tokens", "completion_tokens", "outputTokens"),
		"cache_read_tokens":     number(value, "cache_read_tokens", "cached_input_tokens", "cacheReadTokens"),
		"cache_creation_tokens": number(value, "cache_creation_tokens", "cacheCreationTokens"),
		"cost_usd":              number(value, "estimated_cost_usd", "cost_usd", "total_cost_usd"),
		"total_tokens":          number(value, "total_tokens", "totalTokens"),
	}
}

func number(value map[string]any, keys ...string) float64 {
	for _, key := range keys {
		switch typed := value[key].(type) {
		case float64:
			return typed
		case json.Number:
			result, _ := typed.Float64()
			return result
		case int:
			return float64(typed)
		case int64:
			return float64(typed)
		}
	}
	return 0
}
