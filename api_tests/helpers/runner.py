"""Run an already built executable without a shell or implicit API settings."""

import os
import subprocess
import time
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Sequence


@dataclass(frozen=True)
class RunResult:
    returncode: int
    stdout: str
    stderr: str
    started_at: datetime
    finished_at: datetime
    elapsed: float


def run_ghreport(
    binary: Path,
    work_dir: Path,
    cache_dir: Path,
    args: Sequence[str],
    *,
    base_url: str,
    token: str | None = None,
    timeout: float = 10,
) -> RunResult:
    env = {
        key: value
        for key, value in os.environ.items()
        if key.upper() not in {"GITHUB_API_BASE_URL", "GITHUB_TOKEN"}
    }
    env["GITHUB_API_BASE_URL"] = base_url
    if token is not None:
        env["GITHUB_TOKEN"] = token

    started_at = datetime.now(timezone.utc)
    started = time.monotonic()
    try:
        proc = subprocess.run(
            [str(binary), "--cache-dir", str(cache_dir), *args],
            cwd=work_dir,
            env=env,
            stdin=subprocess.DEVNULL,
            capture_output=True,
            timeout=timeout,
            check=False,
        )
    except subprocess.TimeoutExpired:
        # run() kills and waits for the child before raising. Do not print args,
        # env or partial output here: any of them could contain credentials.
        raise AssertionError(f"ghreport exceeded its {timeout:g}s timeout") from None
    except OSError as exc:
        raise AssertionError(
            f"Cannot start GHREPORT_BIN (OS error {exc.errno})"
        ) from None
    finished_at = datetime.now(timezone.utc)
    elapsed = time.monotonic() - started
    try:
        stdout = proc.stdout.decode("utf-8")
        stderr = proc.stderr.decode("utf-8")
    except UnicodeDecodeError:
        raise AssertionError("ghreport output is not valid UTF-8") from None
    return RunResult(
        proc.returncode, stdout, stderr, started_at, finished_at, elapsed
    )
