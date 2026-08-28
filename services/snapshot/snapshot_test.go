package snapshot

import (
	"errors"
	"testing"
)

func TestSnapshotServiceErrors(t *testing.T) {
	if !errors.Is(NotFound, NotFound) {
		t.Errorf("expected NotFound error to match")
	}
	if !errors.Is(Unauthorized, Unauthorized) {
		t.Errorf("expected Unauthorized error to match")
	}
}
