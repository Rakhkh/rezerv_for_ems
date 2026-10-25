import json
from urllib.parse import urlsplit

import pytest

from ..helpers.checks import check_json_no_token, check_no_token, check_report, json_object
from ..helpers.server import Response


def test_persistent_pages_and_redirect(
    mock_server, run_ghreport, work_dir, make_issue, read_trace, api_token
):
    def respond(request):
        assert request.headers.get("authorization") == "Bearer " + api_token
        if urlsplit(request.target).path == "/api/repos/stub/alpha/issues":
            assert "if-none-match" not in request.headers
            return Response(301, headers={"Location": "/api/moved?cursor=first"})
        assert request.target in {"/api/moved?cursor=first", "/api/moved?cursor=a%2Cb&keep=yes"}
        first = request.target == "/api/moved?cursor=first"
        etag = 'W/"first"' if first else '"last"'
        validator = request.headers.get("if-none-match")
        if validator is not None:
            assert validator == etag, "Validator crossed page URLs"
            return Response(304)  # Neither Link nor ETag is repeated.
        if first:
            return Response.json([
                make_issue(1, "first"),
                make_issue(2, "second", "2026-01-02T00:00:00Z"),
                {"pull_request": None},
            ], headers={"ETag": etag, "Link": '<?cursor=a%2Cb&keep=yes>; rel="next"'})
        return Response.json([make_issue(3, "third", "2026-01-03T00:00:00Z")], headers={
            "ETag": etag,
        })

    server = mock_server(respond)
    cache = work_dir / "общий кэш"
    original = None
    for run, statuses in enumerate(([301, 200, 200], [304, 304], [304])):
        start = len(server.requests)
        args = ["--repos", "stub/alpha", "--cache-dir", str(cache), "--trace", "trace.jsonl"]
        if run == 2:
            args += ["--since", "2026-01-02", "--limit", "1"]
        result = run_ghreport(*args, base_url=server.base_url + "/api/", token=api_token)
        assert result.returncode == 0 and result.stderr == ""
        check_no_token(api_token, result.stdout, result.stderr)
        report = json_object(result.stdout)
        check_report(report, ["stub/alpha"], limit=1 if run == 2 else 100)
        assert report["repos"][0]["error"] is None
        comparable = {key: value for key, value in report.items() if key != "generated_at"}
        if run == 0:
            original = comparable
            assert [item["number"] for item in report["repos"][0]["issues"]] == [1, 2, 3]
        elif run == 1:
            assert comparable == original
        else:
            assert [item["number"] for item in report["repos"][0]["issues"]] == [2]
        records = read_trace(server, start)
        assert [record["status"] for record in records] == statuses
        assert all(record["attempt"] == 1 and record["error"] is None
                   and record["from_cache"] == (run > 0) for record in records)
    assert [request.headers.get("if-none-match") for request in server.requests] == [
        None, None, None, 'W/"first"', '"last"', 'W/"first"',
    ]
    assert [urlsplit(request.target).path for request in server.requests] == [
        "/api/repos/stub/alpha/issues", "/api/moved", "/api/moved",
        "/api/moved", "/api/moved", "/api/moved",
    ]
    assert len(list(cache.iterdir())) == 3
    for path in cache.iterdir():
        text = path.read_text(encoding="utf-8")
        json_object(text)
        check_json_no_token(api_token, text)
        assert '"authorization"' not in text.lower()
    assert server.max_active_handlers == 1


