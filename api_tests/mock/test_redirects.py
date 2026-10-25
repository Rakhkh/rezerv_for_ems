from urllib.parse import urlsplit

import pytest

from ..helpers.checks import check_no_token, check_report, json_object
from ..helpers.server import Response


@pytest.mark.parametrize("status", [301, 302, 307])
def test_redirect_and_relative_pagination_between_processes(
    status, mock_server, run_ghreport, make_issue, read_trace, api_token
):
    def respond(request):
        assert request.method == "GET"
        assert request.headers.get("authorization") == "Bearer " + api_token
        assert request.headers.get("accept") == "application/vnd.github+json"
        assert request.headers.get("x-github-api-version") == "2026-03-10"
        assert "ghreport" in request.headers.get("user-agent", "").lower()
        assert "if-none-match" not in request.headers
        if urlsplit(request.target).path == "/repos/stub/alpha/issues":
            return Response(status, headers={"Location": "/moved/issues?cursor=start"})
        if request.target == "/moved/issues?cursor=start":
            return Response.json([make_issue(1, "first")], headers={
                "Link": '<?cursor=next>; rel="next"',
            })
        if request.target == "/moved/issues?cursor=next":
            return Response.json([make_issue(2, "second", "2026-01-02T00:00:00Z")])
        raise AssertionError(f"Unexpected redirect/pagination URL: {request.target}")

    server = mock_server(respond)
    previous = None
    for run in range(2):
        start = len(server.requests)
        result = run_ghreport(
            "--repos", "stub/alpha", "--trace", "trace.jsonl",
            base_url=server.base_url, token=api_token,
        )
        assert result.returncode == 0 and result.stderr == ""
        check_no_token(api_token, result.stdout, result.stderr)
        report = json_object(result.stdout)
        check_report(report, ["stub/alpha"])
        assert report["repos"][0]["error"] is None
        assert [item["number"] for item in report["repos"][0]["issues"]] == [1, 2]
        comparable = {key: value for key, value in report.items() if key != "generated_at"}
        if previous is not None:
            assert comparable == previous
        previous = comparable
        requests = server.requests[start:]
        expected_targets = ["/moved/issues?cursor=start", "/moved/issues?cursor=next"]
        statuses = [200, 200]
        if run == 0 or status != 301:
            assert urlsplit(requests[0].target).path == "/repos/stub/alpha/issues"
            expected_targets.insert(0, requests[0].target)
            statuses.insert(0, status)
        assert [request.target for request in requests] == expected_targets
        records = read_trace(server, start)
        assert [record["status"] for record in records] == statuses
        assert all(record["attempt"] == 1 and record["error"] is None
                   and record["from_cache"] is False for record in records)
    assert server.max_active_handlers == 1


def test_cached_redirect_keeps_validators_at_their_urls(
    mock_server, run_ghreport, make_issue, read_trace, api_token
):
    calls = 0

    def respond(request):
        nonlocal calls
        assert request.headers.get("authorization") == "Bearer " + api_token
        if urlsplit(request.target).path == "/repos/stub/alpha/issues":
            calls += 1
            if calls == 1:
                assert "if-none-match" not in request.headers
                return Response.json([make_issue(1, "source")], headers={"ETag": '"source"'})
            assert calls == 2, "Permanent redirect was not persisted"
            assert request.headers.get("if-none-match") == '"source"'
            return Response(301, headers={"Location": "/moved"})
        assert request.target == "/moved"
        validator = request.headers.get("if-none-match")
        if validator is None:
            return Response.json([make_issue(2, "target")], headers={"ETag": '"target"'})
        assert validator == '"target"', "Source validator reached the redirect target"
        return Response(304)

    server = mock_server(respond)
    for run, statuses in enumerate(([200], [301, 200], [304])):
        start = len(server.requests)
        result = run_ghreport(
            "--repos", "stub/alpha", "--trace", "trace.jsonl",
            base_url=server.base_url, token=api_token,
        )
        assert result.returncode == 0 and result.stderr == ""
        report = json_object(result.stdout)
        check_report(report, ["stub/alpha"])
        assert [item["number"] for item in report["repos"][0]["issues"]] == [1 if run == 0 else 2]
        records = read_trace(server, start)
        assert [record["status"] for record in records] == statuses
        assert all(record["attempt"] == 1 and record["error"] is None
                   and record["from_cache"] == (run == 2) for record in records)
    assert [request.headers.get("if-none-match") for request in server.requests] == [
        None, '"source"', None, '"target"',
    ]
    assert [urlsplit(request.target).path for request in server.requests] == [
        "/repos/stub/alpha/issues", "/repos/stub/alpha/issues", "/moved", "/moved",
    ]


@pytest.mark.parametrize(
    "kind", ["cycle", "limit", "missing", "malformed", "port", "credentials", "scheme"],
)
def test_redirect_failure_is_finite_and_next_repo_runs(
    kind, mock_server, run_ghreport, read_trace, api_token
):
    bad_calls = 0

    def respond(request):
        nonlocal bad_calls
        path = urlsplit(request.target).path
        if path == "/repos/stub/good/issues":
            return Response.json([])
        assert path == "/repos/stub/bad/issues" or path.startswith("/hop/")
        bad_calls += 1
        assert bad_calls <= (11 if kind == "limit" else 1), "Extra redirect attempt"
        locations = {
            "cycle": request.target, "limit": f"/hop/{bad_calls}", "missing": "",
            "malformed": "%zz", "port": "http://127.0.0.1:65536/path",
            "credentials": server.base_url.replace("://", "://user:private-secret@") + "/path",
            "scheme": "file:///tmp/page",
        }
        return Response(302, headers={"Location": locations[kind]})

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/bad,stub/good", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 1 and result.stderr == ""
    report = json_object(result.stdout)
    check_report(report, ["stub/bad", "stub/good"])
    assert report["repos"] == [
        {"repo": "stub/bad", "error": "request_failed", "count": 0, "issues": []},
        {"repo": "stub/good", "error": None, "count": 0, "issues": []},
    ]
    assert bad_calls == (11 if kind == "limit" else 1)
    assert len(server.requests) == bad_calls + 1
    assert urlsplit(server.requests[-1].target).path == "/repos/stub/good/issues"
    records = read_trace(server)
    assert [record["status"] for record in records] == [302] * bad_calls + [200]
    assert all(record["attempt"] == 1 and record["from_cache"] is False for record in records)
    expected_error = None if kind in {"cycle", "limit"} else "redirect"
    assert [record["error"] for record in records] == [expected_error] * bad_calls + [None]
    check_no_token(api_token, result.stdout, result.stderr)
