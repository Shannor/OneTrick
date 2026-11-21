package stats

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/utils"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

// Service defines operations for retrieving stats-related data.
// Note: "Loadout" in product language corresponds to a Snapshot in code.
// This service focuses on aggregating data to support stats views for a user's loadouts.
type Service interface {
	GetAggregatesForSnapshot(ctx context.Context, snapshotID string, gameMode *api.GameMode) ([]api.Aggregate, error)
	GetAggregatesByCharacterID(ctx context.Context, characterID string, gameMode *api.GameMode) ([]api.Aggregate, error)

	GetMostUsedLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string) ([]api.CharacterSnapshot, map[string]int, error)
	GetBestPerformingLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string, limit int8, minimumGames int) ([]api.CharacterSnapshot, map[string]api.PlayerStats, map[string]int, error)
	GetSnapshotsWithMetrics(ctx context.Context, userID string, characterID string, gameMode *api.GameMode, minimumGames int, sortBy string, includeStats bool, hasSimilar bool, count int, offset int) ([]api.CharacterSnapshot, *map[string]api.PlayerStats, *map[string]int, *map[string]int, error)
	GetSimilarSnapshots(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, []api.SimilarSnapshotItem, *map[string]api.PlayerStats, *map[string]int, error)

	GetFeaturedLoadouts(ctx context.Context, count int, gameMode *api.GameMode) ([]api.FeaturedLoadout, error)
}

type service struct {
	DB              *firestore.Client
	snapshotService snapshot.Service
	userService     user.Service
	manifestService destiny.ManifestService
}

// NewService creates a new Stats service instance.
func NewService(db *firestore.Client, snapshotService snapshot.Service, userService user.Service, manifestService destiny.ManifestService) Service {
	return &service{
		DB:              db,
		snapshotService: snapshotService,
		userService:     userService,
		manifestService: manifestService,
	}
}

const (
	aggregatesCollection = "aggregates"
	snapshotsCollection  = "snapshots"
)

type aggStatSummary struct {
	Kills   int
	Deaths  int
	Assists int
	Wins    int
}

func summarizeAggregatesForCharacter(aggs []api.Aggregate, characterID string) (map[string]aggStatSummary, map[string]int) {
	stats := make(map[string]aggStatSummary)
	counts := make(map[string]int)
	for _, agg := range aggs {
		link, ok := agg.SnapshotLinks[characterID]
		if !ok || link.SnapshotID == nil || *link.SnapshotID == "" {
			continue
		}
		performance, ok := agg.Performance[characterID]
		if !ok {
			continue
		}
		st := stats[*link.SnapshotID]
		if performance.PlayerStats.Kills != nil && performance.PlayerStats.Kills.Value != nil {
			st.Kills += int(*performance.PlayerStats.Kills.Value)
		}
		if performance.PlayerStats.Deaths != nil && performance.PlayerStats.Deaths.Value != nil {
			st.Deaths += int(*performance.PlayerStats.Deaths.Value)
		}
		if performance.PlayerStats.Assists != nil && performance.PlayerStats.Assists.Value != nil {
			st.Assists += int(*performance.PlayerStats.Assists.Value)
		}
		if performance.PlayerStats.Standing != nil && performance.PlayerStats.Standing.Value != nil && *performance.PlayerStats.Standing.Value == 0 {
			st.Wins++
		}
		stats[*link.SnapshotID] = st
		counts[*link.SnapshotID]++
	}
	return stats, counts
}

func buildPlayerStats(st aggStatSummary, count int) api.PlayerStats {
	return api.PlayerStats{
		Assists: new(api.StatsValuePair{
			DisplayValue: new(fmt.Sprintf("%d", st.Assists)),
			Value:        new(float64(st.Assists)),
		}),
		Deaths: new(api.StatsValuePair{
			DisplayValue: new(fmt.Sprintf("%d", st.Deaths)),
			Value:        new(float64(st.Deaths)),
		}),
		Kills: new(api.StatsValuePair{
			DisplayValue: new(fmt.Sprintf("%d", st.Kills)),
			Value:        new(float64(st.Kills)),
		}),
		Kd: new(api.StatsValuePair{
			DisplayValue: new(fmt.Sprintf("%.2f", getKD(st.Kills, st.Deaths))),
			Value:        new(getKD(st.Kills, st.Deaths)),
		}),
		Kda: new(api.StatsValuePair{
			DisplayValue: new(fmt.Sprintf("%.2f", getKDA(st.Kills, st.Deaths, st.Assists))),
			Value:        new(getKDA(st.Kills, st.Deaths, st.Assists)),
		}),
		Standing: new(api.StatsValuePair{
			DisplayValue: new(fmt.Sprintf("%.2f", getKD(st.Wins, count))),
			Value:        new(getKD(st.Wins, count)),
		}),
	}
}