def test_conditional_page_updates_and_removes_metadata(
    mock_server, run_ghreport, make_issue, read_trace, api_token
):
    calls = 0

    def respond(request):
        nonlocal calls
        path = urlsplit(request.target).path
        if path == "/repos/stub/good/issues":
            return Response.json([])
        if request.target == "/new":
            return Response.json([make_issue(9, "new linked page", "2026-01-09T00:00:00Z")])
        assert path == "/repos/stub/alpha/issues", "Old Link was retained or invented"
        calls += 1
        validators = [None, 'W/"one"', '"two"', '"two"', None, None]
        assert calls <= len(validators)
        assert request.headers.get("if-none-match") == validators[calls - 1]
        if calls == 1:
            return Response.json([make_issue(1, "original")], headers={
                "ETag": 'W/"one"', "Link": '</old>; rel="next"',
            })
        if calls == 2:
            return Response(304, headers={"ETag": '"two"', "Link": '</new>; rel="next"'})
        if calls in {3, 6}:
            return Response(304)
        if calls == 4:
            return Response.json([make_issue(2, "new body", "2026-01-02T00:00:00Z")])
        return Response.json([])

    server = mock_server(respond)
    for run in range(6):
        start = len(server.requests)
        limit = 1 if run < 2 else 2
        result = run_ghreport(
            "--repos", "stub/alpha,stub/good", "--limit", str(limit), "--trace", "trace.jsonl",
            base_url=server.base_url, token=api_token,
        )
        assert result.returncode == (1 if run == 5 else 0)
        assert result.stderr == ""
        report = json_object(result.stdout)
        check_report(report, ["stub/alpha", "stub/good"], limit=limit)
        entry = report["repos"][0]
        expected_numbers = [[1], [1], [1, 9], [2], [], []][run]
        assert [item["number"] for item in entry["issues"]] == expected_numbers
        assert entry["error"] == ("invalid_response" if run == 5 else None)
        if run < 3:
            assert entry["issues"][0]["title"] == "original"
        records = read_trace(server, start)
        status = [200, 304, 304, 200, 200, 304][run]
        assert [record["status"] for record in records] == [status] + (
            [200, 200] if run == 2 else [200]
        )
        assert [record["from_cache"] for record in records] == [run in {1, 2}] + (
            [False, False] if run == 2 else [False]
        )
        assert [record["error"] for record in records] == [
            "invalid_response" if run == 5 else None
        ] + [None] * (len(records) - 1)
        assert all(record["attempt"] == 1 for record in records)
        check_no_token(api_token, result.stdout, result.stderr)
    assert calls == 6


