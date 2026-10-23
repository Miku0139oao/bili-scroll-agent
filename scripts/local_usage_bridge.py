#!/usr/bin/env python3
"""Push cc-switch usage and DeepSeek dashboard balances to Bili Scroll Agent.

This script is designed to run on the machine that owns cc-switch. It reads the
cc-switch SQLite database in read-only mode and never writes provider secrets.
"""

from __future__ import annotations

import argparse
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import UTC, date, datetime, timedelta
from pathlib import Path


def request_json(
    url: str,
    *,
    token: str,
    method: str = "GET",
    body: dict | None = None,
) -> dict:
    data = json.dumps(body).encode("utf-8") if body is not None else None
    request = urllib.request.Request(
        url,
        data=data,
        method=method,
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
            "User-Agent": "bili-scroll-local-bridge/0.1",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        text = exc.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {exc.code}: {text[:500]}") from exc


def read_cc_switch(path: Path, days: int) -> list[dict]:
    if not path.exists():
        raise RuntimeError(f"cc-switch database not found: {path}")
    since = (date.today() - timedelta(days=days - 1)).isoformat()
    connection = sqlite3.connect(f"file:{path.as_posix()}?mode=ro", uri=True)
    connection.row_factory = sqlite3.Row
    try:
        rows = connection.execute(
            """
            SELECT date, app_type, provider_id, model, request_count, success_count,
                   input_tokens, output_tokens, cache_read_tokens,
                   cache_creation_tokens, total_cost_usd, avg_latency_ms
            FROM usage_daily_rollups
            WHERE date >= ? AND app_type IN ('codex', 'hermes')
            ORDER BY date
            """,
            (since,),
        ).fetchall()
    finally:
        connection.close()
    return [
        {
            "date": row["date"],
            "app_type": row["app_type"],
            "provider_id": row["provider_id"] or "",
            "model": row["model"] or "",
            "request_count": int(row["request_count"] or 0),
            "success_count": int(row["success_count"] or 0),
            "input_tokens": int(row["input_tokens"] or 0),
            "output_tokens": int(row["output_tokens"] or 0),
            "cache_read_tokens": int(row["cache_read_tokens"] or 0),
            "cache_creation_tokens": int(row["cache_creation_tokens"] or 0),
            "total_cost_usd": float(row["total_cost_usd"] or 0),
            "avg_latency_ms": float(row["avg_latency_ms"] or 0),
        }
        for row in rows
    ]


def read_balance(url: str) -> dict:
    query = urllib.parse.urlencode({"threshold": "10"})
    with urllib.request.urlopen(f"{url.rstrip('/')}/api/balance?{query}", timeout=30) as response:
        return json.loads(response.read().decode("utf-8"))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--server",
        default=os.environ.get("BILI_SCROLL_SERVER", "http://127.0.0.1:8788"),
    )
    parser.add_argument("--token", default=os.environ.get("BILI_SCROLL_TOKEN", ""))
    parser.add_argument(
        "--cc-switch-db",
        default=str(Path.home() / ".cc-switch" / "cc-switch.db"),
    )
    parser.add_argument(
        "--balance-dashboard",
        default=os.environ.get("DEEPSEEK_DASHBOARD_URL"),
        help="Optional legacy dashboard URL. Native Bili Scroll balance does not need this.",
    )
    parser.add_argument("--days", type=int, default=30)
    parser.add_argument("--skip-usage", action="store_true")
    args = parser.parse_args()
    if not args.token:
        print("Missing BILI_SCROLL_TOKEN. Create a device token in the Bili Scroll WebUI.", file=sys.stderr)
        return 2

    try:
        if not args.skip_usage:
            rows = read_cc_switch(Path(args.cc_switch_db), max(1, min(args.days, 365)))
            result = request_json(
                f"{args.server.rstrip('/')}/api/ingest/usage",
                token=args.token,
                method="POST",
                body={"source": "cc-switch", "rows": rows},
            )
            print(f"cc-switch usage: {result.get('updated', 0)} rows")
        if args.balance_dashboard:
            payload = read_balance(args.balance_dashboard)
            balances = payload.get("balances") or []
            request_json(
                f"{args.server.rstrip('/')}/api/ingest/balance",
                token=args.token,
                method="POST",
                body={
                    "provider_id": "deepseek",
                    "available": bool(payload.get("available", True)),
                    "fetched_at": payload.get("fetchedAt") or datetime.now(UTC).isoformat(),
                    "low_threshold": float(payload.get("threshold") or 10),
                    "source": "deepseek-balance-dashboard",
                    "balances": [
                        {
                            "currency": item.get("currency") or "CNY",
                            "total": float(item.get("total") or 0),
                            "granted": float(item.get("granted") or 0),
                            "topped_up": float(item.get("toppedUp") or 0),
                            "low": bool(item.get("low")),
                        }
                        for item in balances
                    ],
                },
            )
            print(f"DeepSeek balance: {len(balances)} currencies")
    except Exception as exc:  # noqa: BLE001
        print(f"Sync failed: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
