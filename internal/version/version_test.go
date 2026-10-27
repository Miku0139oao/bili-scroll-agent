package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestVersionsAreConsistent keeps the Go constant, the plugin manifests, and the
// WebUI package in lockstep. Bump version.Version and these files together.
func TestVersionsAreConsistent(t *testing.T) {
	root := filepath.Join("..", "..")
	cases := []struct {
		path string
		key  string
	}{
		{"plugins/bili-scroll-agent/plugin.json", "version"},
		{"plugins/bili-scroll-agent/.codex-plugin/plugin.json", "version"},
		{"web/package.json", "version"},
	}
	for _, item := range cases {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(item.path)))
		if err != nil {
			t.Fatalf("%s: %v", item.path, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("%s: %v", item.path, err)
		}
		if got := payload[item.key]; got != Version {
			t.Fatalf("%s version = %v, want %s", item.path, got, Version)
		}
	}
}

func TestUserAgent(t *testing.T) {
	if UserAgent() != "bili-scroll-agent/"+Version {
		t.Fatalf("unexpected user agent: %s", UserAgent())
	}
	if String() != Version+Suffix {
		t.Fatalf("unexpected version string: %s", String())
	}
}
