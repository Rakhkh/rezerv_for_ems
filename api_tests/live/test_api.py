"""Properties that remain observable without a second API client or data oracle."""

import pytest

from ..helpers.checks import (
    check_generated_at,
    check_no_token,
    check_report,
    http_origin,
    json_object,
    timestamp_key,
    trace_records,
)


@pytest.mark.parametrize("since", [None, "2020-01-01"], ids=["report", "since"])
def test_api_properties(live_api, run_live_api, work_dir, since):
    args = [
        "--repos", ",".join(live_api.repos), "--limit", "3",
        "--out", "-", "--trace", "trace.jsonl",
    ]
    if since:
        args.extend(["--since", since])
    result = run_live_api(*args)
    trace_path = work_dir / "trace.jsonl"
    trace = trace_path.read_text(encoding="utf-8") if trace_path.exists() else ""
    check_no_token(live_api.token, result.stdout, result.stderr, trace)
    # Check decoded strings before contract assertions can display a bad value.
    report = json_object(result.stdout)
    decoded_trace = [json_object(line) for line in trace.splitlines()]
    check_no_token(live_api.token, report, decoded_trace)
    if result.returncode != 0:
        pytest.fail(
            "Configured live API run failed (exit code %d); check API access and configuration"
            % result.returncode,
            pytrace=False,
        )
    check_report(report, list(live_api.repos), limit=3)
    check_generated_at(report, result)
    assert all(entry["error"] is None for entry in report["repos"])
    if since:
        threshold = timestamp_key(since + "T00:00:00Z")
        assert all(
            timestamp_key(issue["created_at"]) >= threshold
            for entry in report["repos"] for issue in entry["issues"]
        )

    records = trace_records(trace)
    assert records, "Successful API run must have HTTP attempts in trace"
    for record in records:
        assert http_origin(record["url"]) == live_api.origin, (
            "Trace reveals a request outside configured API origin; this check detects egress after the request"
        )
        if record["from_cache"]:
            assert record["status"] == 304 and record["error"] is None
        if record["status"] == 200:
            assert record["from_cache"] is False
