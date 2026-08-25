package stats

import (
	"context"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/utils"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
)

// Service defines operations for retrieving stats-related data.
// Note: "Loadout" in product language corresponds to a Snapshot in code.
// This service focuses on aggregating data to support stats views for a user's loadouts.
type Service interface {
	GetAggregatesForSnapshot(ctx context.Context, snapshotID string, gameModeFilter []string) ([]api.Aggregate, error)
	GetAggregatesByCharacterID(ctx context.Context, characterID string, gameModeFilter []string) ([]api.Aggregate, error)

	GetMostUsedLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string) ([]api.CharacterSnapshot, map[string]int, error)
	GetBestPerformingLoadouts(ctx context.Context, aggs []api.Aggregate, characterID string, limit int8, minimumGames int) ([]api.CharacterSnapshot, map[string]api.PlayerStats, map[string]int, error)

	GetFeaturedLoadouts(ctx context.Context, count int, gameMode *api.GameMode) ([]api.FeaturedLoadout, error)
}

type service struct {
	DB              *firestore.Client
	snapshotService snapshot.Service
	userService     user.Service
}

// NewService creates a new Stats service instance.
func NewService(db *firestore.Client, snapshotService snapshot.Service, userService user.Service) Service {
	return &service{DB: db, snapshotService: snapshotService, userService: userService}
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

func (s *service) GetAggregatesByCharacterID(ctx context.Context, characterID string, activityFilter []string) ([]api.Aggregate, error) {
	if characterID == "" {
		return nil, fmt.Errorf("characterID is required")
	}
	q := s.DB.Collection(aggregatesCollection).
		Where("characterIds", "array-contains", characterID)

	if len(activityFilter) > 0 {
		q = q.Where("activityHistory.activity", "in", activityFilter)
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
		Wins    int
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
			slog.Warn("no performance found for character", "characterID", characterID)
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
	slog.Debug("skipping loadout check", "characterID", characterID, "minimumGames", minimumGames)
	skipped := 0
	for id, obj := range stats {
		if counts[id] < minimumGames {
			skipped++
			continue
		}
		pairs = append(pairs, pair{id: id, stats: obj, counts: counts[id]})
	}
	slog.Debug("loadouts skipped", "skipped", skipped)

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
			Standing: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKD(s.Wins, count))),
				Value:        ptr.Of(getKD(s.Wins, count)),
			}),
		}
	}

	if len(ids) == 0 {
		return nil, nil, nil, fmt.Errorf("no loadouts found")
	}
	loadouts, err := s.snapshotService.GetByIDs(ctx, ids)
	if err != nil {
		slog.Error("failed to get loadouts", "error", err)
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

	q := s.DB.Collection(aggregatesCollection).Query
	if gameMode != nil {
		q = q.Where("activityHistory.mode", "==", string(*gameMode))
	}
	aggDocs, err := q.Limit(500).Documents(ctx).GetAll()
	if err != nil {
		slog.Error("failed fetching aggregates for featured loadouts", "error", err)
	}

	statsMap := make(map[string]*snapStats)
	if err == nil && len(aggDocs) > 0 {
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
		if err != nil || snap == nil {
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
			if err != nil || snap == nil {
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
	if len(featured) < count {
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
		featured[i].FeaturedReason = ptr.Of(reason)
	}

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
		feat.UsageCount = ptr.Of(st.GamesCount)
		feat.Stats = &api.PlayerStats{
			Assists: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", st.Assists)),
				Value:        ptr.Of(float64(st.Assists)),
			}),
			Deaths: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", st.Deaths)),
				Value:        ptr.Of(float64(st.Deaths)),
			}),
			Kills: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", st.Kills)),
				Value:        ptr.Of(float64(st.Kills)),
			}),
			Kd: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKD(st.Kills, st.Deaths))),
				Value:        ptr.Of(getKD(st.Kills, st.Deaths)),
			}),
			Kda: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKDA(st.Kills, st.Deaths, st.Assists))),
				Value:        ptr.Of(getKDA(st.Kills, st.Deaths, st.Assists)),
			}),
			Standing: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKD(st.Wins, st.GamesCount))),
				Value:        ptr.Of(getKD(st.Wins, st.GamesCount)),
			}),
		}
	} else {
		feat.UsageCount = ptr.Of(1)
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
		feat.UsageCount = ptr.Of(st.GamesCount)
		feat.Stats = &api.PlayerStats{
			Assists: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", st.Assists)),
				Value:        ptr.Of(float64(st.Assists)),
			}),
			Deaths: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", st.Deaths)),
				Value:        ptr.Of(float64(st.Deaths)),
			}),
			Kills: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%d", st.Kills)),
				Value:        ptr.Of(float64(st.Kills)),
			}),
			Kd: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKD(st.Kills, st.Deaths))),
				Value:        ptr.Of(getKD(st.Kills, st.Deaths)),
			}),
			Kda: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKDA(st.Kills, st.Deaths, st.Assists))),
				Value:        ptr.Of(getKDA(st.Kills, st.Deaths, st.Assists)),
			}),
			Standing: ptr.Of(api.StatsValuePair{
				DisplayValue: ptr.Of(fmt.Sprintf("%.2f", getKD(st.Wins, st.GamesCount))),
				Value:        ptr.Of(getKD(st.Wins, st.GamesCount)),
			}),
		}
	} else {
		feat.UsageCount = ptr.Of(1)
	}

	return feat
}
