package session

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/destiny"
	"oneTrick/set"
	"slices"
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
	for i, agg := range aggregates {
		acc.addAggregate(i, agg, ses)
	}

	s.resolveMissingModes(ctx, acc)
	topWeapons := s.buildTopWeapons(ctx, acc)
	companions := s.buildCompanions(ctx, ses, acc.companionObs)
	summary := acc.buildSummary(topWeapons, companions)

	clog.WithGroupFields(ctx, "sessionService.computeSummary", clog.Fields{
		"sessionId":      ses.ID,
		"totalMatches":   *summary.TotalMatches,
		"wins":           *summary.Wins,
		"losses":         *summary.Losses,
		"companionCount": len(companions),
	})
	return summary, nil
}

func (s service) UpdateSessionSummary(ctx context.Context, sessionID string, summary *api.SessionSummary) error {
	clog.WithGroupField(ctx, "sessionService.updateSummary", "sessionId", sessionID)
	return s.store.UpdateSummary(ctx, sessionID, summary, s.now())
}

type companionObservation struct {
	matchIndex     int
	characterID    string
	sessionID      string
	userID         string
	displayName    string
	characterClass string
	emblemURL      string
	won            bool
}

type companionParticipantMeta struct {
	userID         string
	displayName    string
	characterClass string
	emblemURL      string
}

type companionSessionTally struct {
	sessionID      string
	characterID    string
	characterClass string
	emblemURL      string
	sharedMatches  int
}

type companionTally struct {
	userID        string
	displayName   string
	sharedMatches int
	wins          int
	losses        int
	sessions      map[string]*companionSessionTally
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
	companionObs      []companionObservation
}

func newSummaryAccumulator(totalMatches int) *summaryAccumulator {
	return &summaryAccumulator{
		totalMatches: totalMatches,
		modes:        set.NewOrdered[string](),
		weapons:      make(map[int64]*weaponTally),
	}
}

func (a *summaryAccumulator) addAggregate(matchIndex int, agg api.Aggregate, ses api.Session) {
	a.recordMode(agg.ActivityDetails)

	perf, ok := agg.Performance[ses.CharacterID]
	if !ok {
		return
	}
	a.recordPlayerStats(perf.PlayerStats)
	a.recordWeapons(perf.Weapons)
	a.recordCompanions(matchIndex, agg, ses, perf.PlayerStats)
}

