from urllib.parse import urlsplit

import pytest

from ..helpers.checks import (
    check_generated_at,
    check_no_token,
    check_report,
    check_trace,
    json_object,
)
from ..helpers.server import Response


def test_multiple_errors_with_success_remain_partial(
    mock_server, run_ghreport, make_issue, api_token
):
    def respond(request):
        path = urlsplit(request.target).path
        if path == "/repos/stub/good/issues":
            return Response.json([make_issue(1, "good")])
        if path == "/repos/stub/missing/issues":
            return Response(404, api_token.encode())
        if path == "/repos/stub/broken/issues":
            return Response.json([make_issue(99, "partial")], headers={
                "Link": '</pages/broken>; rel="next"',
            })
        if path == "/pages/broken":
            return Response(200, b"[")
        if path == "/repos/stub/denied/issues":
            return Response(403, b"Forbidden")
        if path == "/repos/stub/invalid/issues":
            return Response(422)
        raise AssertionError(f"Unexpected request: {request.target}")

    server = mock_server(respond)
    repos = ["stub/good", "stub/missing", "stub/broken", "stub/denied", "stub/invalid"]
    result = run_ghreport("--repos", ",".join(repos), base_url=server.base_url, token=api_token)
    assert result.returncode == 1
    assert result.stderr == ""
    check_no_token(api_token, result.stdout, result.stderr)
    report = json_object(result.stdout)
    check_report(report, repos)
    assert [(entry["error"], entry["count"]) for entry in report["repos"]] == [
        (None, 1), ("not_found", 0), ("invalid_response", 0),
        ("request_failed", 0), ("request_failed", 0),
    ]
    assert report["repos"][0]["issues"][0]["title"] == "good"
    assert [urlsplit(request.target).path for request in server.requests] == [
        "/repos/stub/good/issues", "/repos/stub/missing/issues",
        "/repos/stub/broken/issues", "/pages/broken",
        "/repos/stub/denied/issues", "/repos/stub/invalid/issues",
    ]


@pytest.mark.parametrize("on_next_page", [False, True], ids=["first-page", "next-page"])
@pytest.mark.parametrize(
    "status,body,error,trace_error",
    [
        pytest.param(404, b"private body", "not_found", "http_404", id="not-found"),
        pytest.param(200, b"[", "invalid_response", "invalid_response", id="broken-json"),
        pytest.param(200, b"null", "invalid_response", "invalid_response", id="wrong-structure"),
        pytest.param(304, b"", "invalid_response", "invalid_response", id="unconditional-304"),
        pytest.param(403, b"Forbidden", "request_failed", "http_403", id="plain-forbidden"),
        pytest.param(422, b"private body", "request_failed", "http_422", id="unprocessable"),
    ],
)
def test_repository_error_discards_partial_result_and_continues(
    status, body, error, trace_error, on_next_page,
    mock_server, run_ghreport, work_dir, make_issue, api_token,
):
    def respond(request):
        assert request.method == "GET"
        assert request.headers.get("authorization") == "Bearer " + api_token
        assert "if-none-match" not in request.headers
        path = urlsplit(request.target).path
        if path == "/repos/stub/before/issues":
            return Response.json([make_issue(1, "before")])
        if path == "/repos/stub/after/issues":
            return Response.json([make_issue(1, "after")])
        if path == "/repos/stub/bad/issues" and on_next_page:
            return Response.json([make_issue(99, "must be discarded")], headers={
                "Link": '</pages/failure?cursor=a%2Cb&keep=yes>; rel="next"',
            })
        if (path == "/repos/stub/bad/issues" and not on_next_page) or (
            request.target == "/pages/failure?cursor=a%2Cb&keep=yes" and on_next_page
        ):
            payload = body if status in {200, 304} else body + b": " + api_token.encode()
            return Response(status, payload)
        raise AssertionError(f"Unexpected request: {request.target}")

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/before,stub/bad,stub/after", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 1
    assert result.stderr == ""
    report = json_object(result.stdout)
    check_report(report, ["stub/before", "stub/bad", "stub/after"])
    check_generated_at(report, result)
    before_issue = {
        "number": 1, "title": "before", "author": "author", "labels": [],
        "created_at": "2026-01-01T00:00:00Z", "url": "https://example.com/issue/1",
    }
    assert report["repos"] == [
        {"repo": "stub/before", "error": None, "count": 1, "issues": [before_issue]},
        {"repo": "stub/bad", "error": error, "count": 0, "issues": []},
        {"repo": "stub/after", "error": None, "count": 1,
         "issues": [before_issue | {"title": "after"}]},
    ]
    paths = [urlsplit(request.target).path for request in server.requests]
    expected_paths = ["/repos/stub/before/issues", "/repos/stub/bad/issues"]
    if on_next_page:
        expected_paths.append("/pages/failure")
    expected_paths.append("/repos/stub/after/issues")
    assert paths == expected_paths

    trace = (work_dir / "trace.jsonl").read_text(encoding="utf-8")
    check_no_token(api_token, result.stdout, result.stderr, trace)
    records = check_trace(trace, server.base_url, server.requests)
    expected = [(200, None)]
    if on_next_page:
        expected.append((200, None))
    expected += [(status, trace_error), (200, None)]
    assert [(record["status"], record["error"]) for record in records] == expected
    assert all(record["attempt"] == 1 and record["from_cache"] is False
               for record in records)