func buildPageSimilarCounts(snapshots []api.CharacterSnapshot, allSimilarCounts map[string]int) *map[string]int {
	simMap := make(map[string]int)
	for _, snap := range snapshots {
		if c := allSimilarCounts[snap.ID]; c > 0 {
			simMap[snap.ID] = c
		}
	}
	return &simMap
}

// MatchesGameMode classifies whether an ActivityHistory record belongs to the specified GameMode
// by inspecting both history.Mode and history.Activity.
func MatchesGameMode(history api.ActivityHistory, gameMode api.GameMode) bool {
	return destiny.MatchesGameMode(history, gameMode)
}

func hasGameModeFilter(gameMode *api.GameMode) bool {
	return gameMode != nil && *gameMode != "" && *gameMode != api.GameModeAll
}

func filterSnapshotsByGameModeAndMinGames(allSnapshots []api.CharacterSnapshot, counts map[string]int, gameMode *api.GameMode, minimumGames int) []api.CharacterSnapshot {
	return filterSnapshotsForList(allSnapshots, counts, nil, nil, gameMode, minimumGames, false)
}

func filterSnapshotsForList(
	allSnapshots []api.CharacterSnapshot,
	counts map[string]int,
	similarCounts map[string]int,
	peerGroups map[string][]string,
	gameMode *api.GameMode,
	minimumGames int,
	hasSimilar bool,
) []api.CharacterSnapshot {
	gameModeActive := hasGameModeFilter(gameMode)
	filtered := make([]api.CharacterSnapshot, 0, len(allSnapshots))
	for _, snap := range allSnapshots {
		if snapshot.IsArchived(&snap) {
			continue
		}
		if hasSimilar && similarCounts[snap.ID] == 0 {
			continue
		}
		effectiveCount := counts[snap.ID]
		if hasSimilar {
			groupTotal := 0
			for _, peerID := range peerGroups[snap.ID] {
				groupTotal += counts[peerID]
			}
			if groupTotal > effectiveCount {
				effectiveCount = groupTotal
			}
		}
		if gameModeActive && effectiveCount == 0 {
			continue
		}
		if minimumGames > 0 && effectiveCount < minimumGames {
			continue
		}
		filtered = append(filtered, snap)
	}
	return filtered
}

func paginateSnapshots(snapshots []api.CharacterSnapshot, count int, offset int) []api.CharacterSnapshot {
	if offset > 0 {
		if offset >= len(snapshots) {
			return []api.CharacterSnapshot{}
		}
		snapshots = snapshots[offset:]
	}
	limit := 10
	if count > 0 {
		limit = count
	}
	if limit < len(snapshots) {
		snapshots = snapshots[:limit]
	}
	return snapshots
}

func (s *service) GetAggregatesForSnapshot(ctx context.Context, snapshotID string, gameMode *api.GameMode) ([]api.Aggregate, error) {
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshotID is required")
	}
	clog.WithGroupField(ctx, "statsService.getAggregatesForSnapshot", "snapshotId", snapshotID)

	q := s.DB.Collection(aggregatesCollection).
		Where("snapshotIds", "array-contains", snapshotID)

	if hasGameModeFilter(gameMode) {
		q = q.Where("activityHistory.estimatedGameMode", "==", string(*gameMode))
		clog.WithGroupField(ctx, "statsService.getAggregatesForSnapshot", "gameMode", string(*gameMode))
	}

	docs, err := q.Documents(ctx).GetAll()

	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[api.Aggregate](docs)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "statsService.getAggregatesForSnapshot", "returnedCount", len(results))
	return results, nil
}

func (s *service) GetAggregatesByCharacterID(ctx context.Context, characterID string, gameMode *api.GameMode) ([]api.Aggregate, error) {
	if characterID == "" {
		return nil, fmt.Errorf("characterID is required")
	}
	clog.WithGroupField(ctx, "statsService.getAggregatesByCharacterId", "characterId", characterID)
	q := s.DB.Collection(aggregatesCollection).
		Where("characterIds", "array-contains", characterID)

	if hasGameModeFilter(gameMode) {
		q = q.Where("activityHistory.estimatedGameMode", "==", string(*gameMode))
		clog.WithGroupField(ctx, "statsService.getAggregatesByCharacterId", "gameMode", string(*gameMode))
	}

	aggDocs, err := q.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	aggs, err := utils.GetAllToStructs[api.Aggregate](aggDocs)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "statsService.getAggregatesByCharacterId", "returnedCount", len(aggs))
	return aggs, nil
}