func (a *summaryAccumulator) recordCompanions(matchIndex int, agg api.Aggregate, ses api.Session, ownerStats api.PlayerStats) {
	if isPrivateActivity(agg.ActivityDetails) || len(agg.SnapshotLinks) <= 1 {
		return
	}
	ownerWon := isWin(ownerStats)
	for mapCharID, link := range agg.SnapshotLinks {
		charID := mapCharID
		if charID == "" {
			charID = link.CharacterID
		}
		if charID == "" || charID == ses.CharacterID {
			continue
		}
		if link.SessionID == nil || *link.SessionID == "" || *link.SessionID == ses.ID {
			continue
		}
		otherPerf, ok := agg.Performance[charID]
		if !ok {
			continue
		}
		if !areFireteamCompanions(ownerStats, otherPerf.PlayerStats) {
			continue
		}
		a.companionObs = append(a.companionObs, companionObservation{
			matchIndex:     matchIndex,
			characterID:    charID,
			sessionID:      *link.SessionID,
			userID:         ptr.Deref(link.UserID),
			displayName:    ptr.Deref(link.DisplayName),
			characterClass: ptr.Deref(link.CharacterClass),
			emblemURL:      ptr.Deref(link.EmblemURL),
			won:            ownerWon,
		})
	}
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

func (a *summaryAccumulator) buildSummary(topWeapons []api.SessionWeaponSummary, companions []api.SessionCompanion) *api.SessionSummary {
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
		Companions:   &companions,
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

func (s service) buildCompanions(ctx context.Context, ses api.Session, obs []companionObservation) []api.SessionCompanion {
	if len(obs) == 0 {
		return make([]api.SessionCompanion, 0)
	}

	metaByChar := make(map[string]companionParticipantMeta, len(obs))
	for _, o := range obs {
		if o.userID == "" {
			continue
		}
		meta := metaByChar[o.characterID]
		if meta.userID == "" {
			meta.userID = o.userID
		}
		if meta.displayName == "" {
			meta.displayName = o.displayName
		}
		if meta.characterClass == "" {
			meta.characterClass = o.characterClass
		}
		if meta.emblemURL == "" {
			meta.emblemURL = o.emblemURL
		}
		metaByChar[o.characterID] = meta
	}

	missingCharIDs := set.NewOrdered[string]()
	for _, o := range obs {
		if o.userID != "" {
			continue
		}
		if existing, ok := metaByChar[o.characterID]; ok && existing.userID != "" {
			continue
		}
		missingCharIDs.Add(o.characterID)
	}

	if missingCharIDs.Size() > 0 && s.userService != nil {
		if resolved, err := s.userService.FindByCharacterIDs(ctx, missingCharIDs.ToSlice()); err != nil {
			clog.WithGroupFields(ctx, "sessionService.computeSummary", clog.Fields{
				"warning":              "failed to resolve legacy companion users",
				"companionLookupError": err.Error(),
			})
		} else {
			for _, u := range resolved {
				if u.ID == "" {
					continue
				}
				for _, cid := range u.CharacterIDs {
					if cid == "" {
						continue
					}
					meta := metaByChar[cid]
					if meta.userID == "" {
						meta.userID = u.ID
					}
					if meta.displayName == "" {
						meta.displayName = u.DisplayName
					}
					metaByChar[cid] = meta
				}
				for _, ch := range u.Characters {
					if ch.Id == "" {
						continue
					}
					meta := metaByChar[ch.Id]
					if meta.userID == "" {
						meta.userID = u.ID
					}
					if meta.displayName == "" {
						meta.displayName = u.DisplayName
					}
					if meta.characterClass == "" {
						meta.characterClass = ch.Class
					}
					if meta.emblemURL == "" {
						meta.emblemURL = ch.EmblemURL
					}
					metaByChar[ch.Id] = meta
				}
			}
		}
	}

	type matchEntityKey struct {
		id         string
		matchIndex int
	}

	byUser := make(map[string]*companionTally)
	seenUserMatches := set.New[matchEntityKey]()
	seenSessionMatches := set.New[matchEntityKey]()

	for _, o := range obs {
		meta := metaByChar[o.characterID]
		userID := firstNonEmpty(o.userID, meta.userID)
		if userID == "" || (ses.UserID != "" && userID == ses.UserID) {
			continue
		}
		displayName := firstNonEmpty(o.displayName, meta.displayName)
		charClass := firstNonEmpty(o.characterClass, meta.characterClass)
		emblemURL := firstNonEmpty(o.emblemURL, meta.emblemURL)

		tally, exists := byUser[userID]
		if !exists {
			tally = &companionTally{
				userID:      userID,
				displayName: displayName,
				sessions:    make(map[string]*companionSessionTally),
			}
			byUser[userID] = tally
		} else if tally.displayName == "" && displayName != "" {
			tally.displayName = displayName
		}

		uKey := matchEntityKey{id: userID, matchIndex: o.matchIndex}
		if !seenUserMatches.Contains(uKey) {
			seenUserMatches.Add(uKey)
			tally.sharedMatches++
			if o.won {
				tally.wins++
			} else {
				tally.losses++
			}
		}

		sesTally, sesExists := tally.sessions[o.sessionID]
		if !sesExists {
			sesTally = &companionSessionTally{
				sessionID:      o.sessionID,
				characterID:    o.characterID,
				characterClass: charClass,
				emblemURL:      emblemURL,
			}
			tally.sessions[o.sessionID] = sesTally
		} else {
			if sesTally.characterClass == "" && charClass != "" {
				sesTally.characterClass = charClass
			}
			if sesTally.emblemURL == "" && emblemURL != "" {
				sesTally.emblemURL = emblemURL
			}
		}

		sKey := matchEntityKey{id: o.sessionID, matchIndex: o.matchIndex}
		if !seenSessionMatches.Contains(sKey) {
			seenSessionMatches.Add(sKey)
			sesTally.sharedMatches++
		}
	}

	companions := make([]api.SessionCompanion, 0, len(byUser))
	for _, tally := range byUser {
		sessions := make([]api.SessionCompanionSession, 0, len(tally.sessions))
		for _, cs := range tally.sessions {
			sessions = append(sessions, api.SessionCompanionSession{
				SessionID:     cs.sessionID,
				CharacterID:   cs.characterID,
				Class:         ptr.NonEmpty(cs.characterClass),
				EmblemURL:     ptr.NonEmpty(cs.emblemURL),
				SharedMatches: cs.sharedMatches,
			})
		}
		slices.SortFunc(sessions, func(a, b api.SessionCompanionSession) int {
			return cmp.Or(
				cmp.Compare(b.SharedMatches, a.SharedMatches),
				strings.Compare(a.SessionID, b.SessionID),
			)
		})
		companions = append(companions, api.SessionCompanion{
			UserID:        tally.userID,
			DisplayName:   tally.displayName,
			SharedMatches: tally.sharedMatches,
			Wins:          tally.wins,
			Losses:        tally.losses,
			Sessions:      sessions,
		})
	}

	slices.SortFunc(companions, func(a, b api.SessionCompanion) int {
		return cmp.Or(
			cmp.Compare(b.SharedMatches, a.SharedMatches),
			cmp.Compare(b.Wins, a.Wins),
			strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)),
			strings.Compare(a.UserID, b.UserID),
		)
	})
	return companions
}

