"""Explicit configuration for an API whose issue data is unknown to the tests."""

import os
from dataclasses import dataclass, field

import pytest

from ..helpers.checks import http_origin


@dataclass(frozen=True)
class LiveAPI:
    base_url: str
    repos: tuple[str, ...]
    token: str | None = field(repr=False)
    origin: tuple[str, str, int]


@pytest.fixture(scope="session")
def live_api(pytestconfig):
    base = os.environ.get("GITHUB_API_BASE_URL")
    raw_repos = pytestconfig.getoption("--api-repos")
    if base is None and raw_repos is None:
        pytest.skip("Live API not configured: set GITHUB_API_BASE_URL and --api-repos")
    if not base or not raw_repos:
        pytest.fail("Live API requires both GITHUB_API_BASE_URL and --api-repos", pytrace=False)
    try:
        origin = http_origin(base)
        if any(ord(char) <= 32 or ord(char) == 127 for char in base):
            raise ValueError
        if any(char in base for char in "?#\\"):
            raise ValueError
    except ValueError:
        pytest.fail("GITHUB_API_BASE_URL must be HTTP(S), without credentials, query or fragment", pytrace=False)

    repos = []
    for item in raw_repos.split(","):
        repo = item.strip()
        parts = repo.split("/")
        if (
            len(parts) != 2 or any(part in {"", ".", ".."} for part in parts)
            or any(char.isspace() or ord(char) < 32 or ord(char) == 127 for char in repo)
            or any(char in repo for char in "?#\\")
        ):
            pytest.fail("--api-repos must contain comma-separated owner/repo names", pytrace=False)
        if repo not in repos:
            repos.append(repo)
    return LiveAPI(base, tuple(repos), os.environ.get("GITHUB_TOKEN"), origin)


@pytest.fixture
def run_live_api(live_api, request):
    # Resolve the binary fixture only after validating configuration. An entirely
    # unconfigured live-only run can skip even when GHREPORT_BIN is absent.
    run = request.getfixturevalue("run_ghreport")

    def launch(*args):
        return run(*args, base_url=live_api.base_url, token=live_api.token, timeout=120)

    return launch
