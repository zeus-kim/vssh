# Python SDK

Python SDK는 vssh daemon을 다시 구현하지 않습니다.

역할 분리는 다음과 같습니다.

- Go daemon/CLI: 실제 원격 실행, RPC, 파일 전송, job, evidence의 source of truth
- Python SDK: Codex, Claude, MCP 서버, notebook, 자동화 script가 쓰는 AI-facing client

즉 `pip install vssh`는 서버에 Python runtime을 강제하는 전략이 아니라, AI와
운영 자동화가 vssh를 쉽게 호출하게 만드는 얇은 SDK입니다.

## 설치

개발 중에는 repo에서 editable 설치합니다.

```bash
python -m pip install -e .
```

실제 배포에서는 Go binary는 GitHub Release/install.sh로 설치하고, Python SDK는
PyPI wheel로 배포합니다.

```bash
curl -fsSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash
python -m pip install vssh
```

## 사용 예

```python
from vssh import VSSH

client = VSSH()   # 키 전용 인증 (노드별 Ed25519 VAUTH1); 설정할 시크릿 없음

result = client.exec("d1", "df -h /")
print(result.stdout)

facts = client.facts("d1")
print(facts["hostname"], facts["memory"])

results = client.exec_many(["d1", "v3"], "uptime")
for item in results:
    print(item.target, item.result.stdout if item.result else item.error)

disk = client.rpc("d1", "get_disk")
print(disk)

job = client.job_start("d1", "sleep 30 && echo done")
job_id = job["data"]["id"]
print(client.job_status("d1", job_id))
print(client.job_logs("d1", job_id))
```

## 제품 판단

Python으로 daemon을 바꾸면 배포는 쉬워 보이지만, 실제 서버 운영 도구로서는
다음 문제가 생깁니다.

- 타깃 서버마다 Python 버전과 패키지 상태가 다릅니다.
- root/systemd/service 실행에서 venv 관리가 새로운 운영 부담이 됩니다.
- 단일 바이너리 배포, cross compile, system daemon 운영은 Go가 더 단순합니다.

따라서 핵심 daemon은 Go로 유지하고, Python은 SDK/MCP/client layer로 두는 것이
맞습니다. 이 구조면 AI agent는 Python 생태계에서 vssh를 쉽게 쓰고, 서버 쪽은
작은 정적 binary 하나로 유지됩니다.
