from __future__ import annotations

import json
import os
import subprocess
from dataclasses import dataclass
from typing import Any, Callable, Mapping, Sequence


CommandRunner = Callable[..., subprocess.CompletedProcess[str]]


class VSSHError(RuntimeError):
    """Raised when the vssh CLI cannot complete an SDK operation."""


@dataclass(frozen=True)
class ExecResult:
    success: bool
    command: str
    stdout: str
    stderr: str
    exit_code: int
    duration_ms: int
    error: str = ""

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "ExecResult":
        return cls(
            success=bool(data.get("success", False)),
            command=str(data.get("command", "")),
            stdout=str(data.get("stdout", "")),
            stderr=str(data.get("stderr", "")),
            exit_code=int(data.get("exit_code", 0)),
            duration_ms=int(data.get("duration_ms", 0)),
            error=str(data.get("error", "")),
        )


@dataclass(frozen=True)
class MultiExecResult:
    target: str
    result: ExecResult | None = None
    error: str = ""

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "MultiExecResult":
        raw_result = data.get("result")
        return cls(
            target=str(data.get("target", "")),
            result=ExecResult.from_dict(raw_result) if isinstance(raw_result, Mapping) else None,
            error=str(data.get("error", "")),
        )


@dataclass(frozen=True)
class MultiRPCResult:
    target: str
    result: Mapping[str, Any] | None = None
    error: str = ""

    @classmethod
    def from_dict(cls, data: Mapping[str, Any]) -> "MultiRPCResult":
        raw_result = data.get("result")
        return cls(
            target=str(data.get("target", "")),
            result=raw_result if isinstance(raw_result, Mapping) else None,
            error=str(data.get("error", "")),
        )


class VSSH:
    """Small Python client for the installed vssh binary.

    The Go daemon and CLI remain the source of truth. This SDK gives AI agents,
    MCP servers, notebooks, and automation scripts a stable Python surface
    without reimplementing the wire protocol.
    """

    def __init__(
        self,
        *,
        binary: str = "vssh",
        secret: str | None = None,
        port: int | None = None,
        env: Mapping[str, str] | None = None,
        timeout: float = 30.0,
        runner: CommandRunner | None = None,
    ) -> None:
        self.binary = binary
        self.timeout = timeout
        self.runner = runner or subprocess.run
        merged_env = dict(os.environ)
        if env:
            merged_env.update(env)
        if secret is not None:
            merged_env["VSSH_SECRET"] = secret
        if port is not None:
            merged_env["VSSH_PORT"] = str(port)
        self.env = merged_env

    def exec(self, target: str, command: str, *, timeout: float | None = None) -> ExecResult:
        proc = self._run(["run", target, command], timeout=timeout)
        return ExecResult(
            success=proc.returncode == 0,
            command=command,
            stdout=proc.stdout,
            stderr=proc.stderr,
            exit_code=proc.returncode,
            duration_ms=0,
            error="" if proc.returncode == 0 else proc.stderr.strip(),
        )

    def exec_many(
        self,
        targets: Sequence[str] | str,
        command: str,
        *,
        timeout: float | None = None,
    ) -> list[MultiExecResult]:
        payload = self._run_json(["run-many", self._target_list(targets), command], timeout=timeout)
        if not isinstance(payload, list):
            raise VSSHError("vssh run-many returned non-list JSON")
        return [MultiExecResult.from_dict(item) for item in payload if isinstance(item, Mapping)]

    def rpc(
        self,
        target: str,
        method: str,
        params: Mapping[str, Any] | None = None,
        *,
        timeout: float | None = None,
    ) -> Mapping[str, Any]:
        args = ["rpc", target, method]
        if params:
            args.append(json.dumps(params, separators=(",", ":")))
        payload = self._run_json(args, timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh rpc returned non-object JSON")
        return payload

    def rpc_many(
        self,
        targets: Sequence[str] | str,
        method: str,
        params: Mapping[str, Any] | None = None,
        *,
        timeout: float | None = None,
    ) -> list[MultiRPCResult]:
        args = ["rpc-many", self._target_list(targets), method]
        if params:
            args.append(json.dumps(params, separators=(",", ":")))
        payload = self._run_json(args, timeout=timeout)
        if not isinstance(payload, list):
            raise VSSHError("vssh rpc-many returned non-list JSON")
        return [MultiRPCResult.from_dict(item) for item in payload if isinstance(item, Mapping)]

    def facts(self, target: str, *, timeout: float | None = None) -> Mapping[str, Any]:
        payload = self._run_json(["facts", target], timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh facts returned non-object JSON")
        return payload

    def facts_many(
        self,
        targets: Sequence[str] | str,
        *,
        timeout: float | None = None,
    ) -> list[MultiRPCResult]:
        payload = self._run_json(["facts-many", self._target_list(targets)], timeout=timeout)
        if not isinstance(payload, list):
            raise VSSHError("vssh facts-many returned non-list JSON")
        return [MultiRPCResult.from_dict(item) for item in payload if isinstance(item, Mapping)]

    def job_start(self, target: str, command: str, *, timeout: float | None = None) -> Mapping[str, Any]:
        payload = self._run_json(["job-start", target, command], timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh job-start returned non-object JSON")
        return payload

    def job_status(self, target: str, job_id: str, *, timeout: float | None = None) -> Mapping[str, Any]:
        payload = self._run_json(["job-status", target, job_id], timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh job-status returned non-object JSON")
        return payload

    def job_logs(
        self,
        target: str,
        job_id: str,
        *,
        tail_bytes: int | None = None,
        timeout: float | None = None,
    ) -> Mapping[str, Any]:
        args = ["job-logs", target, job_id]
        if tail_bytes is not None:
            args.append(str(tail_bytes))
        payload = self._run_json(args, timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh job-logs returned non-object JSON")
        return payload

    def job_cancel(self, target: str, job_id: str, *, timeout: float | None = None) -> Mapping[str, Any]:
        payload = self._run_json(["job-cancel", target, job_id], timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh job-cancel returned non-object JSON")
        return payload

    def artifact_collect(
        self,
        target: str,
        path: str,
        *,
        max_bytes: int | None = None,
        timeout: float | None = None,
    ) -> Mapping[str, Any]:
        args = ["artifact-collect", target, path]
        if max_bytes is not None:
            args.append(str(max_bytes))
        payload = self._run_json(args, timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh artifact-collect returned non-object JSON")
        return payload

    def doctor(self, *, timeout: float | None = None) -> Mapping[str, Any]:
        payload = self._run_json(["doctor", "--json"], timeout=timeout)
        if not isinstance(payload, Mapping):
            raise VSSHError("vssh doctor returned non-object JSON")
        return payload

    def _run_json(self, args: Sequence[str], *, timeout: float | None = None) -> Any:
        proc = self._run(args, timeout=timeout)
        try:
            return json.loads(proc.stdout)
        except json.JSONDecodeError as exc:
            raise VSSHError(f"vssh returned invalid JSON: {exc}") from exc

    def _run(self, args: Sequence[str], *, timeout: float | None = None) -> subprocess.CompletedProcess[str]:
        proc = self.runner(
            [self.binary, *args],
            text=True,
            capture_output=True,
            env=self.env,
            timeout=self.timeout if timeout is None else timeout,
        )
        if proc.returncode != 0 and args and args[0] != "run":
            message = proc.stderr.strip() or proc.stdout.strip() or f"vssh exited with {proc.returncode}"
            raise VSSHError(message)
        return proc

    @staticmethod
    def _target_list(targets: Sequence[str] | str) -> str:
        if isinstance(targets, str):
            return targets
        return ",".join(targets)
