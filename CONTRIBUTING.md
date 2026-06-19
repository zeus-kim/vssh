# Contributing

Thanks for helping improve `vssh`. This is a Go binary with a small Python SDK.

## Development setup

```bash
git clone https://github.com/zeus-kim/vssh && cd vssh
make build          # build ./vssh
```

Requires Go 1.25+ (and Python 3.9+ for the SDK tests).

## Before opening a PR

```bash
go vet ./...
go build ./...
go test ./...                 # Go tests
make test                     # Go tests + Python SDK tests
bash -n install.sh            # when editing the installer
```

CI runs the same checks. Please make sure they pass locally.

## Pull requests

- Target `main`.
- Keep changes focused and describe behavior changes clearly — **especially
  anything touching authentication, transport, policy, or `vssh server`.**
- Update `CHANGELOG.md` and bump the version in `cmd/vssh/main.go` for
  user-visible changes.
- Add or update tests for new behavior.

## Releasing

Maintainers: tag a release (`vX.Y.Z`) on `main`. CI builds the cross-platform
binaries and publishes the GitHub release (checksummed) plus the Python
launcher. Bump the version in `cmd/vssh/main.go` and update `CHANGELOG.md` first.

## Public examples

This is a public repository. Never commit real node names, VPN/Tailscale IPs,
usernames, secrets, or private deployment notes. Use RFC 5737 documentation
ranges in examples (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`).

## Security

Report vulnerabilities privately via
[GitHub Security Advisories](https://github.com/zeus-kim/vssh/security/advisories/new),
not public issues or PRs.
