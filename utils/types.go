package utils

import (
	"cloud.google.com/go/firestore"
	"fmt"
)

func ToPointer[T any](value T) *T {
	return &value
}

func GetAllToStructs[T any](docs []*firestore.DocumentSnapshot) ([]T, error) {
	result := make([]T, len(docs))
	for i, doc := range docs {
		var item T
		if err := doc.DataTo(&item); err != nil {
			return nil, fmt.Errorf("failed to convert doc %s: %w", doc.Ref.ID, err)
		}
		result[i] = item
	}
	return result, nil
}
