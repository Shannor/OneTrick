package set

import (
	"slices"
	"testing"
)

func TestOrderedSet(t *testing.T) {
	s := NewOrdered[string]()
	if s.Size() != 0 {
		t.Fatalf("expected empty set, got size %d", s.Size())
	}
	if got := s.ToSlice(); got == nil || len(got) != 0 {
		t.Fatalf("expected non-nil empty slice, got %#v", got)
	}

	if !s.Add("control") {
		t.Fatal("expected first Add to return true")
	}
	if !s.Add("clash") {
		t.Fatal("expected second Add to return true")
	}
	if s.Add("control") {
		t.Fatal("expected duplicate Add to return false")
	}

	if !s.Contains("control") || !s.Contains("clash") || s.Contains("trials") {
		t.Fatal("unexpected Contains results")
	}
	if s.Size() != 2 {
		t.Fatalf("expected size 2, got %d", s.Size())
	}

	got := s.ToSlice()
	if !slices.Equal(got, []string{"control", "clash"}) {
		t.Fatalf("expected [control clash], got %v", got)
	}

	got[0] = "mutated"
	if !slices.Equal(s.ToSlice(), []string{"control", "clash"}) {
		t.Fatalf("expected ToSlice to return defensive copy, got %v", s.ToSlice())
	}
}

func TestOrderedFromSlice(t *testing.T) {
	s := OrderedFromSlice([]int64{30, 10, 30, 20, 10})
	if !slices.Equal(s.ToSlice(), []int64{30, 10, 20}) {
		t.Fatalf("expected [30 10 20], got %v", s.ToSlice())
	}
}
