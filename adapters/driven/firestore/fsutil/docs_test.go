package fsutil

import (
	"testing"

	"cloud.google.com/go/firestore"
)

func TestGetAllToStructsEmptyAndNilInputs(t *testing.T) {
	type sampleDoc struct {
		ID string `firestore:"id"`
	}

	gotNil, err := GetAllToStructs[sampleDoc](nil)
	if err != nil {
		t.Fatalf("unexpected error for nil slice: %v", err)
	}
	if len(gotNil) != 0 {
		t.Fatalf("expected 0 items for nil slice, got %d", len(gotNil))
	}

	gotWithNilElem, err := GetAllToStructs[sampleDoc]([]*firestore.DocumentSnapshot{nil})
	if err != nil {
		t.Fatalf("unexpected error for nil snapshot element: %v", err)
	}
	if len(gotWithNilElem) != 0 {
		t.Fatalf("expected 0 items for nil snapshot element, got %d", len(gotWithNilElem))
	}
}
