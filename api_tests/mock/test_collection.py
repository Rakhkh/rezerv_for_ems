from urllib.parse import parse_qs, urlsplit

import pytest

from ..helpers.checks import (
    check_generated_at,
    check_no_token,
    check_report,
    json_object,
    timestamp_key,
)
from ..helpers.server import Response


def test_pagination_selection_and_repo_order(
    mock_server, run_ghreport, make_issue, api_token
):
    boundary = "2026-01-01T03:00:00+03:00"
    first_page = [
        make_issue(99, "too old", "2025-12-31T23:59:59.999999999Z"),
        make_issue(3, "first version", "2026-01-02T00:00:00Z"),
        make_issue(2, "boundary", boundary),
        make_issue(90, "pull request") | {"pull_request": {}},
        {"pull_request": None},
        {"pull_request": {}, "number": "ignored"},
    ]
    last_page = [
        make_issue(3, "duplicate version", "2026-01-02T00:00:00Z"),
        make_issue(1, "same instant, smaller number"),
        make_issue(4, "last selected", "2026-01-03T00:00:00Z"),
        make_issue(5, "beyond limit", "2026-01-04T00:00:00Z"),
    ]

    def respond(request):
        assert request.method == "GET"
        assert request.headers.get("authorization") == "Bearer " + api_token
        target = urlsplit(request.target)
        if target.path.startswith("/repos/"):
            assert parse_qs(target.query, keep_blank_values=True) == {
                "state": ["open"], "sort": ["created"],
                "direction": ["asc"], "per_page": ["100"],
            }
        if target.path == "/repos/stub/paged/issues":
            return Response.json(first_page, headers={
                "Link": '</never-last>; rel="last", '
                        '</repositories/123/issues?cursor=a%2Cb&custom=keep>; rel="next"',
            })
        if request.target == "/repositories/123/issues?cursor=a%2Cb&custom=keep":
            # A short/empty page is not the end when next is present.
            return Response.json([], headers={
                "Link": f'<{server.base_url}/pages/three>; rel="next"',
            })
        if request.target == "/pages/three":
            return Response.json(last_page, headers={
                "Link": '</must-not-be-requested>; rel="next"',
            })
        if target.path == "/repos/stub/other/issues":
            return Response.json([make_issue(1, "another repository")])
        if target.path == "/repos/stub/empty/issues":
            return Response.json([])
        raise AssertionError(f"Unexpected request: {request.target}")

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/paged,stub/other,stub/empty,stub/paged",
        "--limit", "4", "--since", "2026-01-01",
        base_url=server.base_url, token=api_token,
    )
    assert result.returncode == 0
    assert result.stderr == ""
    check_no_token(api_token, result.stdout, result.stderr)
    report = json_object(result.stdout)
    check_report(report, ["stub/paged", "stub/other", "stub/empty"], limit=4)
    check_generated_at(report, result)
    paged, other, empty = report["repos"]
    assert [entry["error"] for entry in report["repos"]] == [None, None, None]
    assert [(item["number"], item["title"], item["created_at"])
            for item in paged["issues"]] == [
        (1, "same instant, smaller number", "2026-01-01T00:00:00Z"),
        (2, "boundary", boundary),
        (3, "first version", "2026-01-02T00:00:00Z"),
        (4, "last selected", "2026-01-03T00:00:00Z"),
    ]
    assert other["count"] == 1
    assert [(item["number"], item["title"]) for item in other["issues"]] == [
        (1, "another repository"),
    ]
    assert empty == {"repo": "stub/empty", "error": None, "count": 0, "issues": []}
    requests = server.requests
    assert [urlsplit(request.target).path for request in requests] == [
        "/repos/stub/paged/issues", "/repositories/123/issues", "/pages/three",
        "/repos/stub/other/issues", "/repos/stub/empty/issues",
    ]
    assert requests[1].target == "/repositories/123/issues?cursor=a%2Cb&custom=keep"