func (s *service) GetSnapshotsWithMetrics(
	ctx context.Context,
	userID string,
	characterID string,
	gameMode *api.GameMode,
	minimumGames int,
	sortBy string,
	includeStats bool,
	hasSimilar bool,
	count int,
	offset int,
) ([]api.CharacterSnapshot, *map[string]api.PlayerStats, *map[string]int, *map[string]int, error) {
	if characterID == "" {
		return nil, nil, nil, nil, fmt.Errorf("characterID is required")
	}

	gameModeActive := hasGameModeFilter(gameMode)
	needsAggs := includeStats || sortBy == "win_rate" || sortBy == "kd_ratio" || sortBy == "matches_played" || gameModeActive || minimumGames > 0

	clog.WithGroupFields(ctx, "statsService.getSnapshotsWithMetrics", clog.Fields{
		"userId":       userID,
		"characterId":  characterID,
		"minimumGames": minimumGames,
		"sortBy":       sortBy,
		"includeStats": includeStats,
		"hasSimilar":   hasSimilar,
		"count":        count,
		"offset":       offset,
		"fastPath":     !needsAggs && !hasSimilar && sortBy == "created_at",
	})
	if gameMode != nil {
		clog.WithGroupField(ctx, "statsService.getSnapshotsWithMetrics", "gameMode", string(*gameMode))
	}

	// Always load all active character snapshots upfront so similarCounts is computed across
	// the full active character pool BEFORE applying minimumGames or gameMode filters.
	allSnapshots, err := s.snapshotService.GetAllByCharacter(ctx, userID, characterID, 0, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	allSimilarCounts, peerGroups := ComputeSimilarCounts(allSnapshots)

	if !needsAggs && sortBy == "created_at" {
		filtered := allSnapshots
		if hasSimilar {
			filtered = filterSnapshotsForList(allSnapshots, nil, allSimilarCounts, peerGroups, nil, 0, true)
		}
		paged := paginateSnapshots(filtered, count, offset)
		clog.WithGroupField(ctx, "statsService.getSnapshotsWithMetrics", "returnedCount", len(paged))
		return paged, nil, nil, buildPageSimilarCounts(paged, allSimilarCounts), nil
	}

	aggs, err := s.GetAggregatesByCharacterID(ctx, characterID, gameMode)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	stats, counts := summarizeAggregatesForCharacter(aggs, characterID)

	filteredSnapshots := filterSnapshotsForList(allSnapshots, counts, allSimilarCounts, peerGroups, gameMode, minimumGames, hasSimilar)
	passedFilterCount := len(filteredSnapshots)

	// Sort snapshots based on sortBy
	slices.SortFunc(filteredSnapshots, func(a, b api.CharacterSnapshot) int {
		switch sortBy {
		case "win_rate":
			ca, cb := counts[a.ID], counts[b.ID]
			wra := getKD(stats[a.ID].Wins, ca)
			wrb := getKD(stats[b.ID].Wins, cb)
			if wra == wrb {
				return b.CreatedAt.Compare(a.CreatedAt)
			}
			if wra < wrb {
				return 1
			}
			return -1
		case "kd_ratio":
			sa, sb := stats[a.ID], stats[b.ID]
			kda := getKD(sa.Kills, sa.Deaths)
			kdb := getKD(sb.Kills, sb.Deaths)
			if kda == kdb {
				return b.CreatedAt.Compare(a.CreatedAt)
			}
			if kda < kdb {
				return 1
			}
			return -1
		case "matches_played":
			ca, cb := counts[a.ID], counts[b.ID]
			if ca == cb {
				return b.CreatedAt.Compare(a.CreatedAt)
			}
			return cb - ca
		case "created_at":
			fallthrough
		default:
			return b.CreatedAt.Compare(a.CreatedAt)
		}
	})

	filteredSnapshots = paginateSnapshots(filteredSnapshots, count, offset)

	clog.WithGroupFields(ctx, "statsService.getSnapshotsWithMetrics", clog.Fields{
		"numAggregates":                 len(aggs),
		"totalSnapshotsInDb":            len(allSnapshots),
		"uniqueSnapshotsWithAggMatches": len(counts),
		"passedFilterCount":             passedFilterCount,
		"returnedCount":                 len(filteredSnapshots),
	})

	simMap := buildPageSimilarCounts(filteredSnapshots, allSimilarCounts)
	if len(filteredSnapshots) == 0 && offset > 0 {
		return []api.CharacterSnapshot{}, nil, nil, simMap, nil
	}

	var finalPlayerStats *map[string]api.PlayerStats
	var finalCounts *map[string]int

	if includeStats || sortBy == "win_rate" || sortBy == "kd_ratio" || sortBy == "matches_played" {
		psMap := make(map[string]api.PlayerStats, len(filteredSnapshots))
		cMap := make(map[string]int, len(filteredSnapshots))
		for _, snap := range filteredSnapshots {
			c := counts[snap.ID]
			st := stats[snap.ID]
			cMap[snap.ID] = c
			psMap[snap.ID] = buildPlayerStats(st, c)
		}
		finalPlayerStats = &psMap
		finalCounts = &cMap
	}

	return filteredSnapshots, finalPlayerStats, finalCounts, simMap, nil
}

func (s *service) GetSimilarSnapshots(
	ctx context.Context,
	snapshotID string,
) (*api.CharacterSnapshot, []api.SimilarSnapshotItem, *map[string]api.PlayerStats, *map[string]int, error) {
	if snapshotID == "" {
		return nil, nil, nil, nil, snapshot.NotFound
	}
	clog.WithGroupField(ctx, "statsService.getSimilarSnapshots", "snapshotId", snapshotID)

	target, err := s.snapshotService.Get(ctx, snapshotID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if target == nil {
		return nil, nil, nil, nil, snapshot.NotFound
	}

	aggs, err := s.GetAggregatesByCharacterID(ctx, target.CharacterID, nil)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	aggStats, aggCounts := summarizeAggregatesForCharacter(aggs, target.CharacterID)

	if snapshot.IsArchived(target) {
		cMap := map[string]int{target.ID: aggCounts[target.ID]}
		psMap := map[string]api.PlayerStats{target.ID: buildPlayerStats(aggStats[target.ID], aggCounts[target.ID])}
		return target, []api.SimilarSnapshotItem{}, &psMap, &cMap, nil
	}

	allSnapshots, err := s.snapshotService.GetAllByCharacter(ctx, target.UserID, target.CharacterID, 0, 0)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	tierCache := make(map[int64]int32)
	tierLookup := func(itemHash int64) int32 {
		if itemHash == 0 || s.manifestService == nil {
			return 0
		}
		if cached, ok := tierCache[itemHash]; ok {
			return cached
		}
		def, err := s.manifestService.GetItem(ctx, itemHash)
		if err != nil || def == nil {
			tierCache[itemHash] = 0
			return 0
		}
		tier := int32(def.Inventory.TierType)
		tierCache[itemHash] = tier
		return tier
	}

	items := make([]api.SimilarSnapshotItem, 0)
	for i := range allSnapshots {
		cand := &allSnapshots[i]
		if cand.ID == "" || cand.ID == target.ID || snapshot.IsArchived(cand) {
			continue
		}
		if !snapshot.CanMerge(target, cand) {
			continue
		}
		items = append(items, CompareSimilarLoadout(target, cand, tierLookup))
	}

	slices.SortFunc(items, func(a, b api.SimilarSnapshotItem) int {
		if a.Tier != b.Tier {
			if a.Tier == api.SimilarityTierHigh {
				return -1
			}
			return 1
		}
		ca, cb := aggCounts[a.Snapshot.ID], aggCounts[b.Snapshot.ID]
		if ca != cb {
			return cb - ca
		}
		if cmpTime := b.Snapshot.CreatedAt.Compare(a.Snapshot.CreatedAt); cmpTime != 0 {
			return cmpTime
		}
		return strings.Compare(a.Snapshot.ID, b.Snapshot.ID)
	})

	psMap := make(map[string]api.PlayerStats, len(items)+1)
	cMap := make(map[string]int, len(items)+1)

	targetCount := aggCounts[target.ID]
	cMap[target.ID] = targetCount
	psMap[target.ID] = buildPlayerStats(aggStats[target.ID], targetCount)

	for _, item := range items {
		c := aggCounts[item.Snapshot.ID]
		cMap[item.Snapshot.ID] = c
		psMap[item.Snapshot.ID] = buildPlayerStats(aggStats[item.Snapshot.ID], c)
	}

	clog.WithGroupFields(ctx, "statsService.getSimilarSnapshots", clog.Fields{
		"characterId":    target.CharacterID,
		"candidateCount": len(items),
	})

	return target, items, &psMap, &cMap, nil
}

func (s *service) GetMostUsedLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string) ([]api.CharacterSnapshot, map[string]int, error) {
	if characterID == "" {
		return nil, nil, fmt.Errorf("characterID is required")
	}
	clog.WithGroupFields(ctx, "statsService.getMostUsedLoadouts", clog.Fields{
		"characterId":    characterID,
		"aggregateCount": len(aggs),
	})

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
	loadouts = slices.DeleteFunc(loadouts, func(snap api.CharacterSnapshot) bool {
		if snapshot.IsArchived(&snap) {
			delete(finalCount, snap.ID)
			return true
		}
		return false
	})
	slices.SortFunc(loadouts, func(a, b api.CharacterSnapshot) int {
		if order[a.ID] == order[b.ID] {
			return 0
		}
		return order[a.ID] - order[b.ID]
	})
	clog.WithGroupField(ctx, "statsService.getMostUsedLoadouts", "returnedCount", len(loadouts))
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
		Wins    int
	}
	stats := make(map[string]stat)
	counts := make(map[string]int)
	missingPerf := 0
	for _, agg := range aggs {
		link, ok := agg.SnapshotLinks[characterID]
		if !ok || link.SnapshotID == nil || *link.SnapshotID == "" {
			continue
		}
		performance, ok := agg.Performance[characterID]
		if !ok {
			missingPerf++
			continue
		}
		s := stats[*link.SnapshotID]
		s.Kills += int(*performance.PlayerStats.Kills.Value)
		s.Deaths += int(*performance.PlayerStats.Deaths.Value)
		s.Assists += int(*performance.PlayerStats.Assists.Value)
		// Zero is a win in D2
		if *performance.PlayerStats.Standing.Value == 0 {
			s.Wins++
		}
		stats[*link.SnapshotID] = s
		counts[*link.SnapshotID]++
	}

	// 3) Sort snapshot IDs by K/D and KD/A
	type pair struct {
		id     string
		stats  stat
		counts int
	}
	pairs := make([]pair, 0, len(stats))
	skipped := 0
	for id, obj := range stats {
		if counts[id] < minimumGames {
			skipped++
			continue
		}
		pairs = append(pairs, pair{id: id, stats: obj, counts: counts[id]})
	}
	clog.WithGroupFields(ctx, "statsService.getBestPerformingLoadouts", clog.Fields{
		"characterId":        characterID,
		"minimumGames":       minimumGames,
		"skippedCount":       skipped,
		"missingPerformance": missingPerf,
	})

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
		count := pairs[idx].counts
		finalCount[pairs[idx].id] = pairs[idx].counts
		order[pairs[idx].id] = int(idx + 1)
		s := pairs[idx].stats
		finalPlayerStats[pairs[idx].id] = api.PlayerStats{
			Assists: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", s.Assists)),
				Value:        new(float64(s.Assists)),
			}),
			Deaths: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", s.Deaths)),
				Value:        new(float64(s.Deaths)),
			}),
			Kills: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", s.Kills)),
				Value:        new(float64(s.Kills)),
			}),
			Kd: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKD(s.Kills, s.Deaths))),
				Value:        new(getKD(s.Kills, s.Deaths)),
			}),
			Kda: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKDA(s.Kills, s.Deaths, s.Assists))),
				Value:        new(getKDA(s.Kills, s.Deaths, s.Assists)),
			}),
			Standing: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKD(s.Wins, count))),
				Value:        new(getKD(s.Wins, count)),
			}),
		}
	}

	if len(ids) == 0 {
		clog.WithGroupField(ctx, "statsService.getBestPerformingLoadouts", "returnedCount", 0)
		return []api.CharacterSnapshot{}, finalPlayerStats, finalCount, nil
	}
	loadouts, err := s.snapshotService.GetByIDs(ctx, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	loadouts = slices.DeleteFunc(loadouts, func(snap api.CharacterSnapshot) bool {
		if snapshot.IsArchived(&snap) {
			delete(finalPlayerStats, snap.ID)
			delete(finalCount, snap.ID)
			return true
		}
		return false
	})
	slices.SortFunc(loadouts, func(a, b api.CharacterSnapshot) int {
		if order[a.ID] == order[b.ID] {
			return 0
		}
		return order[a.ID] - order[b.ID]
	})
	clog.WithGroupField(ctx, "statsService.getBestPerformingLoadouts", "returnedCount", len(loadouts))
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

type snapStats struct {
	Kills      int
	Deaths     int
	Assists    int
	Wins       int
	GamesCount int
}

func (s *service) GetFeaturedLoadouts(ctx context.Context, count int, gameMode *api.GameMode) ([]api.FeaturedLoadout, error) {
	if count <= 0 {
		count = 5
	}
	clog.WithGroupField(ctx, "statsService.getFeaturedLoadouts", "requestedCount", count)

	statsMap := make(map[string]*snapStats)
	if s.DB != nil {
		q := s.DB.Collection(aggregatesCollection).Query
		if hasGameModeFilter(gameMode) {
			q = q.Where("activityHistory.estimatedGameMode", "==", string(*gameMode))
		}
		aggDocs, err := q.Limit(500).Documents(ctx).GetAll()
		if err != nil {
			clog.WithGroupField(ctx, "statsService.getFeaturedLoadouts", "warning", "failed fetching aggregates for featured loadouts")
		} else if len(aggDocs) > 0 {
			aggs, err := utils.GetAllToStructs[api.Aggregate](aggDocs)
			if err == nil {
				for _, agg := range aggs {
					for charID, link := range agg.SnapshotLinks {
						if link.SnapshotID == nil || *link.SnapshotID == "" {
							continue
						}
						snapID := *link.SnapshotID
						st, exists := statsMap[snapID]
						if !exists {
							st = &snapStats{}
							statsMap[snapID] = st
						}
						st.GamesCount++
						if perf, ok := agg.Performance[charID]; ok && perf.PlayerStats.Kills != nil {
							if perf.PlayerStats.Kills.Value != nil {
								st.Kills += int(*perf.PlayerStats.Kills.Value)
							}
							if perf.PlayerStats.Deaths.Value != nil {
								st.Deaths += int(*perf.PlayerStats.Deaths.Value)
							}
							if perf.PlayerStats.Assists.Value != nil {
								st.Assists += int(*perf.PlayerStats.Assists.Value)
							}
							if perf.PlayerStats.Standing.Value != nil && *perf.PlayerStats.Standing.Value == 0 {
								st.Wins++
							}
						}
					}
				}
			}
		}
	}

	type snapCandidate struct {
		snapID string
		stats  *snapStats
		snap   *api.CharacterSnapshot
		user   *api.User
		class  string
	}

	// Filter candidates with > 5 games minimum requirement
	var candidates []snapCandidate
	for id, st := range statsMap {
		if st.GamesCount <= 5 {
			continue
		}
		snap, err := s.snapshotService.Get(ctx, id)
		if err != nil || snap == nil || snapshot.IsArchived(snap) {
			continue
		}

		var u *api.User
		var charClass string
		if snap.UserID != "" && s.userService != nil {
			userObj, err := s.userService.GetUser(ctx, snap.UserID)
			if err == nil && userObj != nil {
				u = &api.User{
					ID:                  userObj.ID,
					DisplayName:         userObj.DisplayName,
					UniqueName:          userObj.UniqueName,
					MemberID:            userObj.MemberID,
					PrimaryMembershipID: userObj.PrimaryMembershipID,
					CreatedAt:           userObj.CreatedAt,
					CharacterIDs:        userObj.CharacterIDs,
				}
				for _, c := range userObj.Characters {
					if c.Id == snap.CharacterID && c.Class != "" {
						charClass = c.Class
						break
					}
				}
			}
		}

		subclassName := ""
		if snap.Loadout != nil {
			subClassKey := strconv.FormatInt(destiny.SubClass, 10)
			if item, ok := snap.Loadout[subClassKey]; ok {
				subclassName = item.ItemProperties.BaseInfo.Name
			}
		}

		detectedClass := determineClass(charClass, subclassName)
		candidates = append(candidates, snapCandidate{
			snapID: id,
			stats:  st,
			snap:   snap,
			user:   u,
			class:  detectedClass,
		})
	}

	// If fewer candidates have > 5 games, loosen threshold to > 0 games
	if len(candidates) < count {
		for id, st := range statsMap {
			if st.GamesCount > 5 {
				continue // already included
			}
			snap, err := s.snapshotService.Get(ctx, id)
			if err != nil || snap == nil || snapshot.IsArchived(snap) {
				continue
			}

			var u *api.User
			var charClass string
			if snap.UserID != "" && s.userService != nil {
				userObj, err := s.userService.GetUser(ctx, snap.UserID)
				if err == nil && userObj != nil {
					u = &api.User{
						ID:                  userObj.ID,
						DisplayName:         userObj.DisplayName,
						UniqueName:          userObj.UniqueName,
						MemberID:            userObj.MemberID,
						PrimaryMembershipID: userObj.PrimaryMembershipID,
						CreatedAt:           userObj.CreatedAt,
						CharacterIDs:        userObj.CharacterIDs,
					}
					for _, c := range userObj.Characters {
						if c.Id == snap.CharacterID && c.Class != "" {
							charClass = c.Class
							break
						}
					}
				}
			}

			subclassName := ""
			if snap.Loadout != nil {
				subClassKey := strconv.FormatInt(destiny.SubClass, 10)
				if item, ok := snap.Loadout[subClassKey]; ok {
					subclassName = item.ItemProperties.BaseInfo.Name
				}
			}

			detectedClass := determineClass(charClass, subclassName)
			candidates = append(candidates, snapCandidate{
				snapID: id,
				stats:  st,
				snap:   snap,
				user:   u,
				class:  detectedClass,
			})
		}
	}

	// Sort candidates by games count and KD
	slices.SortFunc(candidates, func(a, b snapCandidate) int {
		if a.stats.GamesCount != b.stats.GamesCount {
			return b.stats.GamesCount - a.stats.GamesCount
		}
		kda := getKD(a.stats.Kills, a.stats.Deaths)
		kdb := getKD(b.stats.Kills, b.stats.Deaths)
		if kda < kdb {
			return 1
		}
		if kda > kdb {
			return -1
		}
		return 0
	})

	daySeed := time.Now().YearDay() + time.Now().Year()*365

	// Partition candidates by class for mandatory class representation (Titan, Hunter, Warlock)
	classBuckets := map[string][]snapCandidate{
		"Titan":   {},
		"Hunter":  {},
		"Warlock": {},
		"Other":   {},
	}
	for _, c := range candidates {
		classBuckets[c.class] = append(classBuckets[c.class], c)
	}

	// Rotate each class bucket deterministically by daySeed
	rotateBucket := func(bucket []snapCandidate) []snapCandidate {
		if len(bucket) <= 1 {
			return bucket
		}
		offset := daySeed % len(bucket)
		rotated := make([]snapCandidate, len(bucket))
		for i := 0; i < len(bucket); i++ {
			rotated[i] = bucket[(i+offset)%len(bucket)]
		}
		return rotated
	}

	classBuckets["Titan"] = rotateBucket(classBuckets["Titan"])
	classBuckets["Hunter"] = rotateBucket(classBuckets["Hunter"])
	classBuckets["Warlock"] = rotateBucket(classBuckets["Warlock"])
	classBuckets["Other"] = rotateBucket(classBuckets["Other"])

	var featured []api.FeaturedLoadout
	seenSnapIDs := make(map[string]bool)
	seenUserIDs := make(map[string]bool)

	addCandidate := func(cand snapCandidate) bool {
		if seenSnapIDs[cand.snapID] {
			return false
		}
		if cand.snap.UserID != "" && seenUserIDs[cand.snap.UserID] {
			return false
		}

		seenSnapIDs[cand.snapID] = true
		if cand.snap.UserID != "" {
			seenUserIDs[cand.snap.UserID] = true
		}

		featItem := s.buildFeaturedLoadoutWithUser(cand.snap, cand.user, cand.stats)
		featured = append(featured, featItem)
		return true
	}

	// Guarantee at least 1 Titan, 1 Hunter, and 1 Warlock
	for _, cls := range []string{"Titan", "Hunter", "Warlock"} {
		for _, cand := range classBuckets[cls] {
			if addCandidate(cand) {
				break
			}
		}
	}

	// Fill remaining slots up to count (default 5) from all remaining candidates
	remainingPool := append([]snapCandidate{}, classBuckets["Titan"]...)
	remainingPool = append(remainingPool, classBuckets["Hunter"]...)
	remainingPool = append(remainingPool, classBuckets["Warlock"]...)
	remainingPool = append(remainingPool, classBuckets["Other"]...)
	remainingPool = rotateBucket(remainingPool)

	for _, cand := range remainingPool {
		if len(featured) >= count {
			break
		}
		addCandidate(cand)
	}

	// Fallback to recent snapshots if we still have fewer than count
	if len(featured) < count && s.DB != nil {
		recentDocs, err := s.DB.Collection(snapshotsCollection).
			OrderBy("createdAt", firestore.Desc).
			Limit(count * 5).
			Documents(ctx).GetAll()
		if err == nil {
			recentSnaps, err := utils.GetAllToStructs[api.CharacterSnapshot](recentDocs)
			if err == nil {
				if len(recentSnaps) > count {
					offset := daySeed % len(recentSnaps)
					rotated := make([]api.CharacterSnapshot, len(recentSnaps))
					for i := 0; i < len(recentSnaps); i++ {
						rotated[i] = recentSnaps[(i+offset)%len(recentSnaps)]
					}
					recentSnaps = rotated
				}

				for _, snap := range recentSnaps {
					if len(featured) >= count {
						break
					}
					if snapshot.IsArchived(&snap) {
						continue
					}
					if seenSnapIDs[snap.ID] {
						continue
					}
					if snap.UserID != "" && seenUserIDs[snap.UserID] {
						continue
					}

					seenSnapIDs[snap.ID] = true
					if snap.UserID != "" {
						seenUserIDs[snap.UserID] = true
					}

					featItem := s.buildFeaturedLoadout(ctx, &snap, nil)
					featured = append(featured, featItem)
				}
			}
		}
	}

	// Assign detailed featured reasons
	for i := range featured {
		classType := "PvP"
		if featured[i].Snapshot.Loadout != nil {
			subClassKey := strconv.FormatInt(destiny.SubClass, 10)
			if item, ok := featured[i].Snapshot.Loadout[subClassKey]; ok {
				if item.ItemProperties.BaseInfo.Name != "" {
					classType = item.ItemProperties.BaseInfo.Name
				}
			}
		}

		reason := fmt.Sprintf("Featured %s Choice", classType)
		if i == 0 {
			reason = fmt.Sprintf("Top %s PvP Loadout of the Day", classType)
		} else if featured[i].UsageCount != nil && *featured[i].UsageCount > 5 {
			reason = fmt.Sprintf("Most Active %s Community Loadout (>5 Games)", classType)
		}
		featured[i].FeaturedReason = new(reason)
	}

	clog.WithGroupFields(ctx, "statsService.getFeaturedLoadouts", clog.Fields{
		"candidateCount": len(candidates),
		"returnedCount":  len(featured),
	})
	return featured, nil
}

func determineClass(charClass string, subclassName string) string {
	c := strings.ToLower(charClass)
	sub := strings.ToLower(subclassName)

	if strings.Contains(c, "titan") || strings.Contains(sub, "striker") || strings.Contains(sub, "sunbreaker") || strings.Contains(sub, "sentinel") || strings.Contains(sub, "behemoth") || strings.Contains(sub, "berserker") {
		return "Titan"
	}
	if strings.Contains(c, "hunter") || strings.Contains(sub, "gunslinger") || strings.Contains(sub, "nightstalker") || strings.Contains(sub, "arcstrider") || strings.Contains(sub, "revenant") || strings.Contains(sub, "threadrunner") {
		return "Hunter"
	}
	if strings.Contains(c, "warlock") || strings.Contains(sub, "voidwalker") || strings.Contains(sub, "dawnblade") || strings.Contains(sub, "stormcaller") || strings.Contains(sub, "shadebinder") || strings.Contains(sub, "broodweaver") {
		return "Warlock"
	}
	return "Other"
}

func (s *service) buildFeaturedLoadoutWithUser(snap *api.CharacterSnapshot, u *api.User, st *snapStats) api.FeaturedLoadout {
	feat := api.FeaturedLoadout{
		Snapshot: *snap,
		User:     u,
	}

	if st != nil {
		feat.UsageCount = new(st.GamesCount)
		feat.Stats = &api.PlayerStats{
			Assists: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", st.Assists)),
				Value:        new(float64(st.Assists)),
			}),
			Deaths: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", st.Deaths)),
				Value:        new(float64(st.Deaths)),
			}),
			Kills: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", st.Kills)),
				Value:        new(float64(st.Kills)),
			}),
			Kd: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKD(st.Kills, st.Deaths))),
				Value:        new(getKD(st.Kills, st.Deaths)),
			}),
			Kda: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKDA(st.Kills, st.Deaths, st.Assists))),
				Value:        new(getKDA(st.Kills, st.Deaths, st.Assists)),
			}),
			Standing: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKD(st.Wins, st.GamesCount))),
				Value:        new(getKD(st.Wins, st.GamesCount)),
			}),
		}
	} else {
		feat.UsageCount = new(1)
	}

	return feat
}

