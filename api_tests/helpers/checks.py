"""Assertions based on the report contract, independent of Go's JSON structs."""

import json
import re
from datetime import datetime, timezone
from decimal import Decimal
from typing import Sequence
from urllib.parse import urlsplit

from .runner import RunResult
from .server import Request


_RFC3339 = re.compile(
    r"(?P<date>\d{4}-\d{2}-\d{2})[Tt](?P<time>\d{2}:\d{2}:\d{2})"
    r"(?:\.(?P<fraction>\d+))?(?P<zone>[Zz]|[+-]\d{2}:\d{2})",
    re.ASCII,
)
_EPOCH = datetime(1970, 1, 1, tzinfo=timezone.utc)


def timestamp_key(value: str, *, utc: bool = False) -> tuple[int, Decimal]:
    """Compare RFC3339 instants without losing sub-microsecond digits."""
    assert isinstance(value, str), "Timestamp must be a string"
    match = _RFC3339.fullmatch(value)
    assert match, "Timestamp must be RFC3339 with a timezone"
    zone = match["zone"]
    if zone.upper() == "Z":
        zone = "+00:00"
    try:
        whole = datetime.fromisoformat(
            match["date"] + "T" + match["time"] + zone
        )
    except ValueError:
        raise AssertionError("Timestamp has an invalid date or time") from None
    if utc:
        assert whole.utcoffset().total_seconds() == 0, "Timestamp must be UTC"
    delta = whole.astimezone(timezone.utc) - _EPOCH
    # A tuple avoids Decimal-context rounding when adding a long fraction to
    # epoch seconds. Constructing and comparing Decimal values is exact.
    fraction = Decimal("0." + (match["fraction"] or "0"))
    return delta.days * 86400 + delta.seconds, fraction


def json_object(text: str) -> dict:
    def invalid_constant(value):
        raise ValueError("Non-JSON numeric constant")

    try:
        value = json.loads(text, parse_constant=invalid_constant)
    except ValueError:
        raise AssertionError("Expected one valid JSON object") from None
    assert isinstance(value, dict), "Expected a JSON object"
    return value


def check_report(report: dict, repos: list[str], *, limit: int = 100):
    assert {"generated_at", "repos"} <= report.keys()
    timestamp_key(report["generated_at"], utc=True)
    assert isinstance(report["repos"], list)
    assert [entry["repo"] for entry in report["repos"]] == repos
    errors = {
        "not_found", "rate_limited", "request_failed", "invalid_response"
    }
    for entry in report["repos"]:
        assert {"repo", "error", "count", "issues"} <= entry.keys()
        assert isinstance(entry["repo"], str)
        assert entry["error"] is None or entry["error"] in errors
        assert type(entry["count"]) is int
        assert isinstance(entry["issues"], list)
        assert entry["count"] == len(entry["issues"])
        assert 0 <= entry["count"] <= limit
        if entry["error"] is not None:
            assert entry["issues"] == []
        numbers = []
        ordering = []
        for issue in entry["issues"]:
            assert {
                "number", "title", "author", "labels", "created_at", "url"
            } <= issue.keys()
            assert type(issue["number"]) is int
            for key in ("title", "author", "created_at", "url"):
                assert isinstance(issue[key], str)
            assert isinstance(issue["labels"], list)
            assert all(isinstance(label, str) for label in issue["labels"])
            numbers.append(issue["number"])
            ordering.append((timestamp_key(issue["created_at"]), issue["number"]))
        assert len(set(numbers)) == len(numbers), "Duplicate issue numbers"
        assert ordering == sorted(ordering), "Issues are not sorted by creation/number"


def check_generated_at(report: dict, result: RunResult):
    generated = timestamp_key(report["generated_at"], utc=True)
    # The service's generated_at may be rounded down to whole seconds.
    before = result.started_at.replace(microsecond=0).isoformat()
    assert timestamp_key(before) <= generated
    assert generated <= timestamp_key(result.finished_at.isoformat())


def check_no_token(token: str | None, *texts: str):
    if token:
        assert all(token not in text for text in texts), "API token leaked"


def check_trace(text: str, base_url: str, requests: Sequence[Request]) -> list[dict]:
    """Validate JSON Lines and correlate each record with an observed request."""
    assert not re.search(r'"authorization"\s*:', text, re.IGNORECASE), (
        "Authorization header appeared in trace"
    )
    records = [json_object(line) for line in text.splitlines()]
    assert len(records) == len(requests), "Trace missed or added HTTP attempts"
    for record, request in zip(records, requests):
        assert {
            "ts", "method", "url", "status", "attempt", "from_cache", "error"
        } <= record.keys()
        timestamp_key(record["ts"], utc=True)
        assert record["method"] == request.method == "GET"
        assert isinstance(record["url"], str)
        address = urlsplit(record["url"])
        assert address.scheme in {"http", "https"} and address.netloc
        assert record["url"] == base_url + request.target
        assert type(record["status"]) is int and record["status"] >= 0
        assert type(record["attempt"]) is int and record["attempt"] >= 1
        assert type(record["from_cache"]) is bool
        assert record["error"] is None or isinstance(record["error"], str)
    return records
