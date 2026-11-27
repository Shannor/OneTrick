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

func (m *memAggregateStore) listBySessionID(_ context.Context, sessionID string) ([]api.Aggregate, error) {
	out := make([]api.Aggregate, 0)
	for _, doc := range m.docs {
		if slices.Contains(doc.SessionIds, sessionID) {
			out = append(out, cloneAggregate(doc))
		}
	}
	return out, nil
}

func (m *memAggregateStore) detachSession(_ context.Context, aggregateID string, sessionID string, clearSessionCharIDs, removeCharIDs []string) error {
	doc, ok := m.docs[aggregateID]
	if !ok {
		return NotFound
	}
	doc.SessionIds = slices.DeleteFunc(slices.Clone(doc.SessionIds), func(id string) bool {
		return id == sessionID
	})
	if len(removeCharIDs) > 0 {
		doc.CharacterIds = slices.DeleteFunc(slices.Clone(doc.CharacterIds), func(cid string) bool {
			return slices.Contains(removeCharIDs, cid)
		})
		if doc.Performance != nil {
			perf := make(map[string]api.InstancePerformance, len(doc.Performance))
			for k, v := range doc.Performance {
				if slices.Contains(removeCharIDs, k) {
					continue
				}
				perf[k] = v
			}
			doc.Performance = perf
		}
	}
	if doc.SnapshotLinks != nil {
		links := make(map[string]api.SnapshotLink, len(doc.SnapshotLinks))
		for k, v := range doc.SnapshotLinks {
			if slices.Contains(removeCharIDs, k) {
				continue
			}
			if slices.Contains(clearSessionCharIDs, k) {
				v.SessionID = nil
			}
			links[k] = v
		}
		doc.SnapshotLinks = links
	}
	m.docs[aggregateID] = doc
	return nil
}

func (m *memAggregateStore) listAll(_ context.Context) ([]api.Aggregate, error) {
	out := make([]api.Aggregate, 0, len(m.docs))
	for _, doc := range m.docs {
		out = append(out, cloneAggregate(doc))
	}
	return out, nil
}

func (m *memAggregateStore) stampSnapshotLinkParticipants(_ context.Context, aggregateID string, participants map[string]SnapshotLinkParticipant) error {
	doc, ok := m.docs[aggregateID]
	if !ok {
		return NotFound
	}
	if doc.SnapshotLinks == nil {
		doc.SnapshotLinks = make(map[string]api.SnapshotLink)
	}
	for charID, meta := range participants {
		link, exists := doc.SnapshotLinks[charID]
		if !exists {
			continue
		}
		if meta.UserID != "" {
			link.UserID = new(meta.UserID)
		}
		if meta.DisplayName != "" {
			link.DisplayName = new(meta.DisplayName)
		}
		if meta.CharacterClass != "" {
			link.CharacterClass = new(meta.CharacterClass)
		}
		if meta.EmblemURL != "" {
			link.EmblemURL = new(meta.EmblemURL)
		}
		doc.SnapshotLinks[charID] = link
	}
	m.docs[aggregateID] = doc
	return nil
}

