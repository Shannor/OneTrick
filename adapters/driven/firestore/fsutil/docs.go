package fsutil

import (
	"fmt"

	"cloud.google.com/go/firestore"
)

// GetAllToStructs unmarshals existing Firestore document snapshots into a slice of T.
// Placed in a leaf subpackage so both legacy services/* and permanent adapters/driven/firestore
// stores can share it without creating a StatsStore <-> services/stats import cycle.
func GetAllToStructs[T any](docs []*firestore.DocumentSnapshot) ([]T, error) {
	result := make([]T, 0, len(docs))
	for _, doc := range docs {
		if doc == nil || !doc.Exists() {
			continue
		}
		var item T
		if err := doc.DataTo(&item); err != nil {
			return nil, fmt.Errorf("failed to convert doc %s: %w", doc.Ref.ID, err)
		}
		result = append(result, item)
	}
	return result, nil
}
