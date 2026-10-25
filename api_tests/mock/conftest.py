import pytest

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
def mock_server(request):
    def create(handler):
        server = MockServer(handler)

        def cleanup():
            server.close()
            server.assert_ok()

        request.addfinalizer(cleanup)
        return server

    return create
