import pytest

from ..helpers.checks import check_no_token


@pytest.mark.parametrize(
    "args,base_override",
    [
        pytest.param((), None, id="missing-repos"),
        pytest.param(("--repos", "owner"), None, id="invalid-repo"),
        pytest.param(("--repos", "o/r", "--limit", "0"), None, id="invalid-limit"),
        pytest.param(("--repos", "o/r", "--since", "2026-02-30"), None, id="invalid-date"),
        pytest.param(
            ("--repos", "o/r", "--unknown=integration-test-secret"),
            None, id="unknown-flag",
        ),
        pytest.param(("--repos", "o/r", "--limit"), None, id="missing-value"),
        pytest.param(("--repos", "o/r", "extra"), None, id="positional-argument"),
        pytest.param(("--repos", "o/r"), "", id="empty-api-base"),
        pytest.param(
            ("--repos", "o/r"),
            "https://user:integration-test-secret@example.invalid",
            id="invalid-api-base",
        ),
    ],
)
def test_invalid_arguments_preserve_files(
    args, base_override, mock_server, run_ghreport, work_dir, cache_dir, api_token
):
    def unexpected(request):
        raise AssertionError("Invalid configuration caused an HTTP request")

    server = mock_server(unexpected)
    preserved = b"preserve existing contents"
    report_path = work_dir / "report.json"
    trace_path = work_dir / "trace.jsonl"
    for path in (report_path, trace_path):
        path.write_bytes(preserved)
    result = run_ghreport(
        "--out", "report.json", "--trace", "trace.jsonl", *args,
        base_url=server.base_url if base_override is None else base_override,
        token=api_token,
    )

    assert result.returncode == 2
    assert result.stdout == ""
    assert result.stderr.strip(), "Missing argument diagnostic"
    check_no_token(api_token, result.stdout, result.stderr)
    assert report_path.read_bytes() == preserved
    assert trace_path.read_bytes() == preserved
    assert not cache_dir.exists()
    assert {path.name for path in work_dir.iterdir()} == {"report.json", "trace.jsonl"}
    assert server.requests == ()
