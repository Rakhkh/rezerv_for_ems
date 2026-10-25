"""A local HTTP server with a per-test handler and a thread-safe request log."""

import json
import socket
import threading
import time
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import MappingProxyType
from typing import Callable, Mapping


@dataclass(frozen=True)
class Request:
    method: str
    target: str
    headers: Mapping[str, str] = field(repr=False)
    received_at: float


@dataclass(frozen=True)
class Response:
    status: int
    body: bytes = b""
    headers: Mapping[str, str] = field(default_factory=dict)

    @classmethod
    def json(
        cls, value: object, *, status: int = 200,
        headers: Mapping[str, str] | None = None,
    ) -> "Response":
        return cls(
            status,
            json.dumps(value, ensure_ascii=False).encode("utf-8"),
            {"Content-Type": "application/json; charset=utf-8", **(headers or {})},
        )


class _HTTPServer(ThreadingHTTPServer):
    daemon_threads = True
    block_on_close = False

    def process_request(self, connection, address):
        connection.settimeout(2)
        worker = threading.Thread(
            target=self.process_request_thread,
            args=(connection, address),
            daemon=True,
        )
        with self.owner._lock:
            self.owner._connections.add(connection)
            self.owner._workers.append(worker)
        worker.start()

    def process_request_thread(self, connection, address):
        try:
            super().process_request_thread(connection, address)
        finally:
            with self.owner._lock:
                self.owner._connections.discard(connection)

    def handle_error(self, request, client_address):
        # Keep worker errors visible to pytest, without dumping HTTP headers.
        self.owner._record_error("HTTP worker failed")


class MockServer:
    def __init__(self, handler: Callable[[Request], Response]):
        self._handler = handler
        self._lock = threading.Lock()
        self._requests: list[Request] = []
        self._errors: list[str] = []
        self._active_handlers = 0
        self._max_active_handlers = 0
        self._connections: set[socket.socket] = set()
        self._workers: list[threading.Thread] = []
        self._closed = False
        owner = self

        class Handler(BaseHTTPRequestHandler):
            # HTTP/1.0 closes each connection; idle keep-alive sockets cannot
            # keep a worker alive during fixture cleanup.
            def respond(self):
                request = Request(
                    self.command,
                    self.path,
                    MappingProxyType(
                        {key.lower(): value for key, value in self.headers.items()}
                    ),
                    time.monotonic(),
                )
                with owner._lock:
                    owner._requests.append(request)
                    owner._active_handlers += 1
                    owner._max_active_handlers = max(
                        owner._max_active_handlers, owner._active_handlers
                    )
                try:
                    response = owner._handler(request)
                    if not isinstance(response, Response):
                        raise TypeError("handler must return Response")
                except Exception as exc:
                    owner._record_error(f"{type(exc).__name__}: {exc}")
                    response = Response(400)
                finally:
                    with owner._lock:
                        owner._active_handlers -= 1
                try:
                    self.send_response(response.status)
                    for key, value in response.headers.items():
                        self.send_header(key, value)
                    if not any(key.lower() == "content-length" for key in response.headers):
                        self.send_header("Content-Length", str(len(response.body)))
                    self.end_headers()
                    if self.command != "HEAD":
                        self.wfile.write(response.body)
                except (BrokenPipeError, ConnectionResetError):
                    # A timed-out client may close before a prepared reply.
                    pass

            do_GET = respond
            do_POST = respond
            do_PUT = respond
            do_PATCH = respond
            do_DELETE = respond
            do_HEAD = respond
            do_OPTIONS = respond

            def log_message(self, format, *args):
                pass

        self._server = _HTTPServer(("127.0.0.1", 0), Handler)
        self._server.owner = self
        self._thread = threading.Thread(
            target=self._server.serve_forever,
            kwargs={"poll_interval": 0.05},
            daemon=True,
        )
        self._thread.start()

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self._server.server_port}"

    @property
    def requests(self) -> tuple[Request, ...]:
        with self._lock:
            return tuple(self._requests)

    @property
    def max_active_handlers(self) -> int:
        """Observed overlap while preparing responses, not idle connections."""
        with self._lock:
            return self._max_active_handlers

    def _record_error(self, message: str):
        with self._lock:
            self._errors.append(message)

    def assert_ok(self):
        with self._lock:
            errors = tuple(self._errors)
        assert not errors, "Mock server errors:\n" + "\n".join(errors)

    def close(self):
        if self._closed:
            return
        self._closed = True
        self._server.shutdown()
        self._server.server_close()
        self._thread.join(timeout=2)
        with self._lock:
            connections = tuple(self._connections)
            workers = tuple(self._workers)
        for connection in connections:
            try:
                connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            connection.close()
        deadline = time.monotonic() + 2
        for worker in workers:
            worker.join(timeout=max(0, deadline - time.monotonic()))
        assert not self._thread.is_alive(), "Mock listener did not stop"
        assert not any(worker.is_alive() for worker in workers), (
            "Mock handler did not stop"
        )
