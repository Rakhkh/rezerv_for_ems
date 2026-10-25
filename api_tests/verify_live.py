"""Optional acceptance driver: exercise live tests through their public settings.

Run from the repository root: python -m api_tests.verify_live [--full]
No server data or expected issue IDs are passed to pytest.
"""

import argparse
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from urllib.parse import parse_qs, urlsplit

from .helpers.server import MockServer, Response


def issue(number, created="2023-01-01T00:00:00Z"):
    return {
        "number": number, "title": "Разные данные <&>",
        "user": {"login": "someone"}, "labels": [{"name": "метка"}],
        "created_at": created, "html_url": "https://example.invalid/never-fetch",
    }


def handler_for(scenario, token):
    calls = {}

    def handle(request):
        assert request.method == "GET"
        headers = {key.lower(): value for key, value in request.headers.items()}
        if token:
            assert headers.get("authorization") == "Bearer " + token
        else:
            assert "authorization" not in headers
        address = urlsplit(request.target)
        if address.path == "/opaque-next":
            assert scenario == "paged" and address.query == "cursor=next"
            return Response.json([
                issue(42, "2022-01-01T00:00:00Z"),
                issue(7, "2023-01-01T03:00:00+03:00"), issue(3),
            ])
        assert address.path in {
            "/api/v3/repos/lab/omega/issues", "/api/v3/repos/lab/empty/issues",
        }
        assert parse_qs(address.query) == {
            "state": ["open"], "sort": ["created"],
            "direction": ["asc"], "per_page": ["100"],
        }
        calls[address.path] = calls.get(address.path, 0) + 1
        if scenario == "empty" or address.path.endswith("/empty/issues"):
            return Response.json([])
        if scenario == "changing":
            count = calls[address.path]
            return Response.json([issue(100 + count)] if count % 2 else [])
        pull = issue(11, "2018-01-01T00:00:00Z")
        pull["pull_request"] = {}
        return Response.json(
            [pull, issue(8, "2019-01-01T00:00:00Z"),
             issue(42, "2022-01-01T00:00:00Z")],
            headers={"Link": '</opaque-next?cursor=next>; rel="next"', "ETag": '"v1"'},
        )

    return handle


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--full", action="store_true",
        help="Also run all mock tests once, together with the paged live API",
    )
    args = parser.parse_args()
    for scenario in ("empty", "paged", "changing"):
        token = None if scenario == "empty" else "live-driver-synthetic-secret"
        server = MockServer(handler_for(scenario, token))
        try:
            env = dict(os.environ)
            for key in tuple(env):
                if key.upper() in {"GITHUB_API_BASE_URL", "GITHUB_TOKEN"}:
                    del env[key]
            env["GITHUB_API_BASE_URL"] = server.base_url + "/api/v3/"
            if token:
                env["GITHUB_TOKEN"] = token
            group = "api_tests" if args.full and scenario == "paged" else "api_tests/live"
            print("Live acceptance API: " + scenario, flush=True)
            with tempfile.TemporaryDirectory(prefix="ghreport-live-") as directory:
                result = subprocess.run(
                    [sys.executable, "-m", "pytest", group, "-q", "-p", "no:cacheprovider",
                     "--api-repos", " lab/omega,lab/empty,lab/omega ",
                     "--basetemp", str(Path(directory) / "pytest"), "--durations=5"],
                    env=env, stdin=subprocess.DEVNULL, timeout=300, check=False,
                )
            if result.returncode:
                return result.returncode
        finally:
            server.close()
            server.assert_ok()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
