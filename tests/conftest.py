from __future__ import annotations

import base64
import os
import secrets
import tempfile
from pathlib import Path

import pytest

_test_root = Path(tempfile.mkdtemp(prefix="bili-scroll-tests-"))
os.environ["BILI_SCROLL_ENVIRONMENT"] = "test"
os.environ["BILI_SCROLL_DATA_DIR"] = str(_test_root)
os.environ["BILI_SCROLL_MASTER_KEY"] = base64.b64encode(secrets.token_bytes(32)).decode()
os.environ["BILI_SCROLL_SECURE_COOKIES"] = "false"
os.environ["BILI_SCROLL_PUBLIC_URL"] = "http://testserver"
os.environ["BILI_SCROLL_PORT"] = "8788"


@pytest.fixture(scope="session")
def client():
    from fastapi.testclient import TestClient

    from server.main import app

    with TestClient(app) as test_client:
        yield test_client
