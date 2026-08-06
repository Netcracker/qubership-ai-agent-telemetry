#!/usr/bin/env python3
"""Serve deterministic GitHub API, moving refs, and hostile archives for maintenance tests."""

from __future__ import annotations

import argparse
import hashlib
import io
import json
import pathlib
import tarfile
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


SHA = "0123456789abcdef0123456789abcdef01234567"
MOVING_SHA = "1111111111111111111111111111111111111111"
MOVED_SHA = "2222222222222222222222222222222222222222"
ARCHIVE_SHAS = {
    "collision": "9999999999999999999999999999999999999999",
    "traversal": "3333333333333333333333333333333333333333",
    "link": "4444444444444444444444444444444444444444",
    "canonical": "5555555555555555555555555555555555555555",
    "unexpected": "6666666666666666666666666666666666666666",
    "special": "7777777777777777777777777777777777777777",
    "reserved": "8888888888888888888888888888888888888888",
    "active": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    "implicit": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    "explicit": "cccccccccccccccccccccccccccccccccccccccc",
    "large": "dddddddddddddddddddddddddddddddddddddddd",
}
COMPOSE = b"""services:
  caddy:
    image: caddy:2
  collector:
    image: otel/opentelemetry-collector-contrib:0.119.0
  grafana:
    build:
      context: ./grafana
  victorialogs:
    image: victoriametrics/victoria-logs:v1.16.0-victorialogs
  victoriametrics:
    image: victoriametrics/victoria-metrics:v1.148.0
  alertmanager:
    image: example/alertmanager:1.0
"""


def archive(root: str, variant: str = "valid") -> bytes:
    payload = io.BytesIO()
    with tarfile.open(fileobj=payload, mode="w:gz") as tar:
        entries = [
            (f"{root}/README.md", tarfile.REGTYPE, b"# Repository fixture\n"),
            (f"{root}/telemetry-backend/docker-compose.yml", tarfile.REGTYPE, COMPOSE),
            (f"{root}/telemetry-backend/.env.example", tarfile.REGTYPE, b"SITE_ADDRESS=fixture.invalid\n"),
            (f"{root}/telemetry-backend/grafana/Dockerfile", tarfile.REGTYPE, b"FROM scratch\n"),
        ]
        if variant == "traversal":
            entries.append((f"{root}/telemetry-backend/../escape", tarfile.REGTYPE, b"escape\n"))
        elif variant == "link":
            entries.append((f"{root}/telemetry-backend/linked", tarfile.SYMTYPE, b""))
        elif variant == "canonical":
            entries.extend(
                [
                    (f"{root}/telemetry-backend/duplicate", tarfile.REGTYPE, b"one\n"),
                    (f"{root}/telemetry-backend/duplicate/", tarfile.DIRTYPE, b""),
                ]
            )
        elif variant == "unexpected":
            entries.append((f"{root}/other-root/file", tarfile.REGTYPE, b"wrong\n"))
        elif variant == "special":
            entries.append((f"{root}/telemetry-backend/fifo", tarfile.FIFOTYPE, b""))
        elif variant == "reserved":
            entries.append((f"{root}/telemetry-backend/.deployment-manifest", tarfile.REGTYPE, b"bad\n"))
        elif variant == "active":
            entries.append((f"{root}/telemetry-backend/.maintenance-active", tarfile.REGTYPE, b"forged\n"))
        elif variant == "explicit":
            entries.append((f"{root}/telemetry-backend/grafana/", tarfile.DIRTYPE, b""))
        elif variant == "large":
            entries.append((f"{root}/telemetry-backend/large.bin", tarfile.REGTYPE, b"x" * (2 * 1024 * 1024)))
        for name, entry_type, content in entries:
            entry = tarfile.TarInfo(name)
            entry.type = entry_type
            if entry_type == tarfile.SYMTYPE:
                entry.linkname = "../../outside"
            elif entry_type == tarfile.REGTYPE:
                entry.size = len(content)
            tar.addfile(entry, io.BytesIO(content) if entry_type == tarfile.REGTYPE else None)
    return payload.getvalue()


