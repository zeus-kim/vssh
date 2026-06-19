"""Console-script entry point: exec the real vssh Go binary with passthrough args."""

from __future__ import annotations

import os
import sys

from ._bootstrap import BinaryError, ensure_binary


def main() -> int:
    try:
        binary = ensure_binary()
    except BinaryError as exc:
        sys.stderr.write(f"vssh: {exc}\n")
        return 1
    argv = [binary, *sys.argv[1:]]
    os.execv(binary, argv)  # replaces this process; does not return
    return 0  # pragma: no cover
