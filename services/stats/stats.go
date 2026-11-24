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
	"golang.org/x/sync/errgroup"
)

// Service defines operations for retrieving stats-related data.
// Note: "Loadout" in product language corresponds to a Snapshot in code.
// This service focuses on aggregating data to support stats views for a user's loadouts.
type Service interface {
	GetAggregatesForSnapshot(ctx context.Context, snapshotID string, gameMode *api.GameMode) ([]api.Aggregate, error)

	GetBestPerformingLoadouts(ctx context.Context, characterID string, gameMode *api.GameMode, limit int, minimumGames int) ([]api.CharacterSnapshot, map[string]api.PlayerStats, map[string]int, error)
	GetSnapshotsWithMetrics(ctx context.Context, userID string, characterID string, gameMode *api.GameMode, minimumGames int, sortBy string, includeStats bool, hasSimilar bool, count int, offset int) ([]api.CharacterSnapshot, *map[string]api.PlayerStats, *map[string]int, *map[string]int, error)
	GetSimilarSnapshots(ctx context.Context, params SimilarSnapshotsParams) (*api.CharacterSnapshot, []api.SimilarSnapshotItem, *map[string]api.PlayerStats, *map[string]int, error)

	GetFeaturedLoadouts(ctx context.Context, count int, gameMode *api.GameMode) ([]api.FeaturedLoadout, error)
}

type service struct {
	DB              *firestore.Client
	store           Store
	snapshotService snapshot.Service
	userService     user.Service
	manifestService destiny.ManifestService
}

