package stats

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/snapshot"
	"oneTrick/utils"
	"slices"

	"cloud.google.com/go/firestore"
	"github.com/rs/zerolog/log"
)

// Service defines operations for retrieving stats-related data.
// Note: "Loadout" in product language corresponds to a Snapshot in code.
// This service focuses on aggregating data to support stats views for a user's loadouts.
type Service interface {
	GetAggregatesForSnapshot(ctx context.Context, snapshotID string, gameModeFilter []string) ([]api.Aggregate, error)
	GetAggregatesByCharacterID(ctx context.Context, characterID string, gameModeFilter []string) ([]api.Aggregate, error)

	GetMostUsedLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string) ([]api.CharacterSnapshot, map[string]int, error)
	GetBestPerformingLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string, limit int8, minimumGames int) ([]api.CharacterSnapshot, map[string]api.PlayerStats, map[string]int, error)
}

type service struct {
	DB              *firestore.Client
	snapshotService snapshot.Service
}

// NewService creates a new Stats service instance.
func NewService(db *firestore.Client, snapshotService snapshot.Service) Service {
	return &service{DB: db, snapshotService: snapshotService}
}

const (
	aggregatesCollection = "aggregates"
	snapshotsCollection  = "snapshots"
)

func (s *service) GetAggregatesForSnapshot(ctx context.Context, snapshotID string, gameModeFilter []string) ([]api.Aggregate, error) {
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshotID is required")
	}

	q := s.DB.Collection(aggregatesCollection).
		Where("snapshotIds", "array-contains", snapshotID)

	if len(gameModeFilter) > 0 {
		q = q.Where("activityHistory.mode", "in", gameModeFilter)
	}

	docs, err := q.Documents(ctx).GetAll()

	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[api.Aggregate](docs)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *service) GetAggregatesByCharacterID(ctx context.Context, characterID string, gameModeFilter []string) ([]api.Aggregate, error) {
	if characterID == "" {
		return nil, fmt.Errorf("characterID is required")
	}
	q := s.DB.Collection(aggregatesCollection).
		Where("characterIds", "array-contains", characterID)

	if len(gameModeFilter) > 0 {
		q = q.Where("activityHistory.mode", "in", gameModeFilter)
	}

	aggDocs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	aggs, err := utils.GetAllToStructs[api.Aggregate](aggDocs)
	if err != nil {
		return nil, err
	}
	return aggs, nil
}

