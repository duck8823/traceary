"""Throwaway native UDS probe, NOT Traceary daemon/public protocol/auth code."""
import ctypes
import json
import os
from pathlib import Path
import socket
import stat
import struct
import sys
import time

MAX_FRAME = 65536


class Rejected(ValueError):
    pass


class Endpoint:
    def __init__(self, root):
        self.root = Path(root)
        self.path = self.root / "daemon.sock"
        self.socket = None
        self.inode = None

    def bind(self):
        if self.socket is not None:
            raise Rejected("endpoint already open")
        if not self.root.is_absolute() or self.root.resolve() != self.root:
            raise Rejected("absolute non-symlink directory required")
        info = self.root.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise Rejected("owner-private directory required")
        if len(os.fsencode(self.path)) >= 104:
            raise Rejected("macOS socket path too long")
        if os.path.lexists(self.path):
            raise Rejected("existing socket path is never removed")
        self.socket = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        try:
            self.socket.bind(str(self.path))
            info = self.path.lstat()
            self.inode = (info.st_dev, info.st_ino)
            self.path.chmod(0o600)
            self.socket.listen(1)
            self.socket.settimeout(0.25)
        except BaseException:
            self.close()
            raise

    def close(self):
        if self.socket is not None:
            self.socket.close()
            self.socket = None
        if self.inode is not None and os.path.lexists(self.path):
            info = self.path.lstat()
            if (info.st_dev, info.st_ino) == self.inode and stat.S_ISSOCK(info.st_mode) and info.st_uid == os.getuid():
                self.path.unlink()
        self.inode = None


def peer_uid(connection):
    if sys.platform != "darwin":
        raise Rejected("this probe requires macOS getpeereid")
    library = ctypes.CDLL(None, use_errno=True)
    call = library.getpeereid
    call.argtypes = [ctypes.c_int, ctypes.POINTER(ctypes.c_uint), ctypes.POINTER(ctypes.c_uint)]
    call.restype = ctypes.c_int
    uid, gid = ctypes.c_uint(), ctypes.c_uint()
    if call(connection.fileno(), ctypes.byref(uid), ctypes.byref(gid)) != 0:
        raise OSError(ctypes.get_errno(), "getpeereid failed")
    return uid.value


def _remaining(deadline):
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise Rejected("total exchange deadline expired")
    return remaining


def _read_exact(connection, count, deadline):
    parts = bytearray()
    while len(parts) < count:
        connection.settimeout(_remaining(deadline))
        try:
            chunk = connection.recv(count - len(parts))
        except socket.timeout as error:
            raise Rejected("total exchange deadline expired") from error
        if not chunk:
            raise Rejected("partial frame or disconnect")
        parts.extend(chunk)
    return bytes(parts)


def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise Rejected("duplicate JSON field")
        result[key] = value
    return result


def _constant(value):
    raise Rejected("non-finite JSON number")


def encode_frame(message):
    if not isinstance(message, dict):
        raise Rejected("object frame required")
    try:
        body = json.dumps(message, ensure_ascii=False, allow_nan=False, separators=(",", ":")).encode("utf-8")
    except (ValueError, TypeError, UnicodeError, RecursionError) as error:
        raise Rejected("invalid JSON frame") from error
    if not 0 < len(body) <= MAX_FRAME:
        raise Rejected("oversize frame")
    return struct.pack("!I", len(body)) + body


def read_frame(connection, deadline):
    size = struct.unpack("!I", _read_exact(connection, 4, deadline))[0]
    if not 0 < size <= MAX_FRAME:
        raise Rejected("invalid frame length")
    body = _read_exact(connection, size, deadline)
    try:
        result = json.loads(body.decode("utf-8"), object_pairs_hook=_pairs, parse_constant=_constant)
    except (ValueError, UnicodeError, RecursionError) as error:
        raise Rejected("invalid JSON frame") from error
    if not isinstance(result, dict):
        raise Rejected("object frame required")
    return result


def _envelope(message, kind):
    if set(message) != {"version", "kind", "payload"} or type(message["version"]) is not int or message["version"] != 1 or message["kind"] != kind or not isinstance(message["payload"], dict):
        raise Rejected("unsupported envelope")
    return message["payload"]


def _send(connection, message, deadline):
    connection.settimeout(_remaining(deadline))
    try:
        connection.sendall(encode_frame(message))
    except socket.timeout as error:
        raise Rejected("total exchange deadline expired") from error


def serve_one(connection, binding, expected_uid, deadline):
    """One experiment exchange; receipt is neither authority nor durable history."""
    if peer_uid(connection) != expected_uid:
        raise Rejected("peer UID mismatch")
    if _envelope(read_frame(connection, deadline), "hello"):
        raise Rejected("unexpected handshake data")
    _send(connection, {"version": 1, "kind": "hello_ack", "payload": {}}, deadline)
    payload = _envelope(read_frame(connection, deadline), "observation")
    fields = {"run", "generation", "event", "source_event_id", "host", "host_version"}
    if set(payload) != fields or not all(isinstance(value, str) and 1 <= len(value) <= 128 and not any(ord(c) < 32 or 127 <= ord(c) <= 159 for c in value) for value in payload.values()):
        raise Rejected("metadata-only observation required")
    if (payload["run"], payload["generation"]) != binding:
        raise Rejected("foreign run or stale generation")
    if payload["event"] not in {"turn_boundary", "working_observed", "unknown"}:
        raise Rejected("unsupported observation event")
    _send(connection, {"version": 1, "kind": "receipt", "payload": {"received": True}}, deadline)
    return "observation_received"