// NewService creates a new Stats service instance.
func NewService(db *firestore.Client, store Store, snapshotService snapshot.Service, userService user.Service, manifestService destiny.ManifestService) Service {
	return &service{
		DB:              db,
		store:           store,
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

func summarizeCharacterMatchStats(matches []CharacterMatchStat) (map[string]aggStatSummary, map[string]int) {
	stats := make(map[string]aggStatSummary)
	counts := make(map[string]int)
	for _, match := range matches {
		if match.SnapshotID == "" {
			continue
		}
		st := stats[match.SnapshotID]
		st.Kills += match.Kills
		st.Deaths += match.Deaths
		st.Assists += match.Assists
		if match.Won {
			st.Wins++
		}
		stats[match.SnapshotID] = st
		counts[match.SnapshotID]++
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

func normalizeGameModeString(gameMode string) string {
	if gameMode == "" || gameMode == string(api.GameModeAll) {
		return ""
	}
	return gameMode
}

func normalizeGameModeFilter(gameMode *api.GameMode) string {
	if !hasGameModeFilter(gameMode) {
		return ""
	}
	return normalizeGameModeString(string(*gameMode))
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
	if !needsAggs && sortBy == "created_at" {
		allSnapshots, err := s.snapshotService.GetAllByCharacter(ctx, userID, characterID, 0, 0)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		allSimilarCounts, peerGroups := ComputeSimilarCounts(allSnapshots)
		filtered := allSnapshots
		if hasSimilar {
			filtered = filterSnapshotsForList(allSnapshots, nil, allSimilarCounts, peerGroups, nil, 0, true)
		}
		paged := paginateSnapshots(filtered, count, offset)
		clog.WithGroupField(ctx, "statsService.getSnapshotsWithMetrics", "returnedCount", len(paged))
		return paged, nil, nil, buildPageSimilarCounts(paged, allSimilarCounts), nil
	}

	if s.store == nil {
		return nil, nil, nil, nil, fmt.Errorf("stats store is not configured")
	}

	var (
		allSnapshots     []api.CharacterSnapshot
		allSimilarCounts map[string]int
		peerGroups       map[string][]string
		matchStats       []CharacterMatchStat
		stats            map[string]aggStatSummary
		counts           map[string]int
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		snaps, err := s.snapshotService.GetAllByCharacter(gctx, userID, characterID, 0, 0)
		if err != nil {
			return err
		}
		allSnapshots = snaps
		allSimilarCounts, peerGroups = ComputeSimilarCounts(snaps)
		return nil
	})
	g.Go(func() error {
		ms, err := s.store.ListCharacterMatchStats(gctx, characterID, normalizeGameModeFilter(gameMode))
		if err != nil {
			return err
		}
		matchStats = ms
		stats, counts = summarizeCharacterMatchStats(ms)
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, nil, nil, nil, err
	}

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
		"numAggregates":                 len(matchStats),
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
	params SimilarSnapshotsParams,
) (*api.CharacterSnapshot, []api.SimilarSnapshotItem, *map[string]api.PlayerStats, *map[string]int, error) {
	if params.SnapshotID == "" {
		return nil, nil, nil, nil, snapshot.NotFound
	}
	if params.IncludeStats && s.store == nil {
		return nil, nil, nil, nil, fmt.Errorf("stats store is not configured")
	}

	gameMode := normalizeGameModeString(params.GameMode)
	tier := params.Tier
	if tier == "" {
		tier = SimilarTierAll
	}

	clog.WithGroupFields(ctx, "statsService.getSimilarSnapshots", clog.Fields{
		"snapshotId":   params.SnapshotID,
		"includeStats": params.IncludeStats,
		"tier":         string(tier),
	})
	if gameMode != "" {
		clog.WithGroupField(ctx, "statsService.getSimilarSnapshots", "gameMode", gameMode)
	}

	target, err := s.snapshotService.Get(ctx, params.SnapshotID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if target == nil {
		return nil, nil, nil, nil, snapshot.NotFound
	}

	if snapshot.IsArchived(target) {
		clog.WithGroupFields(ctx, "statsService.getSimilarSnapshots", clog.Fields{
			"characterId":    target.CharacterID,
			"candidateCount": 0,
		})
		if !params.IncludeStats {
			return target, []api.SimilarSnapshotItem{}, nil, nil, nil
		}
		matchStats, err := s.store.ListCharacterMatchStats(ctx, target.CharacterID, gameMode)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		aggStats, aggCounts := summarizeCharacterMatchStats(matchStats)
		cMap := map[string]int{target.ID: aggCounts[target.ID]}
		psMap := map[string]api.PlayerStats{target.ID: buildPlayerStats(aggStats[target.ID], aggCounts[target.ID])}
		return target, []api.SimilarSnapshotItem{}, &psMap, &cMap, nil
	}

	var (
		aggStats     map[string]aggStatSummary
		aggCounts    map[string]int
		allSnapshots []api.CharacterSnapshot
	)

	if params.IncludeStats {
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			matchStats, err := s.store.ListCharacterMatchStats(gctx, target.CharacterID, gameMode)
			if err != nil {
				return err
			}
			aggStats, aggCounts = summarizeCharacterMatchStats(matchStats)
			return nil
		})
		g.Go(func() error {
			snaps, err := s.snapshotService.GetAllByCharacter(gctx, target.UserID, target.CharacterID, 0, 0)
			if err != nil {
				return err
			}
			allSnapshots = snaps
			return nil
		})
		if err := g.Wait(); err != nil {
			return nil, nil, nil, nil, err
		}
	} else {
		snaps, err := s.snapshotService.GetAllByCharacter(ctx, target.UserID, target.CharacterID, 0, 0)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		allSnapshots = snaps
	}

	candidateIndices := make([]int, 0)
	for i := range allSnapshots {
		cand := &allSnapshots[i]
		if cand.ID == "" || cand.ID == target.ID || snapshot.IsArchived(cand) {
			continue
		}
		if !snapshot.CanMerge(target, cand) {
			continue
		}
		candidateIndices = append(candidateIndices, i)
	}

	tierByHash := s.batchResolveItemTiers(ctx, target, allSnapshots, candidateIndices)
	var tierLookup func(int64) int32
	if len(tierByHash) > 0 {
		tierLookup = func(itemHash int64) int32 {
			return tierByHash[itemHash]
		}
	}

	items := make([]api.SimilarSnapshotItem, 0, len(candidateIndices))
	for _, idx := range candidateIndices {
		item := CompareSimilarLoadout(target, &allSnapshots[idx], tierLookup)
		if tier == SimilarTierHigh && item.Tier != api.SimilarityTierHigh {
			continue
		}
		items = append(items, item)
	}

	slices.SortFunc(items, func(a, b api.SimilarSnapshotItem) int {
		if a.Tier != b.Tier {
			if a.Tier == api.SimilarityTierHigh {
				return -1
			}
			return 1
		}
		if params.IncludeStats {
			ca, cb := aggCounts[a.Snapshot.ID], aggCounts[b.Snapshot.ID]
			if ca != cb {
				return cb - ca
			}
		}
		if cmpTime := b.Snapshot.CreatedAt.Compare(a.Snapshot.CreatedAt); cmpTime != 0 {
			return cmpTime
		}
		return strings.Compare(a.Snapshot.ID, b.Snapshot.ID)
	})

	clog.WithGroupFields(ctx, "statsService.getSimilarSnapshots", clog.Fields{
		"characterId":    target.CharacterID,
		"candidateCount": len(items),
	})

	if !params.IncludeStats {
		return target, items, nil, nil, nil
	}

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

	return target, items, &psMap, &cMap, nil
}

func (s *service) batchResolveItemTiers(
	ctx context.Context,
	target *api.CharacterSnapshot,
	allSnapshots []api.CharacterSnapshot,
	candidateIndices []int,
) map[int64]int32 {
	if len(candidateIndices) == 0 || s.manifestService == nil {
		return nil
	}
	missingHashes := collectMissingArmorTierHashes(target, allSnapshots, candidateIndices)
	if len(missingHashes) == 0 {
		return nil
	}
	defs, err := s.manifestService.GetItemsByIDs(ctx, missingHashes)
	if err != nil {
		clog.WithGroupField(ctx, "statsService.getSimilarSnapshots", "manifestLookupError", err.Error())
		return nil
	}
	tiers := make(map[int64]int32, len(defs))
	for k, def := range defs {
		hash := def.Hash
		if hash == 0 {
			parsed, parseErr := strconv.ParseInt(k, 10, 64)
			if parseErr != nil {
				continue
			}
			hash = parsed
		}
		tiers[hash] = int32(def.Inventory.TierType)
	}
	return tiers
}

func (s *service) GetBestPerformingLoadouts(ctx context.Context, characterID string, gameMode *api.GameMode, limit int, minimumGames int) ([]api.CharacterSnapshot, map[string]api.PlayerStats, map[string]int, error) {
	if characterID == "" {
		return nil, nil, nil, fmt.Errorf("characterID is required")
	}
	if s.store == nil {
		return nil, nil, nil, fmt.Errorf("stats store is not configured")
	}

	matchStats, err := s.store.ListCharacterMatchStats(ctx, characterID, normalizeGameModeFilter(gameMode))
	if err != nil {
		return nil, nil, nil, err
	}

	stats, counts := summarizeCharacterMatchStats(matchStats)

	type pair struct {
		id     string
		stats  aggStatSummary
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
		"characterId":    characterID,
		"minimumGames":   minimumGames,
		"skippedCount":   skipped,
		"matchStatCount": len(matchStats),
	})
	if gameMode != nil {
		clog.WithGroupField(ctx, "statsService.getBestPerformingLoadouts", "gameMode", string(*gameMode))
	}

	slices.SortFunc(pairs, func(a, b pair) int {
		kda := getKD(a.stats.Kills, a.stats.Deaths)
		kdb := getKD(b.stats.Kills, b.stats.Deaths)
		if kda != kdb {
			if kda < kdb {
				return 1
			}
			return -1
		}
		if a.counts != b.counts {
			return b.counts - a.counts
		}
		return strings.Compare(a.id, b.id)
	})

	if limit <= 0 || len(pairs) == 0 {
		clog.WithGroupField(ctx, "statsService.getBestPerformingLoadouts", "returnedCount", 0)
		return []api.CharacterSnapshot{}, map[string]api.PlayerStats{}, map[string]int{}, nil
	}

	capHint := min(limit, len(pairs))
	loadouts := make([]api.CharacterSnapshot, 0, capHint)
	finalPlayerStats := make(map[string]api.PlayerStats, capHint)
	finalCount := make(map[string]int, capHint)

	// Walk ranked candidates in batches so archived or deleted snapshots don't under-fill limit or orphan map keys.
	batchSize := max(limit, 10)
	for start := 0; start < len(pairs) && len(loadouts) < limit; start += batchSize {
		end := min(start+batchSize, len(pairs))
		batch := pairs[start:end]

		batchIDs := make([]string, len(batch))
		for i, p := range batch {
			batchIDs[i] = p.id
		}

		fetched, err := s.snapshotService.GetByIDs(ctx, batchIDs)
		if err != nil {
			return nil, nil, nil, err
		}

		activeByID := make(map[string]api.CharacterSnapshot, len(fetched))
		for _, snap := range fetched {
			if snapshot.IsArchived(&snap) {
				continue
			}
			activeByID[snap.ID] = snap
		}

		for _, p := range batch {
			snap, ok := activeByID[p.id]
			if !ok {
				continue
			}
			loadouts = append(loadouts, snap)
			finalPlayerStats[p.id] = buildPlayerStats(p.stats, p.counts)
			finalCount[p.id] = p.counts
			if len(loadouts) == limit {
				break
			}
		}
	}

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