RELEASE_ARCHIVE = archive("release-7f4c")
SOURCE_ARCHIVES = {SHA: archive("Netcracker-qubership-ai-agent-telemetry-0123456"), MOVING_SHA: archive("moving-a")}
SOURCE_ARCHIVES[MOVED_SHA] = archive("moving-b")
SOURCE_ARCHIVES.update({value: archive(f"bad-{key}", key) for key, value in ARCHIVE_SHAS.items()})
RELEASE_SUM = hashlib.sha256(RELEASE_ARCHIVE).hexdigest()


def hostile_release_archive(variant: str) -> bytes:
    payload = io.BytesIO()
    with tarfile.open(fileobj=payload, mode="w:gz") as tar:
        entries = [
            ("docker-compose.yml", tarfile.REGTYPE, COMPOSE),
            (".env.example", tarfile.REGTYPE, b"SITE_ADDRESS=fixture.invalid\n"),
            ("grafana/Dockerfile", tarfile.REGTYPE, b"FROM scratch\n"),
        ]
        if variant == "traversal":
            entries.append(("../escape", tarfile.REGTYPE, b"escape\n"))
        elif variant == "link":
            entries.append(("linked", tarfile.SYMTYPE, b""))
        elif variant == "special":
            entries.append(("fifo", tarfile.FIFOTYPE, b""))
        elif variant == "reserved":
            entries.append((".deployment-manifest", tarfile.REGTYPE, b"bad\n"))
        elif variant == "missing":
            entries = [entry for entry in entries if entry[0] != "docker-compose.yml"]
        elif variant == "wrapped":
            entries = [(f"repository/telemetry-backend/{name}", entry_type, content)
                       for name, entry_type, content in entries]
        for name, entry_type, content in entries:
            entry = tarfile.TarInfo(name)
            entry.type = entry_type
            if entry_type == tarfile.SYMTYPE:
                entry.linkname = "../../outside"
            elif entry_type == tarfile.REGTYPE:
                entry.size = len(content)
            tar.addfile(entry, io.BytesIO(content) if entry_type == tarfile.REGTYPE else None)
    return payload.getvalue()


HOSTILE_RELEASE_ARCHIVES = {
    variant: hostile_release_archive(variant)
    for variant in ("traversal", "link", "special", "reserved", "missing", "wrapped")
}


