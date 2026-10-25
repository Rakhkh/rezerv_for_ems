import errno
import os
from urllib.parse import parse_qs, urlsplit

import pytest

from ..helpers.checks import (
    check_generated_at,
    check_json_no_token,
    check_no_token,
    check_report,
    json_object,
)
from ..helpers.server import Response


@pytest.mark.parametrize("out", ["-", "отчёт.json"], ids=["stdout", "file"])
@pytest.mark.parametrize(
    "prefix,token_mode",
    [("", "absent"), ("/api/v3", "present"), ("/api/v3/", "empty")],
    ids=["root-no-token", "prefix-token", "prefix-slash-empty-token"],
)
def test_request_and_report(
    out, prefix, token_mode, mock_server, run_ghreport, work_dir, cache_dir, api_token
):
    token = {"absent": None, "present": api_token, "empty": ""}[token_mode]
    issues = [
        {
            "number": 1, "title": "Первый выпуск", "user": {"login": "octocat"},
            "labels": [{"name": "bug"}, {"name": "help wanted"}],
            "created_at": "2026-01-05T10:00:00.123+03:00",
            "html_url": "https://example.com/issue/1", "unknown_field": "ignored",
        },
        {
            "number": 2, "title": "Второй выпуск", "user": {"login": "hubot"},
            "labels": [], "created_at": "2026-01-06T11:00:00Z",
            "html_url": "https://example.com/issue/2",
        },
    ]

    def respond(request):
        assert request.method == "GET"
        target = urlsplit(request.target)
        assert target.path == prefix.rstrip("/") + "/repos/cli/cli/issues"
        assert parse_qs(target.query, keep_blank_values=True) == {
            "state": ["open"], "sort": ["created"],
            "direction": ["asc"], "per_page": ["100"],
        }
        assert request.headers.get("accept") == "application/vnd.github+json"
        assert request.headers.get("x-github-api-version") == "2026-03-10"
        assert "ghreport" in request.headers.get("user-agent", "").lower()
        if token:
            assert request.headers.get("authorization") == "Bearer " + token
        else:
            assert "authorization" not in request.headers
        assert "if-none-match" not in request.headers
        return Response.json(issues)

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "cli/cli", "--out", out, "--since", "2020-01-01",
        base_url=server.base_url + prefix, token=token,
    )
    assert result.returncode == 0
    assert result.stderr == ""
    if out == "-":
        text = result.stdout
        assert not (work_dir / "отчёт.json").exists()
    else:
        assert result.stdout == ""
        text = (work_dir / out).read_text(encoding="utf-8")
    report = json_object(text)
    check_report(report, ["cli/cli"])
    check_generated_at(report, result)
    assert report["repos"] == [{
        "repo": "cli/cli", "error": None, "count": 2,
        "issues": [
            {
                "number": 1, "title": "Первый выпуск", "author": "octocat",
                "labels": ["bug", "help wanted"],
                "created_at": "2026-01-05T10:00:00.123+03:00",
                "url": "https://example.com/issue/1",
            },
            {
                "number": 2, "title": "Второй выпуск", "author": "hubot",
                "labels": [], "created_at": "2026-01-06T11:00:00Z",
                "url": "https://example.com/issue/2",
            },
        ],
    }]
    assert cache_dir.is_dir()
    assert not (work_dir / "trace.jsonl").exists()
    check_no_token(api_token, text, result.stdout, result.stderr)
    assert len(server.requests) == 1


