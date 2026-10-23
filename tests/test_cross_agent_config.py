from __future__ import annotations

import json
import tomllib
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent


def test_cc_switch_mcp_config_is_portable():
    payload = json.loads(
        (ROOT / "integrations" / "cc-switch" / "mcp-servers.json").read_text(encoding="utf-8")
    )
    server = payload["mcpServers"]["bili-scroll"]
    assert server["type"] == "http"
    assert server["url"].endswith("/api/mcp")


def test_codex_and_hermes_examples_parse():
    codex = tomllib.loads(
        (ROOT / "integrations" / "cc-switch" / "codex.toml.example").read_text(encoding="utf-8")
    )
    hermes = yaml.safe_load(
        (ROOT / "integrations" / "cc-switch" / "hermes.yaml.example").read_text(encoding="utf-8")
    )
    assert codex["mcp_servers"]["bili-scroll"]["url"].endswith("/api/mcp")
    assert hermes["mcp_servers"]["bili-scroll"]["url"].endswith("/api/mcp")
    assert hermes["mcp_servers"]["bili-scroll"]["trust"] == "untrusted"


def test_shared_skill_has_standard_frontmatter():
    text = (ROOT / "plugins" / "bili-scroll-agent" / "skills" / "bili-scroll" / "SKILL.md").read_text(
        encoding="utf-8"
    )
    assert text.startswith("---\n")
    frontmatter = yaml.safe_load(text.split("---", 2)[1])
    assert frontmatter["name"] == "bili-scroll"
    assert "Bilibili" in frontmatter["description"]
