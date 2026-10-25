import os
from pathlib import Path

import pytest

from .helpers.runner import run_ghreport as run_process


def pytest_addoption(parser):
    parser.addoption(
        "--api-repos",
        help="Repositories for the live API tests: owner/repo[,owner/repo...]",
    )


@pytest.fixture(scope="session")
def ghreport_bin():
    value = os.environ.get("GHREPORT_BIN")
    if not value:
        pytest.fail("Set GHREPORT_BIN to the absolute path of the built service", pytrace=False)
    binary = Path(value)
    if not binary.is_absolute() or not binary.is_file():
        pytest.fail("GHREPORT_BIN must be an absolute path to an existing file", pytrace=False)
    return binary


@pytest.fixture
def work_dir(tmp_path):
    return tmp_path


@pytest.fixture
def cache_dir(work_dir):
    # Do not create it: invalid configuration must leave it absent.
    return work_dir / "cache"


@pytest.fixture
def run_ghreport(ghreport_bin, work_dir, cache_dir):
    def run(*args, base_url, token=None, timeout=10):
        return run_process(
            ghreport_bin, work_dir, cache_dir, args,
            base_url=base_url, token=token, timeout=timeout,
        )

    return run
