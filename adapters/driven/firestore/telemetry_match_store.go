// Package firestore provides infrastructure-organized DRIVEN persistence adapters
// implementing outbound store ports for core domain packages.
package firestore

import (
	"context"
	"errors"
	"fmt"
	coretelemetry "oneTrick/core/telemetry"
	"time"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/iterator"
)

const aggregatesCollection = "aggregates"

type projectedAggregateDoc struct {
	ActivityID      string                      `firestore:"activityId"`
	ActivityHistory projectedActivityHistoryDoc `firestore:"activityHistory"`
	Performance     map[string]projectedPerfDoc `firestore:"performance"`
	SnapshotLinks   map[string]projectedLinkDoc `firestore:"snapshotLinks"`
}

type projectedActivityHistoryDoc struct {
	Period            time.Time `firestore:"period"`
	Location          string    `firestore:"location"`
	Activity          string    `firestore:"activity"`
	Mode              string    `firestore:"mode"`
	EstimatedGameMode string    `firestore:"estimatedGameMode"`
	InstanceID        string    `firestore:"instanceId"`
}

type projectedPerfDoc struct {
	PlayerStats *projectedPlayerStatsDoc `firestore:"playerStats"`
}

type projectedPlayerStatsDoc struct {
	Kills      projectedStatValueDoc         `firestore:"kills"`
	Deaths     projectedStatValueDoc         `firestore:"deaths"`
	Assists    projectedStatValueDoc         `firestore:"assists"`
	Standing   projectedOptionalStatValueDoc `firestore:"standing"`
	TimePlayed projectedOptionalStatValueDoc `firestore:"timePlayed"`
}

type projectedStatValueDoc struct {
	Value float64 `firestore:"value"`
}

type projectedOptionalStatValueDoc struct {
	Value *float64 `firestore:"value"`
}

type projectedLinkDoc struct {
	SnapshotID      string `firestore:"snapshotId"`
	SessionID       string `firestore:"sessionId"`
	ConfidenceLevel string `firestore:"confidenceLevel"`
}

type TelemetryMatchStore struct {
	db *firestore.Client
}

var _ coretelemetry.MatchStore = (*TelemetryMatchStore)(nil)

func NewTelemetryMatchStore(db *firestore.Client) *TelemetryMatchStore {
	return &TelemetryMatchStore{db: db}
}

func (f *TelemetryMatchStore) buildBaseQuery(characterID, snapshotID string, from, to time.Time) firestore.Query {
	var q firestore.Query
	if snapshotID != "" {
		q = f.db.Collection(aggregatesCollection).Where("snapshotIds", "array-contains", snapshotID)
	} else {
		q = f.db.Collection(aggregatesCollection).Where("characterIds", "array-contains", characterID)
	}
	if !from.IsZero() {
		q = q.Where("activityHistory.period", ">=", from)
	}
	if !to.IsZero() {
		q = q.Where("activityHistory.period", "<=", to)
	}
	return q.OrderBy("activityHistory.period", firestore.Desc)
}

func applyTelemetryProjection(q firestore.Query, characterID string) firestore.Query {
	return q.SelectPaths(
		firestore.FieldPath{"activityId"},
		firestore.FieldPath{"activityHistory"},
		firestore.FieldPath{"performance", characterID, "playerStats"},
		firestore.FieldPath{"snapshotLinks", characterID},
	)
}