def test_since_uses_creation_utc_and_preserves_precision(
    mock_server, run_ghreport, make_issue
):
    issues = [
        make_issue(99, "old but recently updated", "2025-12-31T23:59:59.999999999Z")
        | {"updated_at": "2027-01-01T00:00:00Z"},
        make_issue(98, "old UTC instant", "2026-01-01T02:59:59.999999999+03:00"),
        make_issue(5, "boundary with previous calendar date", "2025-12-31T19:00:00-05:00")
        | {"updated_at": "2020-01-01T00:00:00Z"},
        make_issue(4, "boundary with positive offset", "2026-01-01T03:00:00+03:00"),
        make_issue(1, "two nanoseconds", "2026-01-01T00:00:00.000000002Z"),
        make_issue(2, "one nanosecond", "2026-01-01T00:00:00.000000001Z"),
    ]

    def respond(request):
        target = urlsplit(request.target)
        assert target.path == "/repos/stub/dates/issues"
        assert parse_qs(target.query, keep_blank_values=True) == {
            "state": ["open"], "sort": ["created"],
            "direction": ["asc"], "per_page": ["100"],
        }
        return Response.json(issues)

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/dates", "--since", "2026-01-01", base_url=server.base_url,
    )
    assert result.returncode == 0
    assert result.stderr == ""
    report = json_object(result.stdout)
    check_report(report, ["stub/dates"])
    check_generated_at(report, result)
    entry = report["repos"][0]
    assert entry["error"] is None
    assert [(item["number"], item["created_at"]) for item in entry["issues"]] == [
        (4, "2026-01-01T03:00:00+03:00"),
        (5, "2025-12-31T19:00:00-05:00"),
        (2, "2026-01-01T00:00:00.000000001Z"),
        (1, "2026-01-01T00:00:00.000000002Z"),
    ]
    assert all(timestamp_key(item["created_at"]) >= timestamp_key("2026-01-01T00:00:00Z")
               for item in entry["issues"])
    assert len(server.requests) == 1


def test_no_matching_issues_is_success(mock_server, run_ghreport, make_issue):
    def respond(request):
        assert urlsplit(request.target).path == "/repos/stub/filtered/issues"
        return Response.json([
            make_issue(1, "too old", "2025-12-31T23:59:59Z"),
            make_issue(2, "pull request") | {"pull_request": {}},
            {"pull_request": None},
        ])

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/filtered", "--since", "2026-01-01", base_url=server.base_url,
    )
    assert result.returncode == 0
    assert result.stderr == ""
    report = json_object(result.stdout)
    check_report(report, ["stub/filtered"])
    assert report["repos"] == [
        {"repo": "stub/filtered", "error": None, "count": 0, "issues": []},
    ]
    assert len(server.requests) == 1


@pytest.mark.parametrize("limit", [1, 100], ids=["limit-before-next", "cycle-error"])
def test_cycle_and_early_stop(limit, mock_server, run_ghreport, make_issue):
    def respond(request):
        path = urlsplit(request.target).path
        if path == "/repos/stub/cycle/issues":
            assert len(server.requests) == 1, "Repeated request to a cyclic page"
            return Response.json([make_issue(1, "issue")], headers={
                "Link": f'<{request.target}#ignored>; rel="next"',
            })
        if path == "/repos/stub/empty/issues":
            return Response.json([])
        raise AssertionError(f"Unexpected request: {request.target}")

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/cycle,stub/empty", "--limit", str(limit),
        base_url=server.base_url,
    )
    assert result.returncode == (0 if limit == 1 else 1)
    assert result.stderr == ""
    report = json_object(result.stdout)
    check_report(report, ["stub/cycle", "stub/empty"], limit=limit)
    entry = report["repos"][0]
    if limit == 1:
        assert entry["error"] is None and entry["count"] == 1
        assert entry["issues"][0]["number"] == 1
    else:
        assert entry == {
            "repo": "stub/cycle", "error": "invalid_response", "count": 0, "issues": [],
        }
    assert report["repos"][1] == {
        "repo": "stub/empty", "error": None, "count": 0, "issues": [],
    }
    assert [urlsplit(request.target).path for request in server.requests] == [
        "/repos/stub/cycle/issues", "/repos/stub/empty/issues",
    ]
