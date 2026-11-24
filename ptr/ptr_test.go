package ptr

import "testing"

func TestNonEmpty(t *testing.T) {
	if got := NonEmpty(""); got != nil {
		t.Fatalf("expected nil for empty string, got %q", *got)
	}
	got := NonEmpty("hello")
	if got == nil || *got != "hello" {
		t.Fatalf("expected pointer to hello, got %v", got)
	}
}

func TestDeref(t *testing.T) {
	if got := Deref((*string)(nil)); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
	if got := Deref((*int)(nil)); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	if got := Deref((*float64)(nil)); got != 0 {
		t.Fatalf("expected 0, got %f", got)
	}
	if got := Deref(new("abc")); got != "abc" {
		t.Fatalf("expected abc, got %q", got)
	}
	if got := Deref(new(7)); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
	if got := Deref(new(true)); !got {
		t.Fatalf("expected true, got %v", got)
	}
}

func TestDerefOr(t *testing.T) {
	if got := DerefOr((*int)(nil), 10); got != 10 {
		t.Fatalf("expected fallback 10, got %d", got)
	}
	if got := DerefOr(new(3), 10); got != 3 {
		t.Fatalf("expected 3, got %d", got)
	}
}
