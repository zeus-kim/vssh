# vssh policy templates (P1b, docs/SECURITY_TRANSPORT_MIGRATION.md §6)

Per-key command/path whitelists. A key opts in via its `authorized_keys` line:

    <pubB64> caps=exec policy=backup m1-backup

Install a policy on a node as `/etc/vssh/policies/<name>.json` (root-owned 0644)
or `~/.vssh/policies/<name>.json`. Hot-reloaded on change. A key tagged
`policy=<name>` whose file is missing/invalid **fails closed** (key unusable).

Evaluation (daemon, authoritative): `exec_deny` first, then
`danger_preapproved`, then `exec_allow`; **no match = refuse** (typed
`policy_denied`, with the matched rule id in the audit record). Rules should be
fully anchored (`^...$`) — the daemon warns on load otherwise, because an
unanchored rule can match a substring of a metachar-smuggled command.

Templates:
- `readonly` — no exec at all (pair with `caps=rpc`); read-only facts/rpc key.
- `backup`   — rsync into vault + backup.sh; file scope `/var/backups/**`.
- `ci`       — allow-listed make/go/git build commands.
- `deploy`   — deploy.sh + `danger_preapproved` systemctl restart (vsshd/caddy/nginx).

`danger_preapproved` patterns run without interactive MCP approval but are
audited with `preapproved:<rule_id>`. The MCP auto-approve profile is generated
from the same file (single source of truth) — never hand-maintained.
