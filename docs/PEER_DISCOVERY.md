# Peer Discovery

vssh는 여러 소스에서 노드 목록을 가져옵니다.

## Tailscale (권장)

[Tailscale](https://tailscale.com)이 설치되어 있으면 자동으로 peer 목록을 가져옵니다.

**장점:**
- 설정 불필요 - Tailscale 네트워크의 모든 노드 자동 발견
- 실시간 온라인 상태
- NAT traversal 자동 처리

## 수동 설정

Tailscale 없이 사용하려면 `~/.vssh/servers.json`에 노드를 직접 정의:

```json
{
  "my-server": {
    "ip": "192.168.1.100",
    "port": 48291,
    "tags": ["linux", "docker"]
  },
  "another-server": {
    "ip": "192.168.1.101",
    "port": 48291
  }
}
```

---

## 빠른 시작

### Tailscale 사용 (권장)

```bash
# 1. 각 노드에 Tailscale 설치 후 로그인
curl -fsSL https://tailscale.com/install.sh | sh
tailscale up

# 2. 각 노드에 vssh 설치
curl -sSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash

# 3. 각 노드에서 vssh 서버 시작
vssh server &

# 4. 상태 확인
vssh status
```

### 수동 설정 (Tailscale 없이)

```bash
# 1. vssh 설치
curl -sSL https://raw.githubusercontent.com/zeus-kim/vssh/main/install.sh | bash

# 2. 서버 목록 설정
cat > ~/.vssh/servers.json << 'EOF'
{
  "server1": {"ip": "192.168.1.100", "port": 48291},
  "server2": {"ip": "192.168.1.101", "port": 48291}
}
EOF

# 3. 각 노드에서 vssh 서버 시작
vssh server &

# 4. 상태 확인
vssh status
```

---

## 노드 정보 조회

### 기본 상태

```bash
vssh status
```

### 상세 시스템 정보

```bash
vssh facts <node>
```

hostname, OS, CPU, 메모리, load, 디스크 사용량 등을 반환합니다.

---

## 문제 해결

### 노드가 offline으로 표시됨

1. 대상 노드에서 `vssh server`가 실행 중인지 확인
2. 네트워크 연결 확인: `ping <node-ip>`
3. 포트 확인: `nc -zv <node-ip> 48291`

### Tailscale 노드가 표시되지 않음

```bash
# Tailscale 상태 확인
tailscale status

# vssh가 Tailscale을 찾는지 확인
which tailscale
```