@pytest.mark.parametrize("out", ["-", "report.json"], ids=["stdout", "file"])
@pytest.mark.parametrize(
    "failure", ["all-failed", "unauthorized-first", "unauthorized-next"],
)
def test_fatal_result_preserves_report_and_trace(
    failure, out, mock_server, run_ghreport, work_dir, make_issue, api_token
):
    def respond(request):
        assert request.method == "GET"
        assert request.headers.get("authorization") == "Bearer " + api_token
        path = urlsplit(request.target).path
        if path == "/repos/stub/good/issues":
            return Response.json([make_issue(1, "previous success must not be output")])
        if path == "/repos/stub/missing/issues":
            return Response(404, api_token.encode())
        if path == "/repos/stub/broken/issues":
            return Response.json([make_issue(99, "partial")], headers={
                "Link": '</pages/broken>; rel="next"',
            })
        if path == "/pages/broken":
            return Response(200, b"[")
        if path == "/repos/stub/auth/issues" and failure == "unauthorized-next":
            return Response.json([make_issue(99, "partial")], headers={
                "Link": '</pages/auth>; rel="next"',
            })
        if path in {"/repos/stub/auth/issues", "/pages/auth"}:
            return Response(401, b"private body: " + api_token.encode())
        raise AssertionError(f"Unexpected request after fatal response: {request.target}")

    server = mock_server(respond)
    if failure == "all-failed":
        repos = "stub/missing,stub/broken"
        fatal = "all_failed"
        paths = ["/repos/stub/missing/issues", "/repos/stub/broken/issues", "/pages/broken"]
        expected = [(404, "http_404"), (200, None), (200, "invalid_response")]
    else:
        repos = "stub/good,stub/auth,stub/never"
        fatal = "unauthorized"
        paths = ["/repos/stub/good/issues", "/repos/stub/auth/issues"]
        expected = [(200, None)]
        if failure == "unauthorized-next":
            paths.append("/pages/auth")
            expected.append((200, None))
        expected.append((401, "http_401"))
    report_path = work_dir / "report.json"
    previous = b"existing report"
    report_path.write_bytes(previous)
    result = run_ghreport(
        "--repos", repos, "--out", out, "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 3
    assert result.stderr == ""
    assert json_object(result.stdout) == {"error": fatal}
    assert report_path.read_bytes() == previous
    assert [urlsplit(request.target).path for request in server.requests] == paths
    trace = (work_dir / "trace.jsonl").read_text(encoding="utf-8")
    check_no_token(api_token, result.stdout, result.stderr, trace)
    records = check_trace(trace, server.base_url, server.requests)
    assert [(record["status"], record["error"]) for record in records] == expected
    assert all(record["attempt"] == 1 and record["from_cache"] is False
               for record in records)