class Handler(BaseHTTPRequestHandler):
    moving_requests = 0
    requests: list[str] = []

    def log_message(self, _format: str, *_args: object) -> None:
        pass

    def respond(self, status: int, body: bytes, content_type: str = "application/json") -> None:
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def redirect(self, target: str) -> None:
        self.send_response(302)
        self.send_header("Location", target)
        self.end_headers()

    def release(self, tag: str, archive: str = "/assets/backend", checksums: str = "/assets/checksums",
                duplicate: bool = False) -> None:
        base = f"http://{self.server.server_address[0]}:{self.server.server_address[1]}"
        assets = [
            {"name": "ai-agent-telemetry-backend.tar.gz", "browser_download_url": f"{base}{archive}"},
            {"name": "SHA256SUMS", "browser_download_url": f"{base}{checksums}"},
        ]
        if duplicate:
            assets.insert(1, {"name": "ai-agent-telemetry-backend.tar.gz", "browser_download_url": f"{base}/assets/backend"})
        self.respond(200, json.dumps({"tag_name": tag, "assets": assets}).encode())

    def commit(self, ref: str) -> None:
        if ref in {"main", SHA}:
            sha = SHA
        elif ref == "moving":
            self.__class__.moving_requests += 1
            sha = MOVING_SHA if self.moving_requests == 1 else MOVED_SHA
        elif ref == "feature/with space":
            sha = SHA
        elif ref in ARCHIVE_SHAS:
            sha = ARCHIVE_SHAS[ref]
        elif ref == "malformed":
            self.respond(200, b"{")
            return
        else:
            self.respond(404, json.dumps({"message": "Not Found"}).encode())
            return
        self.respond(200, json.dumps({"sha": sha}).encode())

    def do_GET(self) -> None:  # noqa: N802
        raw_path = self.path
        self.__class__.requests.append(raw_path)
        prefix = "/api/repos/Netcracker/qubership-ai-agent-telemetry/"
        if raw_path == "/requests":
            self.respond(200, json.dumps(self.requests).encode())
        elif raw_path == prefix + "releases/latest":
            self.release("v1.2.3")
        elif raw_path in {prefix + "releases/tags/v1.2.3", prefix + "releases/tags/v1.2.3%2Bbuild.7"}:
            self.release("v1.2.3+build.7" if raw_path.endswith("build.7") else "v1.2.3")
        elif raw_path == prefix + "releases/tags/v1.2.4":
            self.release("v1.2.4", duplicate=True)
        elif raw_path == prefix + "releases/tags/v1.2.5":
            self.release("v1.2.5", checksums="/assets/bad-checksums")
        elif raw_path == prefix + "releases/tags/v1.2.6":
            self.respond(200, b"{")
        elif raw_path.startswith(prefix + "releases/tags/v1.3."):
            variants = {
                "v1.3.0": "traversal",
                "v1.3.1": "link",
                "v1.3.2": "special",
                "v1.3.3": "reserved",
                "v1.3.4": "missing",
                "v1.3.5": "wrapped",
            }
            tag = raw_path.rsplit("/", 1)[1]
            variant = variants.get(tag)
            if variant is None:
                self.respond(404, json.dumps({"message": "Not Found"}).encode())
            else:
                self.release(tag, f"/assets/hostile/{variant}", f"/assets/hostile-checksums/{variant}")
        elif raw_path.startswith(prefix + "commits/"):
            self.commit(urllib.parse.unquote(raw_path.removeprefix(prefix + "commits/")))
        elif raw_path == "/assets/backend":
            self.redirect("/opaque/release-asset-7f4c")
        elif raw_path == "/assets/checksums":
            self.respond(200, f"{RELEASE_SUM}  ai-agent-telemetry-backend.tar.gz\n".encode(), "text/plain")
        elif raw_path == "/assets/bad-checksums":
            self.respond(200, b"0" * 64 + b"  ai-agent-telemetry-backend.tar.gz\n", "text/plain")
        elif raw_path.startswith("/assets/hostile-checksums/"):
            variant = raw_path.rsplit("/", 1)[1]
            payload = HOSTILE_RELEASE_ARCHIVES.get(variant)
            if payload is None:
                self.respond(404, b"not found\n", "text/plain")
            else:
                checksum = hashlib.sha256(payload).hexdigest()
                self.respond(200, f"{checksum}  ai-agent-telemetry-backend.tar.gz\n".encode(), "text/plain")
        elif raw_path.startswith("/assets/hostile/"):
            variant = raw_path.rsplit("/", 1)[1]
            payload = HOSTILE_RELEASE_ARCHIVES.get(variant)
            if payload is None:
                self.respond(404, b"not found\n", "text/plain")
            else:
                self.respond(200, payload, "application/gzip")
        elif raw_path == "/opaque/release-asset-7f4c":
            self.respond(200, RELEASE_ARCHIVE, "application/gzip")
        elif raw_path.startswith("/tarballs/") or raw_path.startswith(prefix + "tarball/"):
            sha = raw_path.rsplit("/", 1)[1]
            if sha in SOURCE_ARCHIVES:
                self.redirect(f"/opaque/source-{sha[:8]}")
            else:
                self.respond(404, json.dumps({"message": "Not Found"}).encode())
        elif raw_path.startswith("/opaque/source-"):
            sha_prefix = raw_path.removeprefix("/opaque/source-")
            for sha, payload in SOURCE_ARCHIVES.items():
                if sha.startswith(sha_prefix):
                    self.respond(200, payload, "application/gzip")
                    return
            self.respond(404, json.dumps({"message": "Not Found"}).encode())
        else:
            self.respond(404, json.dumps({"message": "Not Found"}).encode())


def main() -> None:
    global RELEASE_ARCHIVE, RELEASE_SUM

    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--release-archive", type=pathlib.Path)
    args = parser.parse_args()
    if args.release_archive is not None:
        RELEASE_ARCHIVE = args.release_archive.read_bytes()
        RELEASE_SUM = hashlib.sha256(RELEASE_ARCHIVE).hexdigest()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print(server.server_address[1], flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
