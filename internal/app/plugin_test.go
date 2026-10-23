package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortablePluginAndLocalMCPManifest(t *testing.T) {
	root := filepath.Join("..", "..", "plugins", "bili-scroll-agent")
	raw, err := os.ReadFile(filepath.Join(root, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["$schema"] != "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json" {
		t.Fatalf("unexpected plugin schema: %v", manifest["$schema"])
	}
	if manifest["name"] != "bili-scroll-agent" {
		t.Fatalf("unexpected plugin name: %v", manifest["name"])
	}
	raw, err = os.ReadFile(filepath.Join(root, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mcpConfig struct {
		Schema  string `json:"$schema"`
		Servers map[string]struct {
			Type    string `json:"type"`
			Command string `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &mcpConfig); err != nil {
		t.Fatal(err)
	}
	server, ok := mcpConfig.Servers["bili-scroll"]
	if !ok || server.Type != "stdio" || server.Command == "" {
		t.Fatalf("invalid local MCP server manifest: %+v", mcpConfig)
	}
	executable := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(server.Command, "./")))
	if info, err := os.Stat(executable); err != nil || info.IsDir() {
		t.Fatalf("local MCP binary missing at %s", executable)
	}
}

func TestCaddyConfigKeepsStreamingAndTLSBoundary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", "Caddyfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{
		"Strict-Transport-Security",
		"reverse_proxy 127.0.0.1:8788",
		"flush_interval -1",
		"request_body",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Caddyfile missing %q", expected)
		}
	}
}