@pytest.mark.parametrize("corruption", ["json", "etag"])
def test_corrupted_cache_is_refreshed_without_retries(
    corruption, mock_server, run_ghreport, cache_dir, read_trace, api_token
):
    def respond(request):
        assert urlsplit(request.target).path == "/repos/stub/alpha/issues"
        assert "if-none-match" not in request.headers
        return Response.json([], headers={"ETag": '"good"'})

    server = mock_server(respond)
    warm = run_ghreport("--repos", "stub/alpha", base_url=server.base_url, token=api_token)
    assert warm.returncode == 0 and warm.stderr == ""
    files = list(cache_dir.iterdir())
    assert len(files) == 1
    path = files[0]
    if corruption == "json":
        path.write_text("[private-cache-marker", encoding="utf-8")
    else:
        # This regression intentionally depends on the documented cache format.
        entry = json_object(path.read_text(encoding="utf-8"))
        entry["etag"] = '"bad\x01"'
        path.write_text(json.dumps(entry), encoding="utf-8")
    start = len(server.requests)
    result = run_ghreport(
        "--repos", "stub/alpha", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 0 and result.stderr.strip()
    assert "private-cache-marker" not in result.stderr
    check_no_token(api_token, result.stdout, result.stderr)
    assert json_object(result.stdout)["repos"] == [
        {"repo": "stub/alpha", "error": None, "count": 0, "issues": []},
    ]
    assert len(server.requests) == 2
    records = read_trace(server, start)
    assert [(record["status"], record["attempt"], record["from_cache"], record["error"])
            for record in records] == [(200, 1, False, None)]
    repaired = json_object(path.read_text(encoding="utf-8"))
    assert repaired["etag"] == '"good"' and repaired["body"] == []
    assert list(cache_dir.iterdir()) == [path]


@pytest.mark.parametrize("failure", ["server-error", "unauthorized", "invalid-json"])
def test_cache_does_not_mask_failure_or_lose_saved_page(
    failure, mock_server, run_ghreport, make_issue, cache_dir, read_trace, api_token
):
    phase = "warm"

    def respond(request):
        path = urlsplit(request.target).path
        if path == "/repos/stub/good/issues":
            return Response.json([])
        assert path == "/repos/stub/alpha/issues"
        if phase == "warm":
            assert "if-none-match" not in request.headers
            return Response.json([make_issue(1, "saved")], headers={"ETag": '"saved"'})
        assert request.headers.get("if-none-match") == '"saved"'
        if phase == "recover":
            return Response(304)
        if failure == "server-error":
            return Response(503, api_token.encode())
        if failure == "unauthorized":
            return Response(401, api_token.encode())
        return Response(200, b"[")

    server = mock_server(respond)
    warm = run_ghreport("--repos", "stub/alpha", base_url=server.base_url, token=api_token)
    assert warm.returncode == 0 and warm.stderr == ""
    phase = "fail"
    start = len(server.requests)
    result = run_ghreport(
        "--repos", "stub/alpha,stub/good", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token, timeout=15,
    )
    assert result.returncode == (3 if failure == "unauthorized" else 1)
    assert result.stderr == ""
    report = json_object(result.stdout)
    if failure == "unauthorized":
        assert report == {"error": "unauthorized"}
    else:
        check_report(report, ["stub/alpha", "stub/good"])
        assert report["repos"][0] == {
            "repo": "stub/alpha", "error": "request_failed" if failure == "server-error"
            else "invalid_response", "count": 0, "issues": [],
        }
        assert report["repos"][1] == {
            "repo": "stub/good", "error": None, "count": 0, "issues": [],
        }
    for path in cache_dir.iterdir():
        check_json_no_token(api_token, path.read_text(encoding="utf-8"))
    records = read_trace(server, start)
    expected = {
        "server-error": [(503, n, "http_503") for n in range(1, 5)] + [(200, 1, None)],
        "unauthorized": [(401, 1, "http_401")],
        "invalid-json": [(200, 1, "invalid_response"), (200, 1, None)],
    }[failure]
    assert [(record["status"], record["attempt"], record["error"])
            for record in records] == expected
    assert all(record["from_cache"] is False for record in records)
    check_no_token(api_token, result.stdout, result.stderr)
    phase = "recover"
    start = len(server.requests)
    recovered = run_ghreport(
        "--repos", "stub/alpha", "--trace", "trace.jsonl",
        base_url=server.base_url, token=api_token,
    )
    assert recovered.returncode == 0 and recovered.stderr == ""
    assert {key: value for key, value in json_object(recovered.stdout).items()
            if key != "generated_at"} == {
        key: value for key, value in json_object(warm.stdout).items() if key != "generated_at"
    }
    records = read_trace(server, start)
    assert [(record["status"], record["attempt"], record["from_cache"], record["error"])
            for record in records] == [(304, 1, True, None)]


@pytest.mark.parametrize("location", ["value", "nested", "key", "duplicate-key"])
def test_escaped_token_in_unknown_fields_is_not_persisted(
    location, mock_server, run_ghreport, make_issue, cache_dir, read_trace, api_token
):
    escaped = "".join("\\u%04x" % ord(char) for char in api_token)
    metadata = {
        "value": '"metadata":"' + escaped + '"',
        "nested": '"metadata":{"nested":["prefix ' + escaped + ' suffix"]}',
        "key": '"' + escaped + '":"value"',
        "duplicate-key": '"metadata":"' + escaped + '","metadata":"overwritten"',
    }[location]
    issue = make_issue(17, "safe report")
    body = ("[" + json.dumps(issue)[:-1] + "," + metadata + "}]").encode("utf-8")
    phase = "clean"
    saved_path = None

    def respond(request):
        assert urlsplit(request.target).path == "/repos/stub/alpha/issues"
        assert request.headers.get("authorization") == (
            None if phase == "legacy" else "Bearer " + api_token
        )
        assert request.headers.get("if-none-match") == {
            "clean": None, "fresh": '"saved"', "legacy": None, "conditional": '"unsafe"',
        }[phase]
        if phase == "clean":
            return Response.json([issue], headers={"ETag": '"saved"'})
        if phase == "conditional":
            # The disk copy is removed on load, while its validator/body remain
            # available to this request and the report produced from a 304.
            assert saved_path is not None and not saved_path.exists()
            return Response(304)
        return Response(200, body, {"Content-Type": "application/json", "ETag": '"unsafe"'})

    server = mock_server(respond)
    original = None
    for phase in ("clean", "fresh", "legacy", "conditional"):
        start = len(server.requests)
        result = run_ghreport(
            "--repos", "stub/alpha", "--trace", "trace.jsonl",
            base_url=server.base_url, token=None if phase == "legacy" else api_token,
        )
        assert result.returncode == 0
        report = json_object(result.stdout)
        check_no_token(api_token, result.stdout, result.stderr, report)
        check_report(report, ["stub/alpha"])
        assert report["repos"][0]["error"] is None
        assert report["repos"][0]["issues"] == [{
            "number": 17, "title": "safe report", "author": "author", "labels": [],
            "created_at": issue["created_at"], "url": issue["html_url"],
        }]
        comparable = {key: value for key, value in report.items() if key != "generated_at"}
        if original is None:
            original = comparable
        else:
            assert comparable == original
        records = read_trace(server, start)
        cached = phase == "conditional"
        assert [(record["status"], record["attempt"], record["from_cache"], record["error"])
                for record in records] == [(304 if cached else 200, 1, cached, None)]
        files = list(cache_dir.iterdir())
        if phase in {"clean", "legacy"}:
            assert len(files) == 1
            saved_path = files[0]
            text = saved_path.read_text(encoding="utf-8")
            if phase == "legacy":
                assert api_token not in text, "Legacy setup must use JSON-escaped data"
                with pytest.raises(AssertionError, match="API token leaked"):
                    check_json_no_token(api_token, text)
            else:
                check_json_no_token(api_token, text)
        else:
            assert files == [], "Unsafe response or previous cache entry remained on disk"
    assert len(server.requests) == 4
