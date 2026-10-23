from __future__ import annotations

import asyncio
import json
from collections import defaultdict
from collections.abc import AsyncIterator
from typing import Any


class EventHub:
    def __init__(self) -> None:
        self._subscribers: dict[str, set[asyncio.Queue]] = defaultdict(set)

    async def publish(self, user_id: str, event: str, data: dict[str, Any]) -> None:
        payload = {"event": event, "data": data}
        for queue in list(self._subscribers[user_id]):
            try:
                queue.put_nowait(payload)
            except asyncio.QueueFull:
                pass

    async def stream(self, user_id: str) -> AsyncIterator[str]:
        queue: asyncio.Queue = asyncio.Queue(maxsize=32)
        self._subscribers[user_id].add(queue)
        try:
            yield "event: ready\ndata: {}\n\n"
            while True:
                try:
                    item = await asyncio.wait_for(queue.get(), timeout=20)
                    yield (
                        f"event: {item['event']}\ndata: {json.dumps(item['data'], ensure_ascii=False)}\n\n"
                    )
                except TimeoutError:
                    yield ": keepalive\n\n"
        finally:
            self._subscribers[user_id].discard(queue)
