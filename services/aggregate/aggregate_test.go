package aggregate

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/utils"
	"slices"
	"testing"
)

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

type memAggregateStore struct {
	docs    map[string]api.Aggregate
	nextSeq int
}

func newMemAggregateStore() *memAggregateStore {
	return &memAggregateStore{docs: make(map[string]api.Aggregate)}
}

func cloneAggregate(a api.Aggregate) api.Aggregate {
	cp := a
	cp.SessionIds = slices.Clone(a.SessionIds)
	cp.SnapshotIds = slices.Clone(a.SnapshotIds)
	cp.CharacterIds = slices.Clone(a.CharacterIds)
	if a.SnapshotLinks != nil {
		cp.SnapshotLinks = make(map[string]api.SnapshotLink, len(a.SnapshotLinks))
		for k, v := range a.SnapshotLinks {
			cp.SnapshotLinks[k] = v
		}
	}
	if a.Performance != nil {
		cp.Performance = make(map[string]api.InstancePerformance, len(a.Performance))
		for k, v := range a.Performance {
			cp.Performance[k] = v
		}
	}
	return cp
}

func (m *memAggregateStore) getByActivityID(_ context.Context, activityID string) (*api.Aggregate, error) {
	for _, doc := range m.docs {
		if doc.ActivityID == activityID {
			cp := cloneAggregate(doc)
			return &cp, nil
		}
	}
	return nil, NotFound
}

func (m *memAggregateStore) mergeSessionActivity(
	_ context.Context,
	aggregateID string,
	characterID string,
	snapshotLink api.SnapshotLink,
	performance api.InstancePerformance,
	sessionIDs, snapshotIDs, characterIDs []string,
	estimatedGameMode *api.GameMode,
) error {
	doc, ok := m.docs[aggregateID]
	if !ok {
		return NotFound
	}
	if doc.SnapshotLinks == nil {
		doc.SnapshotLinks = make(map[string]api.SnapshotLink)
	}
	if doc.Performance == nil {
		doc.Performance = make(map[string]api.InstancePerformance)
	}
	doc.SnapshotLinks[characterID] = snapshotLink
	doc.Performance[characterID] = performance
	doc.SessionIds = utils.UnionNonZero(doc.SessionIds, sessionIDs)
	doc.SnapshotIds = utils.UnionNonZero(doc.SnapshotIds, snapshotIDs)
	doc.CharacterIds = utils.UnionNonZero(doc.CharacterIds, characterIDs)
	if estimatedGameMode != nil {
		doc.ActivityDetails.EstimatedGameMode = estimatedGameMode
	}
	m.docs[aggregateID] = doc
	return nil
}

func (m *memAggregateStore) createAggregate(_ context.Context, agg api.Aggregate) (api.Aggregate, error) {
	m.nextSeq++
	agg.ID = fmt.Sprintf("agg-%d", m.nextSeq)
	m.docs[agg.ID] = cloneAggregate(agg)
	return agg, nil
}

func TestUpsertSessionActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("creates new aggregate when none exists for activityId", func(t *testing.T) {
		store := newMemAggregateStore()
		svc := &service{store: store}
		sessID := "sess-1"
		snapID := "snap-1"
		mode := api.GameModeCompetitive

		agg, err := svc.UpsertSessionActivity(
			ctx,
			"char-1",
			api.ActivityHistory{InstanceID: "act-100", EstimatedGameMode: &mode},
			api.SnapshotLink{CharacterID: "char-1", SessionID: &sessID, SnapshotID: &snapID, ConfidenceLevel: api.HighConfidenceLevel},
			api.InstancePerformance{},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if agg.ID != "agg-1" || agg.ActivityID != "act-100" {
			t.Fatalf("unexpected aggregate identity: %+v", agg)
		}
		if !slices.Equal(agg.SessionIds, []string{"sess-1"}) || !slices.Equal(agg.SnapshotIds, []string{"snap-1"}) || !slices.Equal(agg.CharacterIds, []string{"char-1"}) {
			t.Fatalf("unexpected membership arrays: sessions=%v snapshots=%v chars=%v", agg.SessionIds, agg.SnapshotIds, agg.CharacterIds)
		}
		if agg.ActivityDetails.EstimatedGameMode == nil || *agg.ActivityDetails.EstimatedGameMode != api.GameModeCompetitive {
			t.Fatalf("expected competitive estimatedGameMode, got %+v", agg.ActivityDetails.EstimatedGameMode)
		}
	})

	t.Run("merges second character into existing aggregate and deduplicates IDs", func(t *testing.T) {
		store := newMemAggregateStore()
		svc := &service{store: store}
		sess1 := "sess-1"
		snap1 := "snap-1"
		sess2 := "sess-2"
		snap2 := "snap-2"

		first, err := svc.UpsertSessionActivity(
			ctx,
			"char-1",
			api.ActivityHistory{InstanceID: "act-200"},
			api.SnapshotLink{CharacterID: "char-1", SessionID: &sess1, SnapshotID: &snap1},
			api.InstancePerformance{},
		)
		if err != nil {
			t.Fatalf("first upsert failed: %v", err)
		}

		// Merge second character with estimatedGameMode backfill
		qp := api.GameModeQuickPlay
		merged, err := svc.UpsertSessionActivity(
			ctx,
			"char-2",
			api.ActivityHistory{InstanceID: "act-200", EstimatedGameMode: &qp},
			api.SnapshotLink{CharacterID: "char-2", SessionID: &sess2, SnapshotID: &snap2},
			api.InstancePerformance{},
		)
		if err != nil {
			t.Fatalf("second upsert failed: %v", err)
		}
		if merged.ID != first.ID {
			t.Fatalf("expected same aggregate ID %q, got %q", first.ID, merged.ID)
		}
		if len(merged.SnapshotLinks) != 2 || len(merged.Performance) != 2 {
			t.Fatalf("expected 2 characters merged, got links=%d perf=%d", len(merged.SnapshotLinks), len(merged.Performance))
		}
		if !slices.Equal(merged.SessionIds, []string{"sess-1", "sess-2"}) ||
			!slices.Equal(merged.SnapshotIds, []string{"snap-1", "snap-2"}) ||
			!slices.Equal(merged.CharacterIds, []string{"char-1", "char-2"}) {
			t.Fatalf("unexpected merged arrays: sessions=%v snapshots=%v chars=%v", merged.SessionIds, merged.SnapshotIds, merged.CharacterIds)
		}
		if merged.ActivityDetails.EstimatedGameMode == nil || *merged.ActivityDetails.EstimatedGameMode != api.GameModeQuickPlay {
			t.Fatalf("expected estimatedGameMode backfilled to quickplay, got %+v", merged.ActivityDetails.EstimatedGameMode)
		}

		// Re-upserting with a different mode does not overwrite existing estimatedGameMode or duplicate IDs
		trials := api.GameModeTrials
		again, err := svc.UpsertSessionActivity(
			ctx,
			"char-1",
			api.ActivityHistory{InstanceID: "act-200", EstimatedGameMode: &trials},
			api.SnapshotLink{CharacterID: "char-1", SessionID: &sess1, SnapshotID: &snap1},
			api.InstancePerformance{},
		)
		if err != nil {
			t.Fatalf("third upsert failed: %v", err)
		}
		if *again.ActivityDetails.EstimatedGameMode != api.GameModeQuickPlay {
			t.Fatalf("expected existing estimatedGameMode preserved as quickplay, got %v", *again.ActivityDetails.EstimatedGameMode)
		}
		if len(again.SessionIds) != 2 || len(again.SnapshotIds) != 2 || len(again.CharacterIds) != 2 {
			t.Fatalf("expected deduplicated arrays of len 2, got sessions=%v snapshots=%v chars=%v", again.SessionIds, again.SnapshotIds, again.CharacterIds)
		}
	})
}
