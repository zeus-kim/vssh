# 다음 세션 TODO

> **작업 폴더:** `/Users/dragon/Projects/vssh-repo`
> **GitHub:** https://github.com/zeus-kim/vssh

## 폴더 구조

| 경로 | 상태 |
|------|------|
| `Projects/vssh-repo` | ✅ 메인 작업 폴더 |
| `meshpop-repos/vssh` | 개발 소스 (그대로 유지) |
| `meshpop-repos/vssh-archive-20260628.bundle` | 백업 |
| `Projects/vssh` | ❌ 삭제됨 (Rust 버전, 안 씀) |

## 완료된 작업 (2026-06-28)

- [x] README 히어로: "AI asks. vssh answers."
- [x] README 구조 재정렬: Why → Features → Install
- [x] Install 보안 철학: download → review → execute
- [x] demo.gif 생성 (asciinema 스크립트)
- [x] README 40% 축소 (334줄 → 197줄)
- [x] 중복 Install 섹션 제거

## 다음에 할 것

### 1. YouTube 영상 업로드 (우선순위 높음)

**영상 위치:** `/Users/dragon/Projects/screen/app-explainer/out/vssh-demo.mp4` (5MB, 소리 있음)

**계정:** zeus@zeus.kim

**업로드 방법:**
1. https://studio.youtube.com (zeus@zeus.kim)
2. 영상 업로드
3. 제목: `vssh — AI Execution Runtime Demo`
4. 공개 설정

### 2. README에 YouTube 링크 추가

```markdown
[![Demo](https://img.youtube.com/vi/VIDEO_ID/0.jpg)](https://www.youtube.com/watch?v=VIDEO_ID)
```

### 3. 실제 스크린샷 추가 (선택)

Claude + vssh 대화 스크린샷 5장:
1. "Show me my fleet"
2. "What services are running?"
3. "Check GPU status"
4. "Find all services on all servers"
5. "Suggest reallocation"

## 데모 녹화 팁 (참고용)

**황금 플로우:**
```
hello → show fleet → services → GPU status → all services parallel → suggest reallocation
```

**녹화 주의:**
- 녹화 안 할 때가 더 자연스러움
- 미리 질문 연습하고 녹화
- IP 주소 노출 주의

## 핵심 인사이트

> 기능 부족 ❌ 전달력 부족 ✅

README 다듬는 것보다 실제 사용 예시 보여주는 게 더 효과적.
