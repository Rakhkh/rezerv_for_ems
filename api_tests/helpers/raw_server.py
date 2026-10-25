"""One raw HTTP/1.1 scenario; the regular mock server remains HTTP/1.0."""

import socket
import threading
import time
from types import MappingProxyType
from urllib.parse import urlsplit

from .server import Request


class RawTransportServer:
    def __init__(self, token: str):
        self._token = token
        self._lock = threading.Lock()
        self._requests: list[Request] = []
        self._errors: list[str] = []
        self._connection = None
        self._stop = threading.Event()
        self.first_connection_closed = threading.Event()
        self._listener = socket.socket()
        self._listener.bind(("127.0.0.1", 0))
        self._listener.listen()
        self._listener.settimeout(0.1)
        self.base_url = "http://127.0.0.1:%d" % self._listener.getsockname()[1]
        self._thread = threading.Thread(target=self._serve, daemon=True)
        self._thread.start()

    def _accept(self):
        deadline = time.monotonic() + 5
        while not self._stop.is_set() and time.monotonic() < deadline:
            try:
                return self._listener.accept()[0]
            except socket.timeout:
                pass
        raise TimeoutError("Raw HTTP client did not connect")

    def _read_request(self, connection):
        data = b""
        deadline = time.monotonic() + 5
        while b"\r\n\r\n" not in data:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or len(data) >= 65536:
                raise TimeoutError("Raw HTTP request headers were not completed")
            connection.settimeout(remaining)
            chunk = connection.recv(min(4096, 65536 - len(data)))
            if not chunk:
                raise AssertionError("Raw HTTP client closed before request headers")
            data += chunk
        head, tail = data.split(b"\r\n\r\n", 1)
        assert not tail, "Unexpected request body"
        lines = head.decode("iso-8859-1").split("\r\n")
        method, target, version = lines[0].split(" ")
        assert version == "HTTP/1.1"
        headers = {}
        for line in lines[1:]:
            key, separator, value = line.partition(":")
            assert separator, "Malformed request header"
            headers[key.lower()] = value.strip()
        return Request(method, target, MappingProxyType(headers), time.monotonic())

    def _serve(self):
        try:
            for name in ("first", "second"):
                with self._accept() as connection:
                    with self._lock:
                        self._connection = connection
                    request = self._read_request(connection)
                    with self._lock:
                        self._requests.append(request)
                    assert request.method == "GET"
                    assert urlsplit(request.target).path == f"/repos/stub/{name}/issues"
                    assert request.headers.get("authorization") == "Bearer " + self._token
                    connection.settimeout(5)
                    if name == "first":
                        connection.sendall(
                            b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n[]"
                            + self._token.encode("ascii")
                        )
                        assert connection.recv(1) == b"", (
                            "Transport reused the malformed connection instead of closing it"
                        )
                        self.first_connection_closed.set()
                    else:
                        # This response is gated by EOF on the first connection.
                        # The process cannot exit before handling the extra bytes.
                        connection.sendall(
                            b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\n[]"
                        )
                    with self._lock:
                        self._connection = None
        except Exception as exc:
            if not self._stop.is_set():
                with self._lock:
                    self._errors.append(type(exc).__name__ + ": " + str(exc))

    @property
    def requests(self):
        with self._lock:
            return tuple(self._requests)

    def assert_ok(self):
        with self._lock:
            assert not self._errors, "Raw HTTP scenario failed: " + "; ".join(self._errors)

    def close(self):
        self._stop.set()
        self._listener.close()
        with self._lock:
            connection = self._connection
        if connection is not None:
            try:
                connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        self._thread.join(timeout=2)
        assert not self._thread.is_alive(), "Raw HTTP server did not stop"