func (m *memAggregateStore) deleteAggregate(_ context.Context, aggregateID string) error {
	delete(m.docs, aggregateID)
	return nil
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

func TestRemoveSession(t *testing.T) {
	ctx := context.Background()

	t.Run("detaches session, deletes orphans, and returns deduplicated surviving siblings", func(t *testing.T) {
		store := newMemAggregateStore()
		store.docs["agg-shared-1"] = api.Aggregate{
			ID:          "agg-shared-1",
			ActivityID:  "act-1",
			SessionIds:  []string{"ses-a", "ses-b"},
			SnapshotIds: []string{"snap-a", "snap-b"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-a": {CharacterID: "char-a", SessionID: new("ses-a"), SnapshotID: new("snap-a")},
				"char-b": {CharacterID: "char-b", SessionID: new("ses-b"), SnapshotID: new("snap-b")},
			},
		}
		store.docs["agg-shared-2"] = api.Aggregate{
			ID:          "agg-shared-2",
			ActivityID:  "act-2",
			SessionIds:  []string{"ses-a", "ses-b", "ses-c"},
			SnapshotIds: []string{"snap-a", "snap-b", "snap-c"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-a": {CharacterID: "char-a", SessionID: new("ses-a"), SnapshotID: new("snap-a")},
				"char-b": {CharacterID: "char-b", SessionID: new("ses-b"), SnapshotID: new("snap-b")},
				"char-c": {CharacterID: "char-c", SessionID: new("ses-c"), SnapshotID: new("snap-c")},
			},
		}
		store.docs["agg-orphan"] = api.Aggregate{
			ID:         "agg-orphan",
			ActivityID: "act-3",
			SessionIds: []string{"ses-b"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-b": {CharacterID: "char-b", SessionID: new("ses-b")},
			},
		}
		store.docs["agg-snapshot-only"] = api.Aggregate{
			ID:          "agg-snapshot-only",
			ActivityID:  "act-4",
			SessionIds:  []string{"ses-b"},
			SnapshotIds: []string{"snap-b"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-b": {CharacterID: "char-b", SessionID: new("ses-b"), SnapshotID: new("snap-b")},
			},
		}

		svc := &service{store: store}
		siblings, err := svc.RemoveSession(ctx, "ses-b")
		if err != nil {
			t.Fatalf("RemoveSession failed: %v", err)
		}

		slices.Sort(siblings)
		if !slices.Equal(siblings, []string{"ses-a", "ses-c"}) {
			t.Fatalf("expected surviving siblings [ses-a ses-c], got %v", siblings)
		}

		if _, exists := store.docs["agg-orphan"]; exists {
			t.Fatal("expected orphaned aggregate to be deleted")
		}
		if _, exists := store.docs["agg-snapshot-only"]; !exists {
			t.Fatal("expected snapshot-backed aggregate to survive session deletion")
		}
		if store.docs["agg-snapshot-only"].SnapshotLinks["char-b"].SessionID != nil {
			t.Fatalf("expected char-b sessionId cleared on snapshot-only aggregate, got %v", store.docs["agg-snapshot-only"].SnapshotLinks["char-b"].SessionID)
		}

		shared1 := store.docs["agg-shared-1"]
		if !slices.Equal(shared1.SessionIds, []string{"ses-a"}) {
			t.Fatalf("expected agg-shared-1 SessionIds=[ses-a], got %v", shared1.SessionIds)
		}
		if shared1.SnapshotLinks["char-b"].SessionID != nil {
			t.Fatalf("expected char-b sessionId cleared on agg-shared-1, got %v", shared1.SnapshotLinks["char-b"].SessionID)
		}
		if shared1.SnapshotLinks["char-a"].SessionID == nil || *shared1.SnapshotLinks["char-a"].SessionID != "ses-a" {
			t.Fatalf("expected char-a sessionId preserved on agg-shared-1, got %+v", shared1.SnapshotLinks["char-a"])
		}
	})

	t.Run("removes unlinked session-only character from shared aggregate while preserving teammate and snapshot-backed characters", func(t *testing.T) {
		store := newMemAggregateStore()
		store.docs["agg-shared-unlinked"] = api.Aggregate{
			ID:           "agg-shared-unlinked",
			ActivityID:   "act-unlinked",
			CharacterIds: []string{"char-a", "char-b"},
			SessionIds:   []string{"ses-a", "ses-b"},
			SnapshotIds:  []string{"snap-a"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-a": {CharacterID: "char-a", SessionID: new("ses-a"), SnapshotID: new("snap-a")},
				"char-b": {CharacterID: "char-b", SessionID: new("ses-b"), SnapshotID: nil},
			},
			Performance: map[string]api.InstancePerformance{
				"char-a": {PlayerStats: api.PlayerStats{}},
				"char-b": {PlayerStats: api.PlayerStats{}},
			},
		}
		store.docs["agg-shared-snap"] = api.Aggregate{
			ID:           "agg-shared-snap",
			ActivityID:   "act-snap",
			CharacterIds: []string{"char-a", "char-b"},
			SessionIds:   []string{"ses-a", "ses-b"},
			SnapshotIds:  []string{"snap-a", "snap-b"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-a": {CharacterID: "char-a", SessionID: new("ses-a"), SnapshotID: new("snap-a")},
				"char-b": {CharacterID: "char-b", SessionID: new("ses-b"), SnapshotID: new("snap-b")},
			},
			Performance: map[string]api.InstancePerformance{
				"char-a": {PlayerStats: api.PlayerStats{}},
				"char-b": {PlayerStats: api.PlayerStats{}},
			},
		}

		svc := &service{store: store}
		siblings, err := svc.RemoveSession(ctx, "ses-b")
		if err != nil {
			t.Fatalf("RemoveSession failed: %v", err)
		}
		if !slices.Equal(siblings, []string{"ses-a"}) {
			t.Fatalf("expected surviving sibling [ses-a], got %v", siblings)
		}

		unlinked := store.docs["agg-shared-unlinked"]
		if !slices.Equal(unlinked.CharacterIds, []string{"char-a"}) {
			t.Fatalf("expected char-b removed from CharacterIds, got %v", unlinked.CharacterIds)
		}
		if _, exists := unlinked.SnapshotLinks["char-b"]; exists {
			t.Fatalf("expected char-b removed from SnapshotLinks, got %+v", unlinked.SnapshotLinks)
		}
		if _, exists := unlinked.Performance["char-b"]; exists {
			t.Fatalf("expected char-b removed from Performance, got %+v", unlinked.Performance)
		}
		if _, exists := unlinked.SnapshotLinks["char-a"]; !exists {
			t.Fatalf("expected teammate char-a preserved in SnapshotLinks")
		}
		if _, exists := unlinked.Performance["char-a"]; !exists {
			t.Fatalf("expected teammate char-a preserved in Performance")
		}

		snapped := store.docs["agg-shared-snap"]
		if !slices.Equal(snapped.CharacterIds, []string{"char-a", "char-b"}) {
			t.Fatalf("expected snapshot-backed char-b preserved in CharacterIds, got %v", snapped.CharacterIds)
		}
		linkB, exists := snapped.SnapshotLinks["char-b"]
		if !exists || linkB.SessionID != nil || linkB.SnapshotID == nil || *linkB.SnapshotID != "snap-b" {
			t.Fatalf("expected snapshot-backed char-b link preserved with nil SessionID, got exists=%v link=%+v", exists, linkB)
		}
		if _, exists := snapped.Performance["char-b"]; !exists {
			t.Fatalf("expected snapshot-backed char-b preserved in Performance")
		}
	})

	t.Run("empty sessionID is a no-op", func(t *testing.T) {
		svc := &service{store: newMemAggregateStore()}
		siblings, err := svc.RemoveSession(ctx, "")
		if err != nil || len(siblings) != 0 {
			t.Fatalf("expected nil siblings and nil err, got siblings=%v err=%v", siblings, err)
		}
	})
}

func TestStampSnapshotLinkParticipantsAndGetAllAggregates(t *testing.T) {
	ctx := context.Background()
	store := newMemAggregateStore()
	store.docs["agg-1"] = api.Aggregate{
		ID:         "agg-1",
		ActivityID: "act-1",
		SnapshotLinks: map[string]api.SnapshotLink{
			"char-a": {
				CharacterID: "char-a",
				SnapshotID:  new("snap-a"),
				SessionID:   new("ses-a"),
			},
		},
	}
	svc := &service{store: store}

	all, err := svc.GetAllAggregates(ctx)
	if err != nil {
		t.Fatalf("GetAllAggregates failed: %v", err)
	}
	if len(all) != 1 || all[0].ID != "agg-1" {
		t.Fatalf("unexpected GetAllAggregates result: %+v", all)
	}

	if err := svc.StampSnapshotLinkParticipants(ctx, "agg-1", map[string]SnapshotLinkParticipant{
		"char-a": {
			UserID:         "user-a",
			DisplayName:    "Alpha#1111",
			CharacterClass: "Hunter",
			EmblemURL:      "https://www.bungie.net/emblem.jpg",
		},
	}); err != nil {
		t.Fatalf("StampSnapshotLinkParticipants failed: %v", err)
	}

	updated := store.docs["agg-1"].SnapshotLinks["char-a"]
	if updated.SnapshotID == nil || *updated.SnapshotID != "snap-a" {
		t.Fatalf("expected existing SnapshotID preserved, got %+v", updated)
	}
	if updated.UserID == nil || *updated.UserID != "user-a" ||
		updated.DisplayName == nil || *updated.DisplayName != "Alpha#1111" ||
		updated.CharacterClass == nil || *updated.CharacterClass != "Hunter" ||
		updated.EmblemURL == nil || *updated.EmblemURL != "https://www.bungie.net/emblem.jpg" {
		t.Fatalf("unexpected stamped participant fields: %+v", updated)
	}

	if err := svc.StampSnapshotLinkParticipants(ctx, "", map[string]SnapshotLinkParticipant{"char-a": {UserID: "u"}}); err != nil {
		t.Fatalf("expected empty aggregateID to be a no-op, got %v", err)
	}
	if err := svc.StampSnapshotLinkParticipants(ctx, "agg-1", nil); err != nil {
		t.Fatalf("expected empty participants to be a no-op, got %v", err)
	}
}
