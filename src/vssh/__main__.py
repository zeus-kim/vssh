"""Allow `python -m vssh` to behave like the vssh CLI."""

from ._cli import main

if __name__ == "__main__":
    raise SystemExit(main())
