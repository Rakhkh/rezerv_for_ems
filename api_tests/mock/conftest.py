import pytest

from ..helpers.checks import check_no_token, check_trace
from ..helpers.server import MockServer


@pytest.fixture
def api_token():
    return "integration-test-secret"


@pytest.fixture
def make_issue():
    def make(number, title, created_at="2026-01-01T00:00:00Z"):
        return {
            "number": number, "title": title, "user": {"login": "author"},
            "labels": [], "created_at": created_at,
            "html_url": f"https://example.com/issue/{number}",
        }

    return make


@pytest.fixture
def read_trace(work_dir, api_token):
    def read(server, start=0):
        text = (work_dir / "trace.jsonl").read_text(encoding="utf-8")
        check_no_token(api_token, text)
        return check_trace(text, server.base_url, server.requests[start:])

    return read


@pytest.fixture
def mock_server(request):
    def create(handler):
        server = MockServer(handler)

        def cleanup():
            server.close()
            server.assert_ok()

        request.addfinalizer(cleanup)
        return server

    return create