// GetMostUsedLoadouts returns the top 10 most used loadouts for the given characterID.
// Implementation details:
// - This yields all activity aggregates where this character was linked to the specified snapshot (loadout).
// - We then sort the results by the number of sessions (sessions.length) and return the top 10.
func (s *service) GetMostUsedLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string) ([]api.CharacterSnapshot, map[string]int, error) {
	if characterID == "" {
		return nil, nil, fmt.Errorf("characterID is required")
	}

	counts := map[string]int{}
	for _, agg := range aggs {
		link, ok := agg.SnapshotLinks[characterID]
		if !ok || link.SnapshotID == nil || *link.SnapshotID == "" {
			continue
		}
		counts[*link.SnapshotID]++
	}

	// 3) Sort snapshot IDs by count desc and return the top 10
	type pair struct {
		id    string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for id, c := range counts {
		pairs = append(pairs, pair{id: id, count: c})
	}

	slices.SortFunc(pairs, func(a, b pair) int {
		if a.count == b.count {
			return 0
		}
		return b.count - a.count
	})

	limit := 10
	if len(pairs) < limit {
		limit = len(pairs)
	}

	ids := make([]string, 0, limit)
	finalCount := make(map[string]int)
	order := make(map[string]int, len(pairs))
	for idx := 0; idx < limit; idx++ {
		ids = append(ids, pairs[idx].id)
		finalCount[pairs[idx].id] = pairs[idx].count
		order[pairs[idx].id] = idx + 1
	}

	loadouts, err := s.snapshotService.GetByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	slices.SortFunc(loadouts, func(a, b api.CharacterSnapshot) int {
		if order[a.ID] == order[b.ID] {
			return 0
		}
		return order[a.ID] - order[b.ID]
	})
	return loadouts, finalCount, nil
}

func (s *service) GetBestPerformingLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string, limit int8, minimumGames int) ([]api.CharacterSnapshot, map[string]api.PlayerStats, map[string]int, error) {
	if characterID == "" {
		return nil, nil, nil, fmt.Errorf("characterID is required")
	}

	type stat struct {
		Kills   int
		Deaths  int
		Assists int
	}
	stats := make(map[string]stat)
	counts := make(map[string]int)
	for _, agg := range aggs {
		link, ok := agg.SnapshotLinks[characterID]
		if !ok || link.SnapshotID == nil || *link.SnapshotID == "" {
			continue
		}
		performance, ok := agg.Performance[characterID]
		if !ok {
			log.Warn().Str("characterID", characterID).Msg("no performance found for character")
			continue
		}
		s := stats[*link.SnapshotID]
		s.Kills += int(*performance.PlayerStats.Kills.Value)
		s.Deaths += int(*performance.PlayerStats.Deaths.Value)
		s.Assists += int(*performance.PlayerStats.Assists.Value)
		stats[*link.SnapshotID] = s
		counts[*link.SnapshotID]++
	}

	// TODO: In the future we would want to omit any loadouts that have not been used more than X times,

	// 3) Sort snapshot IDs by K/D and KD/A
	type pair struct {
		id     string
		stats  stat
		counts int
	}
	pairs := make([]pair, 0, len(stats))
	log.Debug().Str("characterID", characterID).Int("Required Games Count", minimumGames).Msg("skipping loadout")
	skipped := 0
	for id, obj := range stats {
		if counts[id] < minimumGames {
			skipped++
			continue
		}
		pairs = append(pairs, pair{id: id, stats: obj, counts: counts[id]})
	}
	log.Debug().Int("skipped", skipped).Msg("loadouts skipped")

	slices.SortFunc(pairs, func(a, b pair) int {
		kda := getKD(a.stats.Kills, a.stats.Deaths)
		kdb := getKD(b.stats.Kills, b.stats.Deaths)
		if kda == kdb {
			return 0
		}
		if kda < kdb {
			return 1
		}
		return -1
	})

	l := int(limit)
	if len(pairs) < l {
		l = len(pairs)
	}

	ids := make([]string, 0, l)
	finalPlayerStats := make(map[string]api.PlayerStats)
	finalCount := make(map[string]int)
	order := make(map[string]int, len(pairs))

	for idx := 0; idx < l; idx++ {
		ids = append(ids, pairs[idx].id)
		finalCount[pairs[idx].id] = pairs[idx].counts
		order[pairs[idx].id] = int(idx + 1)
		s := pairs[idx].stats
		finalPlayerStats[pairs[idx].id] = api.PlayerStats{
			Assists: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", s.Assists)),
				Value:        ptr.Of(float64(s.Assists)),
			}),
			Deaths: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", s.Deaths)),
				Value:        ptr.Of(float64(s.Deaths)),
			}),
			Kills: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", s.Kills)),
				Value:        ptr.Of(float64(s.Kills)),
			}),
			Kd: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKD(s.Kills, s.Deaths))),
				Value:        ptr.Of(getKD(s.Kills, s.Deaths)),
			}),
			Kda: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKDA(s.Kills, s.Deaths, s.Assists))),
				Value:        ptr.Of(getKDA(s.Kills, s.Deaths, s.Assists)),
			}),
		}
	}

	if len(ids) == 0 {
		return nil, nil, nil, fmt.Errorf("no loadouts found")
	}
	loadouts, err := s.snapshotService.GetByIDs(ctx, ids)
	if err != nil {
		log.Error().Err(err).Msg("failed to get loadouts")
		return nil, nil, nil, err
	}
	slices.SortFunc(loadouts, func(a, b api.CharacterSnapshot) int {
		if order[a.ID] == order[b.ID] {
			return 0
		}
		return order[a.ID] - order[b.ID]
	})
	return loadouts, finalPlayerStats, finalCount, nil
}

func getKD(kills int, deaths int) float64 {
	if deaths == 0 {
		return float64(kills)
	}
	return float64(kills) / float64(deaths)
}

func getKDA(kills int, deaths int, assists int) float64 {
	if deaths == 0 {
		return float64(kills) + float64(assists)
	}
	return (float64(kills) + float64(assists)) / float64(deaths)
}
