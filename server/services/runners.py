from __future__ import annotations

import asyncio
import json
import os
import shutil
import signal
import tempfile
from dataclasses import dataclass
from pathlib import Path

from ..config import Settings
from ..models import Run


@dataclass(slots=True)
class RunnerResult:
    ok: bool
    exit_code: int | None
    stdout: str
    stderr: str
    timed_out: bool = False
    usage: dict | None = None


class RunnerUnavailable(RuntimeError):
    pass


class AgentRunner:
    name = "base"

    def __init__(self, settings: Settings):
        self.settings = settings

    def available(self) -> bool:
        raise NotImplementedError

    async def run(
        self,
        *,
        run: Run,
        token: str,
        prompt: str,
        provider: str = "",
        model: str = "",
        reasoning: str = "",
    ) -> RunnerResult:
        raise NotImplementedError

    async def _execute(
        self,
        command: list[str],
        *,
        env: dict[str, str],
        cwd: Path,
        prompt: str,
    ) -> RunnerResult:
        process = await asyncio.create_subprocess_exec(
            *command,
            cwd=str(cwd),
            env=env,
            stdin=asyncio.subprocess.PIPE,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            start_new_session=True,
        )
        try:
            stdout, stderr = await asyncio.wait_for(
                process.communicate(prompt.encode("utf-8")),
                timeout=self.settings.agent_timeout_seconds,
            )
            usage = _usage_from_jsonl(stdout)
            return RunnerResult(
                ok=process.returncode == 0,
                exit_code=process.returncode,
                stdout=_clip(stdout, self.settings.agent_output_limit_bytes),
                stderr=_clip(stderr, self.settings.agent_output_limit_bytes),
                usage=usage,
            )
        except TimeoutError:
            try:
                if os.name == "nt":
                    process.terminate()
                else:
                    os.killpg(process.pid, signal.SIGTERM)
                await asyncio.sleep(2)
                if process.returncode is None:
                    if os.name == "nt":
                        process.kill()
                    else:
                        os.killpg(process.pid, signal.SIGKILL)
            except (OSError, ProcessLookupError, PermissionError):
                pass
            stdout, stderr = await process.communicate()
            return RunnerResult(
                ok=False,
                exit_code=process.returncode,
                stdout=_clip(stdout, self.settings.agent_output_limit_bytes),
                stderr=_clip(stderr, self.settings.agent_output_limit_bytes),
                timed_out=True,
            )


class CodexRunner(AgentRunner):
    name = "codex"

    def available(self) -> bool:
        return bool(shutil.which(self.settings.codex_bin))

    async def run(
        self,
        *,
        run: Run,
        token: str,
        prompt: str,
        provider: str = "",
        model: str = "",
        reasoning: str = "",
    ) -> RunnerResult:
        executable = shutil.which(self.settings.codex_bin)
        if not executable:
            raise RunnerUnavailable("codex executable not found")
        home = self.settings.agent_home / "codex"
        home.mkdir(parents=True, exist_ok=True)
        work = Path(tempfile.mkdtemp(prefix=f"bili-scroll-{run.id[:8]}-", dir=home))
        env = {
            **os.environ,
            "CODEX_HOME": str(home),
            "BILI_SCROLL_RUN_TOKEN": token,
            "BILI_SCROLL_TOKEN": token,
            "BILI_SCROLL_API_URL": f"http://127.0.0.1:{self.settings.port}",
        }
        command = [
            executable,
            "exec",
            "--ephemeral",
            "--skip-git-repo-check",
            "-C",
            str(work),
            "-c",
            'approval_policy="never"',
            "-c",
            'sandbox_mode="read-only"',
            "--json",
            "-",
        ]
        if provider:
            command[command.index("--json") : command.index("--json")] = [
                "-c",
                f'model_provider="{_toml_escape(provider)}"',
            ]
        if model:
            command[command.index("--json") : command.index("--json")] = [
                "-m",
                model,
            ]
        if reasoning:
            command[command.index("--json") : command.index("--json")] = [
                "-c",
                f'model_reasoning_effort="{_toml_escape(reasoning)}"',
            ]
        return await self._execute(command, env=env, cwd=work, prompt=prompt)


