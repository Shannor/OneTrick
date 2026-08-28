package aggregate

import "testing"

func TestIsAggregateOrphaned(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]any
		expected bool
	}{
		{
			name: "Fully populated aggregate is not orphaned",
			data: map[string]any{
				"sessionIds":  []any{"session1"},
				"snapshotIds": []any{"snap1"},
				"snapshotLinks": map[string]any{
					"char1": map[string]any{
						"snapshotId": "snap1",
						"sessionId":  "session1",
					},
				},
			},
			expected: false,
		},
		{
			name: "Aggregate with remaining session is not orphaned",
			data: map[string]any{
				"sessionIds":    []any{"session2"},
				"snapshotIds":   []any{},
				"snapshotLinks": map[string]any{},
			},
			expected: false,
		},
		{
			name: "Aggregate with remaining snapshotId in links is not orphaned",
			data: map[string]any{
				"sessionIds":  []any{},
				"snapshotIds": []any{},
				"snapshotLinks": map[string]any{
					"char1": map[string]any{
						"snapshotId": "snap2",
					},
				},
			},
			expected: false,
		},
		{
			name: "Empty arrays and cleared snapshotLinks is orphaned",
			data: map[string]any{
				"sessionIds":  []any{},
				"snapshotIds": []any{},
				"snapshotLinks": map[string]any{
					"char1": map[string]any{
						"confidenceLevel": "medium",
					},
				},
			},
			expected: true,
		},
		{
			name: "Empty data map is orphaned",
			data: map[string]any{
				"sessionIds":    []any{},
				"snapshotIds":   []any{},
				"snapshotLinks": map[string]any{},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isAggregateOrphaned(tt.data)
			if result != tt.expected {
				t.Errorf("expected isAggregateOrphaned to be %v, got %v", tt.expected, result)
			}
		})
	}
}
