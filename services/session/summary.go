package session

import (
	"context"
	"fmt"
	"math"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"sort"
	"strconv"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

func (s service) ComputeSessionSummary(ctx context.Context, ses api.Session) (*api.SessionSummary, error) {
	if len(ses.AggregateIDs) == 0 {
		emptyModes := make([]string, 0)
		emptyWeapons := make([]api.SessionWeaponSummary, 0)
		zero := 0
		zeroF := 0.0
		return &api.SessionSummary{
			TotalMatches: &zero,
			Wins:         &zero,
			Losses:       &zero,
			WinRate:      &zeroF,
			Kills:        &zero,
			Deaths:       &zero,
			Assists:      &zero,
			KDRatio:      &zeroF,
			KDARatio:     &zeroF,
			ModesPlayed:  &emptyModes,
			TopWeapons:   &emptyWeapons,
		}, nil
	}

	if s.aggregateService == nil {
		return nil, fmt.Errorf("aggregate service is not configured")
	}

	aggregates, err := s.aggregateService.GetAggregates(ctx, ses.AggregateIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch aggregates for session %s: %w", ses.ID, err)
	}

	totalMatches := len(aggregates)
	wins := 0
	kills := 0
	deaths := 0
	assists := 0

	modesMap := make(map[string]struct{})
	var modesOrder []string

	type weaponInfo struct {
		hash         int64
		kills        int
		fallbackName string
		fallbackIcon string
	}
	weaponKillsMap := make(map[int64]*weaponInfo)
	var missingModeActivityHashes []int64

	for _, agg := range aggregates {
		if agg.ActivityDetails.Mode != nil && *agg.ActivityDetails.Mode != "" {
			modeName := *agg.ActivityDetails.Mode
			if _, exists := modesMap[modeName]; !exists {
				modesMap[modeName] = struct{}{}
				modesOrder = append(modesOrder, modeName)
			}
		} else if agg.ActivityDetails.ActivityHash != 0 {
			missingModeActivityHashes = append(missingModeActivityHashes, agg.ActivityDetails.ActivityHash)
		} else if agg.ActivityDetails.ReferenceID != 0 {
			missingModeActivityHashes = append(missingModeActivityHashes, agg.ActivityDetails.ReferenceID)
		}

		perf, ok := agg.Performance[ses.CharacterID]
		if !ok {
			continue
		}

		if perf.PlayerStats.Standing != nil && perf.PlayerStats.Standing.Value != nil && *perf.PlayerStats.Standing.Value == 0 {
			wins++
		}
		if perf.PlayerStats.Kills != nil && perf.PlayerStats.Kills.Value != nil {
			kills += int(*perf.PlayerStats.Kills.Value)
		}
		if perf.PlayerStats.Deaths != nil && perf.PlayerStats.Deaths.Value != nil {
			deaths += int(*perf.PlayerStats.Deaths.Value)
		}
		if perf.PlayerStats.Assists != nil && perf.PlayerStats.Assists.Value != nil {
			assists += int(*perf.PlayerStats.Assists.Value)
		}

		for mapKey, weaponMetrics := range perf.Weapons {
			var refID int64
			if weaponMetrics.ReferenceID != nil && *weaponMetrics.ReferenceID != 0 {
				refID = *weaponMetrics.ReferenceID
			} else if parsed, err := strconv.ParseInt(mapKey, 10, 64); err == nil {
				refID = parsed
			}
			if refID == 0 {
				continue
			}

			wKills := 0
			if weaponMetrics.Stats != nil {
				for statKey, statVal := range *weaponMetrics.Stats {
					if statKey == "uniqueWeaponKills" || (statVal.Name != nil && *statVal.Name == "uniqueWeaponKills") {
						if statVal.Basic.Value != nil {
							wKills += int(*statVal.Basic.Value)
						}
					}
				}
			}

			info, exists := weaponKillsMap[refID]
			if !exists {
				info = &weaponInfo{hash: refID}
				weaponKillsMap[refID] = info
			}
			info.kills += wKills

			if weaponMetrics.Display != nil {
				if info.fallbackName == "" && weaponMetrics.Display.Name != "" {
					info.fallbackName = weaponMetrics.Display.Name
				}
				if info.fallbackIcon == "" && weaponMetrics.Display.Icon != nil {
					info.fallbackIcon = *weaponMetrics.Display.Icon
				}
			}
		}
	}

	if len(missingModeActivityHashes) > 0 && s.manifestService != nil {
		actDefs, err := s.manifestService.GetActivitiesByIDs(ctx, missingModeActivityHashes)
		if err == nil && len(actDefs) > 0 {
			var modeIDs []int64
			for _, def := range actDefs {
				if def.DirectActivityModeHash != 0 {
					modeIDs = append(modeIDs, int64(def.DirectActivityModeHash))
				}
				for _, mHash := range def.ActivityModeHashes {
					modeIDs = append(modeIDs, int64(mHash))
				}
			}
			if len(modeIDs) > 0 {
				modeDefs, err := s.manifestService.GetActivityModesByIDs(ctx, modeIDs)
				if err == nil {
					for _, mDef := range modeDefs {
						name := mDef.DisplayProperties.Name
						if name != "" {
							if _, exists := modesMap[name]; !exists {
								modesMap[name] = struct{}{}
								modesOrder = append(modesOrder, name)
							}
						}
					}
				}
			}
		}
	}

	losses := totalMatches - wins
	var winRate, kdRatio, kdaRatio float64
	if totalMatches > 0 {
		winRate = math.Round((float64(wins)/float64(totalMatches))*100) / 100
		denom := float64(deaths)
		if denom < 1 {
			denom = 1
		}
		kdRatio = math.Round((float64(kills)/denom)*100) / 100
		kdaRatio = math.Round(((float64(kills)+float64(assists)*0.5)/denom)*100) / 100
	}

	weaponHashes := make([]int64, 0, len(weaponKillsMap))
	for h := range weaponKillsMap {
		weaponHashes = append(weaponHashes, h)
	}

	itemDefs := make(map[string]destiny.ItemDefinition)
	if len(weaponHashes) > 0 && s.manifestService != nil {
		if fetched, err := s.manifestService.GetItemsByIDs(ctx, weaponHashes); err == nil {
			itemDefs = fetched
		}
	}

	topWeapons := make([]api.SessionWeaponSummary, 0, len(weaponKillsMap))
	for hash, info := range weaponKillsMap {
		name := info.fallbackName
		icon := info.fallbackIcon

		if def, found := itemDefs[strconv.FormatInt(hash, 10)]; found {
			if def.DisplayProperties.Name != "" {
				name = def.DisplayProperties.Name
			}
			if def.DisplayProperties.Icon != "" {
				icon = def.DisplayProperties.Icon
			}
		}

		if name == "" {
			name = "Unknown Weapon"
		}
		if icon != "" && !strings.HasPrefix(icon, "http") {
			icon = fmt.Sprintf("https://www.bungie.net%s", icon)
		}

		wName := name
		wIcon := icon
		wKills := info.kills
		topWeapons = append(topWeapons, api.SessionWeaponSummary{
			Name:  &wName,
			Icon:  &wIcon,
			Kills: &wKills,
		})
	}

	sort.Slice(topWeapons, func(i, j int) bool {
		ki, kj := *topWeapons[i].Kills, *topWeapons[j].Kills
		if ki != kj {
			return ki > kj
		}
		return *topWeapons[i].Name < *topWeapons[j].Name
	})

	if len(topWeapons) > 5 {
		topWeapons = topWeapons[:5]
	}

	if modesOrder == nil {
		modesOrder = make([]string, 0)
	}

	clog.WithGroupFields(ctx, "sessionService.computeSummary", clog.Fields{
		"sessionId":    ses.ID,
		"totalMatches": totalMatches,
		"wins":         wins,
		"losses":       losses,
	})

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
		ModesPlayed:  &modesOrder,
		TopWeapons:   &topWeapons,
	}, nil
}

func (s service) UpdateSessionSummary(ctx context.Context, sessionID string, summary *api.SessionSummary) error {
	clog.WithGroupField(ctx, "sessionService.updateSummary", "sessionId", sessionID)
	return s.store.updateSummary(ctx, sessionID, summary, s.now())
}