func (s *service) buildFeaturedLoadout(ctx context.Context, snap *api.CharacterSnapshot, st *snapStats) api.FeaturedLoadout {
	feat := api.FeaturedLoadout{
		Snapshot: *snap,
	}

	if snap.UserID != "" && s.userService != nil {
		u, err := s.userService.GetUser(ctx, snap.UserID)
		if err == nil && u != nil {
			feat.User = &api.User{
				ID:                  u.ID,
				DisplayName:         u.DisplayName,
				UniqueName:          u.UniqueName,
				MemberID:            u.MemberID,
				PrimaryMembershipID: u.PrimaryMembershipID,
				CreatedAt:           u.CreatedAt,
				CharacterIDs:        u.CharacterIDs,
			}
		}
	}

	if st != nil {
		feat.UsageCount = new(st.GamesCount)
		feat.Stats = &api.PlayerStats{
			Assists: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", st.Assists)),
				Value:        new(float64(st.Assists)),
			}),
			Deaths: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", st.Deaths)),
				Value:        new(float64(st.Deaths)),
			}),
			Kills: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%d", st.Kills)),
				Value:        new(float64(st.Kills)),
			}),
			Kd: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKD(st.Kills, st.Deaths))),
				Value:        new(getKD(st.Kills, st.Deaths)),
			}),
			Kda: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKDA(st.Kills, st.Deaths, st.Assists))),
				Value:        new(getKDA(st.Kills, st.Deaths, st.Assists)),
			}),
			Standing: new(api.StatsValuePair{
				DisplayValue: new(fmt.Sprintf("%.2f", getKD(st.Wins, st.GamesCount))),
				Value:        new(getKD(st.Wins, st.GamesCount)),
			}),
		}
	} else {
		feat.UsageCount = new(1)
	}

	return feat
}