class HermesRunner(AgentRunner):
    name = "hermes"

    def available(self) -> bool:
        return bool(shutil.which(self.settings.hermes_bin))

    async def run(
        self,
        *,
        run: Run,
        token: str,
        prompt: str,
        provider: str = "",
        model: str = "",
        reasoning: str = "",
    ) -> RunnerResult:
        executable = shutil.which(self.settings.hermes_bin)
        if not executable:
            raise RunnerUnavailable("hermes executable not found")
        home = self.settings.agent_home / "hermes"
        home.mkdir(parents=True, exist_ok=True)
        work = Path(tempfile.mkdtemp(prefix=f"bili-scroll-{run.id[:8]}-", dir=home))
        prompt_file = work / "prompt.txt"
        prompt_file.write_text(prompt, encoding="utf-8")
        env = {
            **os.environ,
            "HERMES_HOME": str(home),
            "BILI_SCROLL_RUN_TOKEN": token,
            "BILI_SCROLL_TOKEN": token,
            "BILI_SCROLL_API_URL": f"http://127.0.0.1:{self.settings.port}",
        }
        command = [
            executable,
            "chat",
            "--oneshot",
            "--quiet",
            "--query-file",
            str(prompt_file),
            "--source",
            "tool",
            "--usage-file",
            str(work / "usage.json"),
        ]
        if provider:
            command.extend(["--provider", provider])
        if model:
            command.extend(["--model", model])
        result = await self._execute(command, env=env, cwd=work, prompt="")
        usage_path = work / "usage.json"
        if usage_path.exists():
            try:
                result.usage = _normalize_usage(json.loads(usage_path.read_text(encoding="utf-8")))
            except (OSError, ValueError):
                pass
        return result


def runner_for(settings: Settings, name: str) -> AgentRunner:
    if name == "codex":
        return CodexRunner(settings)
    if name == "hermes":
        return HermesRunner(settings)
    raise RunnerUnavailable(f"unknown runner: {name}")


def runner_status(settings: Settings) -> dict[str, dict]:
    return {
        name: {
            "available": runner_for(settings, name).available(),
            "command": getattr(settings, f"{name}_bin"),
        }
        for name in ("codex", "hermes")
    }


def _clip(value: bytes, limit: int) -> str:
    return value[:limit].decode("utf-8", errors="replace")


def _usage_from_jsonl(value: bytes) -> dict | None:
    best: dict | None = None
    for raw_line in value.splitlines():
        line = raw_line.strip()
        if not line.startswith(b"{"):
            continue
        try:
            event = json.loads(line)
        except ValueError:
            continue
        usage = _find_usage(event)
        if usage:
            best = {**(best or {}), **usage}
    return best


def _find_usage(value) -> dict | None:  # noqa: ANN001
    if isinstance(value, dict):
        keys = {"input_tokens", "output_tokens", "total_tokens", "cost_usd"}
        if keys & set(value):
            return _normalize_usage(value)
        for child in value.values():
            found = _find_usage(child)
            if found:
                return found
    elif isinstance(value, list):
        for child in value:
            found = _find_usage(child)
            if found:
                return found
    return None


def _normalize_usage(value: dict) -> dict:
    source = value.get("total_including_auxiliary") or value.get("usage") or value
    if not isinstance(source, dict):
        source = value
    return {
        "input_tokens": int(
            source.get("input_tokens") or source.get("prompt_tokens") or source.get("inputTokens") or 0
        ),
        "output_tokens": int(
            source.get("output_tokens") or source.get("completion_tokens") or source.get("outputTokens") or 0
        ),
        "cache_read_tokens": int(
            source.get("cache_read_tokens")
            or source.get("cached_input_tokens")
            or source.get("cacheReadTokens")
            or 0
        ),
        "cache_creation_tokens": int(
            source.get("cache_creation_tokens") or source.get("cacheCreationTokens") or 0
        ),
        "cost_usd": float(
            source.get("estimated_cost_usd") or source.get("cost_usd") or source.get("total_cost_usd") or 0
        ),
        "total_tokens": int(source.get("total_tokens") or source.get("totalTokens") or 0),
    }


def _toml_escape(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"')
