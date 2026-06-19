package fleet

import "testing"

func hitNames(hits []AskHit) map[string]bool {
	m := map[string]bool{}
	for _, h := range hits {
		m[h.Node] = true
	}
	return m
}

func TestAskDiskProblem(t *testing.T) {
	fm := seedThree(t)
	hits := fm.Ask("디스크 문제 있는 노드")
	got := hitNames(hits)
	if !got["d1"] {
		t.Fatalf("expected d1 for disk query, got %v", got)
	}
	if got["g1"] || got["v1"] {
		t.Fatalf("unexpected nodes matched: %v", got)
	}
}

func TestAskGPUList(t *testing.T) {
	fm := seedThree(t)
	got := hitNames(fm.Ask("GPU 서버 목록"))
	if !got["d1"] || !got["g1"] {
		t.Fatalf("expected d1 and g1 for GPU query, got %v", got)
	}
}

func TestAskFaultHistory(t *testing.T) {
	fm := seedThree(t)
	got := hitNames(fm.Ask("장애 이력 있는 노드"))
	if !got["d1"] {
		t.Fatalf("expected d1 for fault query, got %v", got)
	}
}

func TestAskPostgres(t *testing.T) {
	fm := seedThree(t)
	got := hitNames(fm.Ask("postgres 돌리는 서버"))
	if !got["d1"] || len(got) != 1 {
		t.Fatalf("expected only d1 for postgres query, got %v", got)
	}
}

func TestAskReasonsPopulated(t *testing.T) {
	fm := seedThree(t)
	hits := fm.Ask("postgres 돌리는 서버")
	if len(hits) == 0 || len(hits[0].Reasons) == 0 {
		t.Fatalf("expected reasons in ask result: %+v", hits)
	}
}

func TestAskNoMatch(t *testing.T) {
	fm := seedThree(t)
	if hits := fm.Ask("쿠버네티스 클러스터"); len(hits) != 0 {
		t.Fatalf("expected no matches, got %v", hitNames(hits))
	}
}
