"""Requests must remain sequential until the previous response body is read."""

import threading
from urllib.parse import urlsplit

from ..helpers.checks import check_generated_at, check_report, json_object
from ..helpers.server import Response


def test_no_next_request_while_response_body_is_incomplete(
    mock_server, run_ghreport, make_issue, read_trace
):
    lock = threading.Lock()
    received = []
    overlap = threading.Event()
    body_released = threading.Event()
    observation_started = threading.Event()
    paths = {"/repos/stub/first/issues": 1, "/repos/stub/second/issues": 2}

    def observe_incomplete_body():
        # Headers and all but the final JSON byte have already been flushed.
        # This is a bounded observation window, not a startup/scheduling sleep.
        observation_started.set()
        try:
            overlap.wait(timeout=1)
        finally:
            body_released.set()

    def respond(request):
        path = urlsplit(request.target).path
        assert request.method == "GET" and path in paths
        with lock:
            first = not received
            received.append(path)
            if not first and not body_released.is_set():
                overlap.set()
        return Response.json(
            [make_issue(paths[path], path)],
            before_body_end=observe_incomplete_body if first else None,
        )

    server = mock_server(respond)
    result = run_ghreport(
        "--repos", "stub/first,stub/second", "--trace", "trace.jsonl",
        base_url=server.base_url,
    )
    assert observation_started.is_set(), "Incomplete response body was not exercised"
    assert not overlap.is_set(), (
        "Another request arrived before the first response body completed"
    )
    assert result.returncode == 0 and result.stderr == ""
    assert len(received) == 2 and set(received) == paths.keys()
    report = json_object(result.stdout)
    check_report(report, ["stub/first", "stub/second"])
    check_generated_at(report, result)
    assert [
        [issue["number"] for issue in entry["issues"]] for entry in report["repos"]
    ] == [[1], [2]]
    assert all(entry["error"] is None for entry in report["repos"])
    records = read_trace(server)
    assert [(record["status"], record["attempt"], record["from_cache"], record["error"])
            for record in records] == [(200, 1, False, None), (200, 1, False, None)]
