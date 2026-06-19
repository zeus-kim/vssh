"""vssh — AI-native remote execution daemon.

`pip install vssh` gives you both:
  * the `vssh` CLI (a Go binary auto-downloaded for your platform on first use), and
  * the Python SDK: `from vssh import VSSH`.
"""

from ._bootstrap import BinaryError, ensure_binary
from ._version import __version__
from .client import ExecResult, MultiExecResult, MultiRPCResult, VSSH, VSSHError

__all__ = [
    "BinaryError",
    "ExecResult",
    "MultiExecResult",
    "MultiRPCResult",
    "VSSH",
    "VSSHError",
    "__version__",
    "ensure_binary",
]