func emptySessionSummary() *api.SessionSummary {
	return newSummaryAccumulator(0).buildSummary(
		make([]api.SessionWeaponSummary, 0),
		make([]api.SessionCompanion, 0),
	)
}

func isPrivateActivity(details api.ActivityHistory) bool {
	return details.IsPrivate != nil && *details.IsPrivate
}

func areFireteamCompanions(a, b api.PlayerStats) bool {
	if !sameNonZeroFireteamID(a.FireTeamID, b.FireTeamID) {
		return false
	}
	return sameTeamOrStanding(a, b)
}

func sameNonZeroFireteamID(a, b *api.StatsValuePair) bool {
	aVal, aOK := statFloat(a)
	bVal, bOK := statFloat(b)
	if !aOK || !bOK || aVal == 0 || aVal != bVal {
		return false
	}
	if a.DisplayValue != nil && *a.DisplayValue != "" &&
		b.DisplayValue != nil && *b.DisplayValue != "" &&
		*a.DisplayValue != *b.DisplayValue {
		return false
	}
	return true
}

func sameTeamOrStanding(a, b api.PlayerStats) bool {
	aTeam, aTeamOK := statFloat(a.Team)
	bTeam, bTeamOK := statFloat(b.Team)
	if aTeamOK && bTeamOK {
		if aTeam < 0 || bTeam < 0 {
			return false
		}
		return aTeam == bTeam
	}
	if (aTeamOK && aTeam < 0) || (bTeamOK && bTeam < 0) {
		return false
	}
	aStanding, aOK := statFloat(a.Standing)
	bStanding, bOK := statFloat(b.Standing)
	return aOK && bOK && aStanding == bStanding
}

func firstNonEmpty(primary, fallback string) string {
	if primary != "" {
		return primary
	}
	return fallback
}

func isWin(stats api.PlayerStats) bool {
	return stats.Standing != nil && stats.Standing.Value != nil && *stats.Standing.Value == 0
}

func statFloat(pair *api.StatsValuePair) (float64, bool) {
	if pair == nil || pair.Value == nil {
		return 0, false
	}
	return *pair.Value, true
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
