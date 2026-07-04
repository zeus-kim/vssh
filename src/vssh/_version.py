"""Version constants for the vssh package.

The PyPI *package* version and the Go *binary* version are intentionally
decoupled: this `vssh` package already has a PyPI history up to 4.x, while the
CLI binary is versioned independently on the 0.7.x line (see cmd/vssh/main.go).
`pip install vssh` resolves to the highest package version, so the package must
stay on the 4.x line to be the default install; the launcher then fetches the
pinned BINARY_VERSION release asset from GitHub.
"""

__version__ = "4.3.4"        # PyPI package version (hatch reads this)
BINARY_VERSION = "0.7.48"    # GitHub release tag the launcher downloads (== cmd/vssh/main.go)