func (f *TelemetryMatchStore) ListRecentUnfiltered(
	ctx context.Context,
	characterID string,
	from, to time.Time,
	limit int,
) ([]coretelemetry.RawMatch, int, bool, error) {
	baseQuery := f.buildBaseQuery(characterID, "", from, to)
	projectedQuery := applyTelemetryProjection(baseQuery, characterID)
	iter := projectedQuery.Limit(limit + 1).Documents(ctx)
	defer iter.Stop()

	matches := make([]coretelemetry.RawMatch, 0, min(limit, 64))
	rawDocs := 0
	validDocs := 0
	skippedCorrupt := 0
	var lastDoc *firestore.DocumentSnapshot

	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, 0, false, fmt.Errorf("iterate projected aggregates: %w", err)
		}
		rawDocs++
		lastDoc = doc

		raw, ok, err := decodeProjectedMatch(doc, characterID)
		if err != nil {
			return nil, 0, false, err
		}
		if !ok {
			skippedCorrupt++
			continue
		}
		validDocs++
		if len(matches) < limit {
			matches = append(matches, raw)
		}
	}

	if rawDocs <= limit {
		return matches, validDocs, false, nil
	}

	if skippedCorrupt == 0 {
		totalCount, err := f.countQuery(ctx, baseQuery)
		if err != nil {
			return nil, 0, false, err
		}
		if totalCount < len(matches) {
			totalCount = len(matches)
		}
		return matches, totalCount, totalCount > len(matches), nil
	}

	// Corrupt docs appeared within the initial limit+1 window; continue past lastDoc
	// so matches fills up to limit valid entries and totalCount excludes corrupt docs.
	tailIter := projectedQuery.StartAfter(lastDoc).Documents(ctx)
	defer tailIter.Stop()

	for {
		doc, err := tailIter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, 0, false, fmt.Errorf("iterate remaining projected aggregates: %w", err)
		}
		raw, ok, err := decodeProjectedMatch(doc, characterID)
		if err != nil {
			return nil, 0, false, err
		}
		if !ok {
			continue
		}
		validDocs++
		if len(matches) < limit {
			matches = append(matches, raw)
		}
	}

	return matches, validDocs, validDocs > len(matches), nil
}

func (f *TelemetryMatchStore) StreamMatches(
	ctx context.Context,
	characterID, snapshotID string,
	from, to time.Time,
	fn func(coretelemetry.RawMatch) error,
) error {
	baseQuery := f.buildBaseQuery(characterID, snapshotID, from, to)
	iter := applyTelemetryProjection(baseQuery, characterID).Documents(ctx)
	defer iter.Stop()

	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream projected aggregates: %w", err)
		}
		raw, ok, err := decodeProjectedMatch(doc, characterID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := fn(raw); err != nil {
			return err
		}
	}
	return nil
}

func (f *TelemetryMatchStore) countQuery(ctx context.Context, q firestore.Query) (int, error) {
	results, err := q.NewAggregationQuery().WithCount("total").Get(ctx)
	if err != nil {
		return 0, fmt.Errorf("count character aggregates: %w", err)
	}
	countVal, ok := results["total"]
	if !ok {
		return 0, fmt.Errorf("count aggregation missing 'total' alias")
	}
	switch v := countVal.(type) {
	case *firestorepb.Value:
		return int(v.GetIntegerValue()), nil
	case int64:
		return int(v), nil
	case int:
		return v, nil
	default:
		return 0, fmt.Errorf("unexpected count aggregation type %T", countVal)
	}
}

func decodeProjectedMatch(doc *firestore.DocumentSnapshot, characterID string) (coretelemetry.RawMatch, bool, error) {
	var p projectedAggregateDoc
	if err := doc.DataTo(&p); err != nil {
		return coretelemetry.RawMatch{}, false, fmt.Errorf("decode aggregate %s: %w", doc.Ref.ID, err)
	}

	perf, ok := p.Performance[characterID]
	if !ok || perf.PlayerStats == nil {
		return coretelemetry.RawMatch{}, false, nil
	}

	stats := perf.PlayerStats
	kills := int(stats.Kills.Value)
	deaths := int(stats.Deaths.Value)
	assists := int(stats.Assists.Value)
	won := stats.Standing.Value != nil && *stats.Standing.Value == 0

	var timePlayed int
	hasTimePlayed := false
	if stats.TimePlayed.Value != nil {
		timePlayed = int(*stats.TimePlayed.Value)
		hasTimePlayed = true
	}

	activityID := p.ActivityID
	if activityID == "" {
		activityID = p.ActivityHistory.InstanceID
	}

	raw := coretelemetry.RawMatch{
		ActivityID:              activityID,
		Period:                  p.ActivityHistory.Period,
		Location:                p.ActivityHistory.Location,
		Activity:                p.ActivityHistory.Activity,
		Mode:                    p.ActivityHistory.Mode,
		StoredEstimatedGameMode: coretelemetry.GameMode(p.ActivityHistory.EstimatedGameMode),
		Kills:                   kills,
		Deaths:                  deaths,
		Assists:                 assists,
		Won:                     won,
		TimePlayedSeconds:       timePlayed,
		HasTimePlayedSeconds:    hasTimePlayed,
	}

	if link, hasLink := p.SnapshotLinks[characterID]; hasLink {
		raw.SessionID = link.SessionID
		raw.RawSnapshotID = link.SnapshotID
		raw.SnapshotConfidence = coretelemetry.ConfidenceLevel(link.ConfidenceLevel)
	}

	return raw, true, nil
}
