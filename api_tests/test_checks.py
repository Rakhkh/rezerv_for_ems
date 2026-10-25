"""Regressions for contract validators that previously accepted invalid data."""

import json

import pytest

from .helpers.checks import timestamp_key, trace_records


def trace_record():
    return {
        "ts": "2026-01-01T00:00:00Z", "method": "GET",
        "url": "http://127.0.0.1/repos/stub/repo/issues", "status": 200,
        "attempt": 1, "from_cache": False, "error": None,
    }


@pytest.mark.parametrize("key,location", [
    ("Authorization", "top"),
    (r"\u0041uthorization", "top"),
    (r"\u0061UtHoRiZaTiOn", "object"),
    (r"\u0041uthorization", "array"),
], ids=["literal", "escaped", "nested-mixed-case", "inside-array"])
def test_trace_rejects_authorization(key, location):
    header = '{"' + key + '": "Bearer [REDACTED]"}'
    if location == "object":
        extra = '"headers": ' + header
    elif location == "array":
        extra = '"extensions": [' + header + ']'
    else:
        extra = header[1:-1]
    text = json.dumps(trace_record())[:-1] + ", " + extra + "}"
    with pytest.raises(AssertionError, match="Authorization"):
        trace_records(text)


def test_trace_allows_authorization_as_ordinary_text():
    record = trace_record()
    record["extensions"] = [{"note": "Authorization", "values": ["Authorization"]}]
    assert trace_records(json.dumps(record)) == [record]


@pytest.mark.parametrize("offset", ["+00:99", "-00:60", "+24:00", "-24:00"])
def test_timestamp_rejects_invalid_offset(offset):
    with pytest.raises(AssertionError):
        timestamp_key("2026-01-01T00:00:00" + offset)


def test_timestamp_accepts_offset_boundaries_without_losing_precision():
    assert timestamp_key("2026-01-01T00:00:00.123456789+23:59") == timestamp_key(
        "2025-12-31T00:01:00.123456789Z"
    )
    assert timestamp_key("2026-01-01T00:00:00.123456789-23:59") == timestamp_key(
        "2026-01-01T23:59:00.123456789Z"
    )
