from urllib.parse import urlsplit

import pytest

from ..helpers.checks import check_no_token, check_report, json_object
from ..helpers.server import Response


def test_rate_limit_and_redirect_recovery(
    mock_server, run_ghreport, work_dir, make_issue, read_trace, api_token
):
    starts = 0

    def respond(request):
        nonlocal starts
        assert request.method == "GET"
        assert request.headers.get("authorization") == "Bearer " + api_token
        if urlsplit(request.target).path == "/repos/stub/alpha/issues":
            return Response(302, headers={"Location": "/moved/issues?cursor=start"})
        if request.target == "/moved/issues?cursor=start":
            starts += 1
            if starts == 1:
                return Response(429, api_token.encode(), {"Retry-After": "1"})
            return Response.json([make_issue(2, "second", "2026-01-02T00:00:00Z")], headers={
                "Link": '<?cursor=next>; rel="next"',
            })
        assert request.target == "/moved/issues?cursor=next"
        return Response.json([make_issue(1, "first")])

    server = mock_server(respond)
    (work_dir / "trace.jsonl").write_text("old trace contents\n", encoding="utf-8")
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
        assert [item["number"] for item in report["repos"][0]["issues"]] == [1, 2]
        records = read_trace(server, start)
        expected = [(302, 1, None), (200, 1, None), (200, 1, None)]
        if run == 0:
            expected = [(302, 1, None), (429, 1, "rate_limited"),
                        (200, 2, None), (200, 1, None)]
            requests = server.requests[start:]
            assert requests[2].target == requests[1].target == "/moved/issues?cursor=start"
            assert requests[2].received_at - requests[1].received_at >= 1
        assert [(record["status"], record["attempt"], record["error"])
                for record in records] == expected
        assert all(record["from_cache"] is False for record in records)
    assert server.max_active_handlers == 1


@pytest.mark.parametrize("status", [403, 429])
def test_rate_exhaustion_continues_next_repository(
    status, mock_server, run_ghreport, read_trace, api_token
):
    def respond(request):
        path = urlsplit(request.target).path
        if path == "/repos/stub/limited/issues":
            return Response(status, api_token.encode(), {"Retry-After": "0"})
        assert path == "/repos/stub/good/issues"
        return Response.json([])

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/limited,stub/good", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 1 and result.stderr == ""
    report = json_object(result.stdout)
    check_report(report, ["stub/limited", "stub/good"])
    assert report["repos"] == [
        {"repo": "stub/limited", "error": "rate_limited", "count": 0, "issues": []},
        {"repo": "stub/good", "error": None, "count": 0, "issues": []},
    ]
    records = read_trace(server)
    assert [(record["status"], record["attempt"], record["error"])
            for record in records] == [
        (status, 1, "rate_limited"), (status, 2, "rate_limited"),
        (status, 3, "rate_limited"), (200, 1, None),
    ]
    assert all(record["from_cache"] is False for record in records)
    check_no_token(api_token, result.stdout, result.stderr)


@pytest.mark.parametrize("failure", ["server-error", "truncated-body"])
def test_transient_failure_recovers_with_trace(
    failure, mock_server, run_ghreport, make_issue, read_trace, api_token
):
    def respond(request):
        assert urlsplit(request.target).path == "/repos/stub/alpha/issues"
        assert len(server.requests) <= 2
        if len(server.requests) == 1:
            if failure == "server-error":
                return Response(500, api_token.encode())
            # The connection closes with fewer bytes than advertised.
            return Response(200, b"[", {"Content-Length": "100"})
        return Response.json([make_issue(1, "recovered")])

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/alpha", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 0 and result.stderr == ""
    report = json_object(result.stdout)
    check_report(report, ["stub/alpha"])
    assert report["repos"][0]["error"] is None
    assert report["repos"][0]["issues"][0]["title"] == "recovered"
    records = read_trace(server)
    first = (500, "http_500") if failure == "server-error" else (200, "network")
    assert [(record["status"], record["error"], record["attempt"])
            for record in records] == [(*first, 1), (200, None, 2)]
    assert all(record["from_cache"] is False for record in records)
    check_no_token(api_token, result.stdout, result.stderr)
