"""Download and cache the matching vssh Go binary for the current platform.

`pip install vssh` ships this thin wrapper; the actual CLI is a Go binary
released on GitHub. On first use we fetch the release asset that matches the
package version, verify its SHA-256 against the published checksums.txt, cache
it under ~/.vssh/bin, and hand back the path. Pure stdlib (no deps).
"""

from __future__ import annotations

import hashlib
import os
import platform
import stat
import sys
import tempfile
import urllib.request
from pathlib import Path

from ._version import BINARY_VERSION

REPO = "zeus-kim/vssh"


class BinaryError(RuntimeError):
    """Raised when the vssh binary cannot be located, downloaded, or verified."""


def _platform() -> str:
    p = sys.platform
    if p.startswith("linux"):
        return "linux"
    if p == "darwin":
        return "darwin"
    raise BinaryError(
        f"unsupported OS '{p}': vssh ships binaries for linux and darwin only. "
        "Build from source: go build -o vssh ./cmd/vssh"
    )


def _arch() -> str:
    m = platform.machine().lower()
    if m in ("x86_64", "amd64"):
        return "amd64"
    if m in ("aarch64", "arm64"):
        return "arm64"
    raise BinaryError(f"unsupported architecture '{m}': expected x86_64/arm64")


def asset_name() -> str:
    return f"vssh-{_platform()}-{_arch()}"


def cache_dir() -> Path:
    root = os.environ.get("VSSH_HOME") or os.path.join(Path.home(), ".vssh")
    return Path(root) / "bin"


def _release_base(version: str) -> str:
    if version == "latest":
        return f"https://github.com/{REPO}/releases/latest/download"
    return f"https://github.com/{REPO}/releases/download/v{version.lstrip('v')}"


def _download(url: str, dest: Path) -> None:
    try:
        with urllib.request.urlopen(url, timeout=60) as resp:  # noqa: S310
            dest.write_bytes(resp.read())
    except Exception as exc:  # pragma: no cover - network dependent
        raise BinaryError(f"download failed: {url}: {exc}") from exc


def _sha256(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(65536), b""):
            h.update(chunk)
    return h.hexdigest()


def _verify(binary: Path, checksums_url: str, name: str) -> None:
    """Verify binary SHA-256 against checksums.txt; fail closed on mismatch."""
    tmp = binary.with_suffix(".checksums")
    try:
        _download(checksums_url, tmp)
    except BinaryError:
        # No checksums published: warn but do not hard-fail (parity with install.sh).
        sys.stderr.write("vssh: WARNING checksums.txt unavailable, skipping verification\n")
        return
    expected = ""
    for line in tmp.read_text().splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[1] == name:
            expected = parts[0]
            break
    tmp.unlink(missing_ok=True)
    if not expected:
        raise BinaryError(f"no checksum entry for {name}")
    actual = _sha256(binary)
    if expected != actual:
        binary.unlink(missing_ok=True)
        raise BinaryError(f"checksum mismatch for {name}: expected {expected}, got {actual}")


def ensure_binary(version: str | None = None) -> str:
    """Return a path to a usable vssh binary, downloading+verifying if needed.

    Resolution order:
      1. $VSSH_BIN if it points at an executable file.
      2. Cached binary under ~/.vssh/bin (validated against checksum once).
      3. Download the release asset matching `version` (default: package version,
         override with $VSSH_VERSION or the `version` arg), verify, cache, return.
    """
    override = os.environ.get("VSSH_BIN")
    if override and os.path.isfile(override) and os.access(override, os.X_OK):
        return override

    version = version or os.environ.get("VSSH_VERSION") or BINARY_VERSION
    name = asset_name()
    target = cache_dir() / "vssh"

    if target.is_file() and os.access(target, os.X_OK):
        return str(target)

    target.parent.mkdir(parents=True, exist_ok=True)
    base = _release_base(version)
    with tempfile.NamedTemporaryFile(delete=False, dir=target.parent) as tf:
        tmp_path = Path(tf.name)
    try:
        _download(f"{base}/{name}", tmp_path)
        _verify(tmp_path, f"{base}/checksums.txt", name)
        tmp_path.chmod(tmp_path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        os.replace(tmp_path, target)  # atomic
    finally:
        if tmp_path.exists():
            tmp_path.unlink(missing_ok=True)
    return str(target)