@pytest.mark.parametrize("kind", ["new", "relative-alias", "hard-link", "symlink-parent"])
def test_conflicting_outputs_preserve_files(
    kind, mock_server, run_ghreport, work_dir, cache_dir, api_token
):
    def unexpected(request):
        raise AssertionError("Conflicting output paths caused HTTP")

    server = mock_server(unexpected)
    out = trace = "result.json"
    previous = b"existing contents"
    if kind == "symlink-parent":
        if os.name == "nt":
            pytest.skip("POSIX symlink/.. path semantics")
        (work_dir / "base").mkdir()
        (work_dir / "other" / "child").mkdir(parents=True)
        os.symlink("../other/child", work_dir / "base" / "link")
        # Keep the literal components; do not resolve/normalise before launch.
        out, trace = "base/link/../result.json", "other/result.json"
    if kind != "new":
        (work_dir / out).write_bytes(previous)
    if kind == "relative-alias":
        trace = "./result.json"
    if kind == "hard-link":
        trace = "alias.json"
        try:
            os.link(work_dir / out, work_dir / trace)
        except OSError as exc:
            if exc.errno in {errno.EACCES, errno.EPERM, errno.ENOTSUP}:
                pytest.skip("Hard links unavailable on this filesystem")
            raise
    entries_before = {path.name for path in work_dir.iterdir()}
    result = run_ghreport(
        "--repos", "stub/alpha", "--out", out, "--trace", trace,
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 2 and result.stdout == "" and result.stderr.strip()
    check_no_token(api_token, result.stderr)
    assert not cache_dir.exists()
    assert server.requests == ()
    assert {path.name for path in work_dir.iterdir()} == entries_before
    for target in (out, trace):
        if kind == "new":
            assert not (work_dir / target).exists()
        else:
            assert (work_dir / target).read_bytes() == previous


@pytest.mark.parametrize("target", ["trace", "report"])
def test_output_open_failure_is_fatal(
    target, mock_server, run_ghreport, work_dir, read_trace, api_token
):
    def respond(request):
        assert urlsplit(request.target).path == "/repos/stub/alpha/issues"
        return Response.json([])

    server = mock_server(respond)
    report_path = work_dir / "report.json"
    report_path.write_bytes(b"existing report")
    result = run_ghreport(
        "--repos", "stub/alpha", "--out", str(work_dir) if target == "report" else "report.json",
        "--trace", str(work_dir) if target == "trace" else "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 3 and result.stdout == "" and result.stderr.strip()
    check_no_token(api_token, result.stderr)
    assert report_path.read_bytes() == b"existing report"
    if target == "trace":
        assert server.requests == ()
        assert not (work_dir / "trace.jsonl").exists()
    else:
        records = read_trace(server)
        assert [(record["status"], record["error"]) for record in records] == [(200, None)]


@pytest.mark.parametrize("stage", ["open", "write"])
def test_cache_io_failure_is_fatal(
    stage, mock_server, run_ghreport, cache_dir, work_dir, read_trace, api_token
):
    def respond(request):
        assert stage == "write", "Unusable cache should prevent HTTP"
        assert urlsplit(request.target).path == "/repos/stub/alpha/issues"
        cache_dir.rmdir()
        cache_dir.write_bytes(b"preserve")
        return Response.json([])

    server = mock_server(respond)
    if stage == "open":
        cache_dir.write_bytes(b"preserve")
    result = run_ghreport(
        "--repos", "stub/alpha,stub/never", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 3 and result.stdout == "" and result.stderr.strip()
    check_no_token(api_token, result.stderr)
    assert cache_dir.read_bytes() == b"preserve"
    if stage == "open":
        assert server.requests == ()
        assert not (work_dir / "trace.jsonl").exists()
    else:
        records = read_trace(server)
        assert [(record["status"], record["error"], record["attempt"], record["from_cache"])
                for record in records] == [(200, "cache", 1, False)]


@pytest.mark.parametrize("mode", ["stdout", "file", "cached-file"])
def test_reflected_token_cancels_entire_report(
    mode, mock_server, run_ghreport, work_dir, cache_dir, make_issue, read_trace, api_token
):
    def respond(request):
        assert urlsplit(request.target).path == "/repos/stub/alpha/issues"
        if mode == "cached-file" and len(server.requests) > 1:
            assert request.headers.get("authorization") == "Bearer " + api_token
            assert request.headers.get("if-none-match") == '"saved"'
            return Response(304)
        assert request.headers.get("authorization") == (
            None if mode == "cached-file" else "Bearer " + api_token
        )
        return Response.json([make_issue(1, "contains " + api_token)], headers={"ETag": '"saved"'})

    server = mock_server(respond)
    if mode == "cached-file":
        # A previous run without this credential may legitimately save the text.
        warm = run_ghreport("--repos", "stub/alpha", base_url=server.base_url)
        assert warm.returncode == 0 and warm.stderr == ""
        assert api_token in warm.stdout
    report_path = work_dir / "report.json"
    report_path.write_bytes(b"existing report")
    start = len(server.requests)
    result = run_ghreport(
        "--repos", "stub/alpha", "--out", "-" if mode == "stdout" else "report.json",
        "--trace", "trace.jsonl", base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 3 and result.stdout == "" and result.stderr.strip()
    check_no_token(api_token, result.stdout, result.stderr)
    assert report_path.read_bytes() == b"existing report"
    records = read_trace(server, start)
    cached = mode == "cached-file"
    assert [(record["status"], record["from_cache"], record["error"])
            for record in records] == [(304 if cached else 200, cached, None)]
    assert all(record["attempt"] == 1 for record in records)
    assert list(cache_dir.iterdir()) == [], "Reflected token entry was not removed"


def test_file_report_combines_cache_retry_and_partial_failure(
    mock_server, run_ghreport, work_dir, cache_dir, make_issue, read_trace, api_token
):
    calls = 0

    def respond(request):
        nonlocal calls
        assert request.headers.get("authorization") == "Bearer " + api_token
        path = urlsplit(request.target).path
        if path == "/repos/stub/good/issues":
            calls += 1
            if calls == 1:
                return Response(429, api_token.encode(), {"Retry-After": "0"})
            etag = 'W/"first"'
            if request.headers.get("if-none-match") is not None:
                assert request.headers["if-none-match"] == etag
                return Response(304)
            return Response.json([
                {"pull_request": None}, make_issue(99, "old", "2025-12-31T23:59:59Z"),
                make_issue(2, "first version", "2026-01-02T00:00:00Z"),
            ], headers={"ETag": etag, "Link": '</page/two?cursor=opaque>; rel="next"'})
        if request.target == "/page/two?cursor=opaque":
            etag = '"last"'
            if request.headers.get("if-none-match") is not None:
                assert request.headers["if-none-match"] == etag
                return Response(304)
            return Response.json([
                make_issue(2, "duplicate", "2026-01-02T00:00:00Z"),
                make_issue(1, "same instant", "2026-01-02T03:00:00+03:00"),
            ], headers={"ETag": etag, "Link": '</must-not-be-requested>; rel="next"'})
        assert path == "/repos/stub/missing/issues", "Unexpected next page"
        return Response(404, b"private body: " + api_token.encode())

    server = mock_server(respond)
    previous = None
    for run in range(2):
        start = len(server.requests)
        result = run_ghreport(
            "--repos", "stub/good,stub/missing", "--limit", "2", "--since", "2026-01-01",
            "--out", "report.json", "--trace", "trace.jsonl",
            base_url=server.base_url, token=api_token,
        )
        assert result.returncode == 1 and result.stdout == "" and result.stderr == ""
        text = (work_dir / "report.json").read_text(encoding="utf-8")
        check_no_token(api_token, text, result.stdout, result.stderr)
        report = json_object(text)
        check_report(report, ["stub/good", "stub/missing"], limit=2)
        check_generated_at(report, result)
        good, missing = report["repos"]
        assert good["error"] is None
        assert [(item["number"], item["title"], item["created_at"])
                for item in good["issues"]] == [
            (1, "same instant", "2026-01-02T03:00:00+03:00"),
            (2, "first version", "2026-01-02T00:00:00Z"),
        ]
        assert missing == {"repo": "stub/missing", "error": "not_found", "count": 0, "issues": []}
        comparable = {key: value for key, value in report.items() if key != "generated_at"}
        if previous is not None:
            assert comparable == previous
        previous = comparable
        records = read_trace(server, start)
        expected = [(429, 1, "rate_limited"), (200, 2, None), (200, 1, None), (404, 1, "http_404")]
        if run == 1:
            expected = [(304, 1, None), (304, 1, None), (404, 1, "http_404")]
        assert [(record["status"], record["attempt"], record["error"])
                for record in records] == expected
        assert all(record["from_cache"] == (record["status"] == 304) for record in records)
    assert [request.headers.get("if-none-match") for request in server.requests] == [
        None, None, None, None, 'W/"first"', '"last"', None,
    ]
    assert [urlsplit(request.target).path for request in server.requests] == [
        "/repos/stub/good/issues", "/repos/stub/good/issues", "/page/two",
        "/repos/stub/missing/issues", "/repos/stub/good/issues", "/page/two",
        "/repos/stub/missing/issues",
    ]
    for path in cache_dir.iterdir():
        check_json_no_token(api_token, path.read_text(encoding="utf-8"))
