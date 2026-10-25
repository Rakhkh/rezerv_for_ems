import pytest

from ..helpers.server import MockServer


@pytest.fixture
def api_token():
    return "integration-test-secret"


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
