from urllib.parse import parse_qs, urlsplit

import pytest

from ..helpers.checks import (
    check_generated_at,
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
