package main

import (
	"os"
	"testing"
)

func TestNormalizedMaxParallelism(t *testing.T) {
	cases := []struct {
		name  string
		max   int
		total int
		want  int
	}{
		{name: "zero total", max: 16, total: 0, want: 1},
		{name: "unbounded", max: 0, total: 4, want: 4},
		{name: "negative unbounded", max: -1, total: 4, want: 4},
		{name: "clamped to total", max: 99, total: 4, want: 4},
		{name: "bounded", max: 2, total: 4, want: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizedMaxParallelism(tc.max, tc.total)
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDefaultMaxParallelismFromEnv(t *testing.T) {
	t.Setenv("VSSH_MAX_PARALLELISM", "3")
	if got := defaultMaxParallelism(); got != 3 {
		t.Fatalf("got %d", got)
	}

	t.Setenv("VSSH_MAX_PARALLELISM", "not-a-number")
	if got := defaultMaxParallelism(); got != 16 {
		t.Fatalf("got %d", got)
	}

	os.Unsetenv("VSSH_MAX_PARALLELISM")
	if got := defaultMaxParallelism(); got != 16 {
		t.Fatalf("got %d", got)
	}
}
