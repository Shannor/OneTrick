package utils

import (
	"slices"
	"testing"
)

func TestUniqueNonZero(t *testing.T) {
	if got := UniqueNonZero([]int64{0, 10, 20, 10, 0, 30}); !slices.Equal(got, []int64{10, 20, 30}) {
		t.Fatalf("unexpected unique int64s: %v", got)
	}
	if got := UniqueNonZero([]int64{0, 0}); len(got) != 0 {
		t.Fatalf("expected empty result for all-zero int64s, got %v", got)
	}
	if got := UniqueNonZero([]string{"", "a", "b", "a", ""}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("unexpected unique strings: %v", got)
	}
	if got := UniqueNonZero([]int64(nil)); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

func TestChunk(t *testing.T) {
	ids := make([]int64, 65)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	chunks := Chunk(ids, 30)
	if len(chunks) != 3 || len(chunks[0]) != 30 || len(chunks[1]) != 30 || len(chunks[2]) != 5 {
		t.Fatalf("expected chunks of [30, 30, 5], got lens %d", len(chunks))
	}
	if got := Chunk(ids, 0); got != nil {
		t.Fatalf("expected nil for non-positive chunk size, got %v", got)
	}
	if got := Chunk([]int64(nil), 10); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
}

func TestUnionNonZero(t *testing.T) {
	base := []string{"a", "b"}
	got := UnionNonZero(base, []string{"", "b", "c", "c", "d"})
	if !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("expected [a b c d], got %v", got)
	}
	if !slices.Equal(base, []string{"a", "b"}) {
		t.Fatalf("expected base slice not to be mutated, got %v", base)
	}
}
