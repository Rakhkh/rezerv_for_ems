import pytest

from ..helpers.checks import (
    check_generated_at,
    check_json_no_token,
    check_no_token,
    check_report,
    json_object,
)
from ..helpers.raw_server import RawTransportServer


@pytest.fixture
def raw_transport_server(api_token):
    server = RawTransportServer(api_token)
    try:
        yield server
    finally:
        server.close()
        server.assert_ok()


def test_transport_diagnostics_do_not_expose_peer_bytes(
    raw_transport_server, run_ghreport, read_trace, api_token, cache_dir
):
    server = raw_transport_server
    result = run_ghreport(
        "--repos", "stub/first,stub/second", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    check_no_token(api_token, result.stdout, result.stderr)
    assert result.returncode == 0 and result.stderr == ""
    assert server.first_connection_closed.is_set(), "Extra peer bytes were not processed"
    assert len(server.requests) == 2
    report = json_object(result.stdout)
    check_no_token(api_token, report)
    check_report(report, ["stub/first", "stub/second"])
    check_generated_at(report, result)
    assert report["repos"] == [
        {"repo": repo, "error": None, "count": 0, "issues": []}
        for repo in ("stub/first", "stub/second")
    ]
    records = read_trace(server)
    assert [(record["status"], record["attempt"], record["from_cache"], record["error"])
            for record in records] == [(200, 1, False, None), (200, 1, False, None)]
    for path in cache_dir.iterdir():
        check_json_no_token(api_token, path.read_text(encoding="utf-8"))
