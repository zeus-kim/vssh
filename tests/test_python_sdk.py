import json
import subprocess
import unittest

from vssh import ExecResult, VSSH, VSSHError


class FakeRunner:
    def __init__(self, response):
        self.response = response
        self.calls = []

    def __call__(self, args, **kwargs):
        self.calls.append((args, kwargs))
        return self.response


def completed(stdout="", stderr="", returncode=0):
    return subprocess.CompletedProcess(["vssh"], returncode, stdout, stderr)


class VSSHSDKTest(unittest.TestCase):
    def test_exec_wraps_stdout_stderr_and_exit_code(self):
        runner = FakeRunner(completed(stdout="ok\n", stderr="", returncode=0))
        client = VSSH(binary="/bin/vssh", secret="s", runner=runner)

        result = client.exec("d1", "printf ok")

        self.assertIsInstance(result, ExecResult)
        self.assertTrue(result.success)
        self.assertEqual(result.stdout, "ok\n")
        self.assertEqual(runner.calls[0][0], ["/bin/vssh", "run", "d1", "printf ok"])
        self.assertEqual(runner.calls[0][1]["env"]["VSSH_SECRET"], "s")

    def test_exec_many_parses_structured_results(self):
        payload = [
            {
                "target": "d1",
                "result": {
                    "success": True,
                    "command": "uptime",
                    "stdout": "up",
                    "stderr": "",
                    "exit_code": 0,
                    "duration_ms": 5,
                },
            }
        ]
        runner = FakeRunner(completed(stdout=json.dumps(payload)))
        client = VSSH(runner=runner)

        results = client.exec_many(["d1", "v3"], "uptime")

        self.assertEqual(results[0].target, "d1")
        self.assertEqual(results[0].result.stdout, "up")
        self.assertEqual(runner.calls[0][0], ["vssh", "run-many", "d1,v3", "uptime"])

    def test_rpc_serializes_params(self):
        runner = FakeRunner(completed(stdout='{"ok":true,"result":{"x":1}}'))
        client = VSSH(runner=runner)

        result = client.rpc("d1", "service_status", {"name": "nginx"})

        self.assertTrue(result["ok"])
        self.assertEqual(
            runner.calls[0][0],
            ["vssh", "rpc", "d1", "service_status", '{"name":"nginx"}'],
        )

    def test_non_json_raises(self):
        runner = FakeRunner(completed(stdout="not json"))
        client = VSSH(runner=runner)

        with self.assertRaises(VSSHError):
            client.facts("d1")

    def test_facts_many_parses_results(self):
        payload = [{"target": "d1", "result": {"hostname": "d1", "memory": {"used_mb": 1}}}]
        runner = FakeRunner(completed(stdout=json.dumps(payload)))
        client = VSSH(runner=runner)

        results = client.facts_many(["d1", "v3"])

        self.assertEqual(results[0].target, "d1")
        self.assertEqual(results[0].result["hostname"], "d1")
        self.assertEqual(runner.calls[0][0], ["vssh", "facts-many", "d1,v3"])

    def test_job_helpers_call_cli(self):
        runner = FakeRunner(completed(stdout='{"success":true,"data":{"id":"j1"}}'))
        client = VSSH(runner=runner)

        client.job_start("d1", "sleep 1")
        client.job_status("d1", "j1")
        client.job_logs("d1", "j1", tail_bytes=100)
        client.job_cancel("d1", "j1")

        self.assertEqual(runner.calls[0][0], ["vssh", "job-start", "d1", "sleep 1"])
        self.assertEqual(runner.calls[1][0], ["vssh", "job-status", "d1", "j1"])
        self.assertEqual(runner.calls[2][0], ["vssh", "job-logs", "d1", "j1", "100"])
        self.assertEqual(runner.calls[3][0], ["vssh", "job-cancel", "d1", "j1"])

    def test_artifact_collect_calls_cli(self):
        runner = FakeRunner(completed(stdout='{"success":true,"data":{"path":"/tmp/x"}}'))
        client = VSSH(runner=runner)

        result = client.artifact_collect("d1", "/tmp/x", max_bytes=128)

        self.assertTrue(result["success"])
        self.assertEqual(runner.calls[0][0], ["vssh", "artifact-collect", "d1", "/tmp/x", "128"])

    def test_doctor_calls_cli(self):
        runner = FakeRunner(completed(stdout='{"kind":"vssh_doctor","status":"ok"}'))
        client = VSSH(runner=runner)

        result = client.doctor()

        self.assertEqual(result["kind"], "vssh_doctor")
        self.assertEqual(runner.calls[0][0], ["vssh", "doctor", "--json"])


if __name__ == "__main__":
    unittest.main()
