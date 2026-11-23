package session

import (
	"context"
	"fmt"
	"math"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/set"
	"sort"
	"strconv"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

const (
	topWeaponsLimit      = 5
	uniqueWeaponKillsKey = "uniqueWeaponKills"
	unknownWeaponLabel   = "Unknown Weapon"
	bungieAssetBaseURL   = "https://www.bungie.net"
)

func (s service) ComputeSessionSummary(ctx context.Context, ses api.Session) (*api.SessionSummary, error) {
	if len(ses.AggregateIDs) == 0 {
		return emptySessionSummary(), nil
	}
	if s.aggregateService == nil {
		return nil, fmt.Errorf("aggregate service is not configured")
	}

	aggregates, err := s.aggregateService.GetAggregates(ctx, ses.AggregateIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch aggregates for session %s: %w", ses.ID, err)
	}

	acc := newSummaryAccumulator(len(aggregates))
	for _, agg := range aggregates {
		acc.addAggregate(agg, ses.CharacterID)
	}

	s.resolveMissingModes(ctx, acc)
	topWeapons := s.buildTopWeapons(ctx, acc)
	summary := acc.buildSummary(topWeapons)

	clog.WithGroupFields(ctx, "sessionService.computeSummary", clog.Fields{
		"sessionId":    ses.ID,
		"totalMatches": *summary.TotalMatches,
		"wins":         *summary.Wins,
		"losses":       *summary.Losses,
	})
	return summary, nil
}

func (s service) UpdateSessionSummary(ctx context.Context, sessionID string, summary *api.SessionSummary) error {
	clog.WithGroupField(ctx, "sessionService.updateSummary", "sessionId", sessionID)
	return s.store.UpdateSummary(ctx, sessionID, summary, s.now())
}

type summaryAccumulator struct {
	totalMatches      int
	wins              int
	kills             int
	deaths            int
	assists           int
	modes             *set.OrderedSet[string]
	missingModeHashes []int64
	weapons           map[int64]*weaponTally
}

func newSummaryAccumulator(totalMatches int) *summaryAccumulator {
	return &summaryAccumulator{
		totalMatches: totalMatches,
		modes:        set.NewOrdered[string](),
		weapons:      make(map[int64]*weaponTally),
	}
}

func (a *summaryAccumulator) addAggregate(agg api.Aggregate, characterID string) {
	a.recordMode(agg.ActivityDetails)

	perf, ok := agg.Performance[characterID]
	if !ok {
		return
	}
	a.recordPlayerStats(perf.PlayerStats)
	a.recordWeapons(perf.Weapons)
}

func (a *summaryAccumulator) recordMode(details api.ActivityHistory) {
	switch {
	case details.Mode != nil && *details.Mode != "":
		a.addMode(*details.Mode)
	case details.ActivityHash != 0:
		a.missingModeHashes = append(a.missingModeHashes, details.ActivityHash)
	case details.ReferenceID != 0:
		a.missingModeHashes = append(a.missingModeHashes, details.ReferenceID)
	}
}

func (a *summaryAccumulator) addMode(name string) {
	if name != "" {
		a.modes.Add(name)
	}
}

func (a *summaryAccumulator) recordPlayerStats(stats api.PlayerStats) {
	if isWin(stats) {
		a.wins++
	}
	a.kills += statInt(stats.Kills)
	a.deaths += statInt(stats.Deaths)
	a.assists += statInt(stats.Assists)
}

func (a *summaryAccumulator) recordWeapons(weapons map[string]api.WeaponInstanceMetrics) {
	for mapKey, metrics := range weapons {
		refID := resolveWeaponRefID(mapKey, metrics)
		if refID == 0 {
			continue
		}
		tally := a.weaponTally(refID)
		tally.kills += weaponKillCount(metrics)
		tally.mergeFallbackDisplay(metrics.Display)
	}
}

func (a *summaryAccumulator) weaponTally(refID int64) *weaponTally {
	tally, exists := a.weapons[refID]
	if !exists {
		tally = &weaponTally{hash: refID}
		a.weapons[refID] = tally
	}
	return tally
}

func (a *summaryAccumulator) weaponHashes() []int64 {
	hashes := make([]int64, 0, len(a.weapons))
	for h := range a.weapons {
		hashes = append(hashes, h)
	}
	return hashes
}

func (a *summaryAccumulator) buildSummary(topWeapons []api.SessionWeaponSummary) *api.SessionSummary {
	totalMatches := a.totalMatches
	wins := a.wins
	losses := totalMatches - wins
	kills := a.kills
	deaths := a.deaths
	assists := a.assists
	modes := a.modes.ToSlice()

	var winRate, kdRatio, kdaRatio float64
	if totalMatches > 0 {
		winRate = round2(float64(wins) / float64(totalMatches))
		denom := max(float64(deaths), 1.0)
		kdRatio = round2(float64(kills) / denom)
		kdaRatio = round2((float64(kills) + float64(assists)*0.5) / denom)
	}

	return &api.SessionSummary{
		TotalMatches: &totalMatches,
		Wins:         &wins,
		Losses:       &losses,
		WinRate:      &winRate,
		Kills:        &kills,
		Deaths:       &deaths,
		Assists:      &assists,
		KDRatio:      &kdRatio,
		KDARatio:     &kdaRatio,
		ModesPlayed:  &modes,
		TopWeapons:   &topWeapons,
	}
}

type weaponTally struct {
	hash         int64
	kills        int
	fallbackName string
	fallbackIcon string
}

func (w *weaponTally) mergeFallbackDisplay(display *api.Display) {
	if display == nil {
		return
	}
	if w.fallbackName == "" && display.Name != "" {
		w.fallbackName = display.Name
	}
	if w.fallbackIcon == "" && display.Icon != nil {
		w.fallbackIcon = *display.Icon
	}
}

func (w *weaponTally) toSummary(defs map[string]destiny.ItemDefinition) api.SessionWeaponSummary {
	name := w.fallbackName
	icon := w.fallbackIcon

	if def, found := defs[strconv.FormatInt(w.hash, 10)]; found {
		if def.DisplayProperties.Name != "" {
			name = def.DisplayProperties.Name
		}
		if def.DisplayProperties.Icon != "" {
			icon = def.DisplayProperties.Icon
		}
	}

	if name == "" {
		name = unknownWeaponLabel
	}
	icon = formatBungieIconURL(icon)
	kills := w.kills

	return api.SessionWeaponSummary{
		Name:  &name,
		Icon:  &icon,
		Kills: &kills,
	}
}

func (s service) resolveMissingModes(ctx context.Context, acc *summaryAccumulator) {
	if len(acc.missingModeHashes) == 0 || s.manifestService == nil {
		return
	}
	actDefs, err := s.manifestService.GetActivitiesByIDs(ctx, acc.missingModeHashes)
	if err != nil || len(actDefs) == 0 {
		return
	}

	var modeIDs []int64
	for _, def := range actDefs {
		if def.DirectActivityModeHash != 0 {
			modeIDs = append(modeIDs, int64(def.DirectActivityModeHash))
		}
		for _, mHash := range def.ActivityModeHashes {
			modeIDs = append(modeIDs, int64(mHash))
		}
	}
	if len(modeIDs) == 0 {
		return
	}

	modeDefs, err := s.manifestService.GetActivityModesByIDs(ctx, modeIDs)
	if err != nil {
		return
	}
	for _, mDef := range modeDefs {
		acc.addMode(mDef.DisplayProperties.Name)
	}
}

func (s service) buildTopWeapons(ctx context.Context, acc *summaryAccumulator) []api.SessionWeaponSummary {
	itemDefs := make(map[string]destiny.ItemDefinition)
	if hashes := acc.weaponHashes(); len(hashes) > 0 && s.manifestService != nil {
		if fetched, err := s.manifestService.GetItemsByIDs(ctx, hashes); err == nil {
			itemDefs = fetched
		}
	}

	topWeapons := make([]api.SessionWeaponSummary, 0, len(acc.weapons))
	for _, tally := range acc.weapons {
		topWeapons = append(topWeapons, tally.toSummary(itemDefs))
	}

	sort.Slice(topWeapons, func(i, j int) bool {
		ki, kj := *topWeapons[i].Kills, *topWeapons[j].Kills
		if ki != kj {
			return ki > kj
		}
		return *topWeapons[i].Name < *topWeapons[j].Name
	})

	if len(topWeapons) > topWeaponsLimit {
		topWeapons = topWeapons[:topWeaponsLimit]
	}
	return topWeapons
}

func emptySessionSummary() *api.SessionSummary {
	return newSummaryAccumulator(0).buildSummary(make([]api.SessionWeaponSummary, 0))
}

func isWin(stats api.PlayerStats) bool {
	return stats.Standing != nil && stats.Standing.Value != nil && *stats.Standing.Value == 0
}

func statInt(pair *api.StatsValuePair) int {
	if pair == nil || pair.Value == nil {
		return 0
	}
	return int(*pair.Value)
}

func resolveWeaponRefID(mapKey string, metrics api.WeaponInstanceMetrics) int64 {
	if metrics.ReferenceID != nil && *metrics.ReferenceID != 0 {
		return *metrics.ReferenceID
	}
	parsed, err := strconv.ParseInt(mapKey, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func weaponKillCount(metrics api.WeaponInstanceMetrics) int {
	if metrics.Stats == nil {
		return 0
	}
	kills := 0
	for statKey, statVal := range *metrics.Stats {
		isKillStat := statKey == uniqueWeaponKillsKey || (statVal.Name != nil && *statVal.Name == uniqueWeaponKillsKey)
		if isKillStat && statVal.Basic.Value != nil {
			kills += int(*statVal.Basic.Value)
		}
	}
	return kills
}

func formatBungieIconURL(icon string) string {
	if icon != "" && !strings.HasPrefix(icon, "http") {
		return bungieAssetBaseURL + icon
	}
	return icon
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
