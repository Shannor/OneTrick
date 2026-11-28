package bungie

import (
	"context"
	"encoding/json"
	"fmt"
	"oneTrick/clients/bungie"
	"oneTrick/core/activity"
	"oneTrick/core/domain"
	"oneTrick/ptr"
	"oneTrick/set"
	"strconv"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

type WeaponManifestReader interface {
	GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]domain.ItemManifest, error)
}

var _ activity.CarnageReportGateway = (*CarnageGateway)(nil)

type CarnageGateway struct {
	client   bungie.ClientWithResponsesInterface
	manifest WeaponManifestReader
}

func NewCarnageGateway(
	client bungie.ClientWithResponsesInterface,
	manifest WeaponManifestReader,
) *CarnageGateway {
	return &CarnageGateway{
		client:   client,
		manifest: manifest,
	}
}

func (g *CarnageGateway) GetPostGameCarnageReport(ctx context.Context, activityID string) (activity.CarnageReport, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return activity.CarnageReport{}, fmt.Errorf("%w: %w", domain.ErrInvalidActivityID, err)
	}

	clog.WithGroupField(ctx, "bungieCarnage.getPostGameCarnageReport", "activityId", activityID)

	resp, err := g.client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		return activity.CarnageReport{}, err
	}
	if resp == nil {
		return activity.CarnageReport{}, domain.ErrActivityNotFound
	}

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieCarnage.getPostGameCarnageReport", resp.StatusCode(), resp.Body, env); err != nil {
		return activity.CarnageReport{}, err
	}
	if resp.JSON200 == nil || resp.JSON200.PostGameCarnageReportData == nil {
		return activity.CarnageReport{}, domain.ErrActivityNotFound
	}

	data := resp.JSON200.PostGameCarnageReportData
	teams := transformTeams(data.Teams)
	if teams == nil {
		teams = []domain.Team{}
	}

	report := activity.CarnageReport{
		Teams: teams,
	}
	if data.Period != nil {
		report.Period = *data.Period
	}
	if data.ActivityDetails != nil {
		d := data.ActivityDetails
		details := activity.CarnageActivityDetails{
			InstanceID: ptr.Deref(d.InstanceId),
			ModeName:   activityModeTypeToString((*bungie.CurrentActivityModeType)(d.Mode)),
		}
		if d.ReferenceId != nil {
			details.ReferenceID = int64(*d.ReferenceId)
		}
		if d.DirectorActivityHash != nil {
			details.DirectorActivityHash = int64(*d.DirectorActivityHash)
		}
		if d.Mode != nil {
			details.Mode = int(*d.Mode)
		}
		if d.IsPrivate != nil {
			details.IsPrivate = *d.IsPrivate
			details.HasIsPrivate = true
		}
		report.ActivityDetails = details
	}

	if data.Entries != nil {
		src := *data.Entries
		report.Entries = make([]activity.CarnageEntry, 0, len(src))
		for _, entry := range src {
			report.Entries = append(report.Entries, toCarnageEntry(entry))
		}
		report.RawEntries = decodeRawPGCREntries(resp.Body, src)
	} else {
		report.Entries = make([]activity.CarnageEntry, 0)
		report.RawEntries = make([]map[string]any, 0)
	}

	clog.WithGroupField(ctx, "bungieCarnage.getPostGameCarnageReport", "teamCount", len(report.Teams))
	return report, nil
}

// decodeRawPGCREntries extracts verbatim JSON-cased entry maps from Bungie's wire body,
// falling back to JSON round-tripping typed entries when Body is absent (e.g. in unit stubs).
func decodeRawPGCREntries(body []byte, fallback []bungie.PostGameCarnageReportEntry) []map[string]any {
	if len(body) > 0 {
		var wire struct {
			Response *struct {
				Entries []map[string]any `json:"entries"`
			} `json:"Response"`
		}
		if err := json.Unmarshal(body, &wire); err == nil && wire.Response != nil && wire.Response.Entries != nil {
			return wire.Response.Entries
		}
	}
	if len(fallback) == 0 {
		return make([]map[string]any, 0)
	}
	encoded, err := json.Marshal(fallback)
	if err != nil {
		return make([]map[string]any, 0)
	}
	var out []map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil || out == nil {
		return make([]map[string]any, 0)
	}
	return out
}

func (g *CarnageGateway) GetPerformances(
	ctx context.Context,
	activityID string,
	characterIDs []string,
) (map[string]domain.Performance, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}

	clog.WithGroupFields(ctx, "bungieCarnage.getPerformances", clog.Fields{
		"activityId":     activityID,
		"characterCount": len(characterIDs),
	})

	resp, err := g.client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("nil data response")
	}

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieCarnage.getPerformances", resp.StatusCode(), resp.Body, env); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.PostGameCarnageReportData == nil {
		return nil, fmt.Errorf("nil data response")
	}

	performances := g.extractPerformances(ctx, resp.JSON200.PostGameCarnageReportData, characterIDs)
	clog.WithGroupField(ctx, "bungieCarnage.getPerformances", "returnedCount", len(performances))
	return performances, nil
}

func (g *CarnageGateway) extractPerformances(
	ctx context.Context,
	data *bungie.PostGameCarnageReportData,
	characterIDs []string,
) map[string]domain.Performance {
	if data == nil || data.Entries == nil {
		return make(map[string]domain.Performance)
	}

	var characterSet *set.Set[string]
	capHint := len(*data.Entries)
	if len(characterIDs) > 0 {
		characterSet = set.FromSlice(characterIDs)
		capHint = min(len(characterIDs), len(*data.Entries))
	}

	items := g.loadWeaponItems(ctx, data, characterSet)
	performances := make(map[string]domain.Performance, capHint)
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil {
			continue
		}
		if characterSet == nil || characterSet.Contains(*entry.CharacterId) {
			performances[*entry.CharacterId] = carnageEntryToPerformance(entry, items)
		}
	}
	return performances
}

func (g *CarnageGateway) loadWeaponItems(
	ctx context.Context,
	data *bungie.PostGameCarnageReportData,
	characterSet *set.Set[string],
) map[int64]domain.ItemManifest {
	if g.manifest == nil || data == nil || data.Entries == nil {
		return nil
	}
	var itemIDs []int64
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil || (characterSet != nil && !characterSet.Contains(*entry.CharacterId)) {
			continue
		}
		if entry.Extended != nil && entry.Extended.Weapons != nil {
			for _, w := range *entry.Extended.Weapons {
				if w.ReferenceId != nil {
					itemIDs = append(itemIDs, int64(*w.ReferenceId))
				}
			}
		}
	}
	if len(itemIDs) == 0 {
		return nil
	}
	items, err := g.manifest.GetItemsByIDs(ctx, itemIDs)
	if err != nil {
		clog.WithGroupFields(ctx, "bungieCarnage.loadWeaponItems", clog.Fields{
			"warning":    "failed to fetch weapon item definitions",
			"fetchError": err.Error(),
		})
		return nil
	}
	return items
}

func carnageEntryToPerformance(
	entry bungie.PostGameCarnageReportEntry,
	items map[int64]domain.ItemManifest,
) domain.Performance {
	var result domain.Performance
	if entry.Values != nil {
		result.PlayerStats = toDomainPlayerStats(*entry.Values)
	}
	if entry.Extended != nil {
		if entry.Extended.Values != nil {
			result.Extra = toDomainUniqueStats(*entry.Extended.Values)
		}
		if entry.Extended.Weapons != nil {
			rawWeapons := *entry.Extended.Weapons
			weapons := make(map[string]domain.WeaponMetrics, len(rawWeapons))
			for _, w := range rawWeapons {
				if w.ReferenceId == nil || *w.ReferenceId == 0 {
					continue
				}
				ref := int64(*w.ReferenceId)
				wm := domain.WeaponMetrics{
					ReferenceID: ref,
				}
				if w.Values != nil {
					wm.Stats = toDomainUniqueStats(*w.Values)
				}
				if def, ok := items[ref]; ok {
					def.IconURL = formatBungieURL(def.IconURL)
					wm.Display = def.ToWeaponDisplay()
					wm.HasDisplay = true
					wm.BucketHash = def.BucketHash
					wm.ItemTypeDisplayName = def.ItemTypeDisplayName
				}
				weapons[strconv.FormatInt(ref, 10)] = wm
			}
			result.Weapons = weapons
		}
	}
	return result
}

func toCarnageEntry(entry bungie.PostGameCarnageReportEntry) activity.CarnageEntry {
	out := activity.CarnageEntry{
		CharacterID: ptr.Deref(entry.CharacterId),
	}

	if entry.Player != nil {
		p := entry.Player
		out.CharacterClass = ptr.Deref(p.CharacterClass)
		if p.LightLevel != nil {
			out.LightLevel = *p.LightLevel
		}
		if p.EmblemHash != nil {
			out.EmblemHash = int64(*p.EmblemHash)
		}
		id := extractPlayerIdentity(p)
		out.MembershipID = id.membershipID
		out.BungieNetMembershipID = id.bungieNetMembershipID
		out.MembershipType = id.membershipType
		out.DisplayName = id.displayName
		out.DisplayNameCode = id.displayNameCode
	}

	if entry.Standing != nil {
		out.Standing = *entry.Standing
		out.HasStanding = true
	}

	if entry.Score != nil && entry.Score.Basic != nil {
		out.Score = toDomainStatPair(entry.Score.Basic)
	}

	if entry.Values != nil {
		vals := *entry.Values
		out.PlayerStats = toDomainPlayerStats(vals)
		if !out.Score.Present {
			if scoreVal, ok := vals["score"]; ok && scoreVal.Basic != nil {
				out.Score = toDomainStatPair(scoreVal.Basic)
			}
		}
		if compVal, ok := vals["completed"]; ok && compVal.Basic != nil && compVal.Basic.Value != nil {
			out.Completed = *compVal.Basic.Value >= 1
		}
	}

	if entry.Extended != nil {
		if entry.Extended.Values != nil {
			out.ExtraStats = toDomainUniqueStats(*entry.Extended.Values)
		}
		if entry.Extended.Weapons != nil {
			rawWeapons := *entry.Extended.Weapons
			weapons := make([]activity.CarnageWeapon, 0, len(rawWeapons))
			for _, w := range rawWeapons {
				if w.ReferenceId == nil || *w.ReferenceId == 0 {
					continue
				}
				cw := activity.CarnageWeapon{
					ReferenceID: int64(*w.ReferenceId),
				}
				if w.Values != nil {
					cw.Stats = toDomainUniqueStats(*w.Values)
				}
				weapons = append(weapons, cw)
			}
			out.Weapons = weapons
		}
	}

	return out
}

type playerIdentity struct {
	membershipID          string
	bungieNetMembershipID string
	membershipType        int32
	displayName           string
	displayNameCode       string
}

func extractPlayerIdentity(p *bungie.DestinyPlayer) playerIdentity {
	var id playerIdentity
	if p == nil {
		return id
	}
	destinyInfo := p.DestinyUserInfo
	bnetInfo := p.BungieNetUserInfo

	if destinyInfo != nil {
		id.membershipID = strings.TrimSpace(ptr.Deref(destinyInfo.MembershipId))
		if destinyInfo.MembershipType != nil {
			id.membershipType = *destinyInfo.MembershipType
		}
	}
	if bnetInfo != nil {
		id.bungieNetMembershipID = strings.TrimSpace(ptr.Deref(bnetInfo.MembershipId))
		if id.membershipID == "" {
			id.membershipID = id.bungieNetMembershipID
		}
		if id.membershipType == 0 && bnetInfo.MembershipType != nil {
			id.membershipType = *bnetInfo.MembershipType
		}
	}

	if name, code := extractBungieGlobalName(destinyInfo); name != "" {
		id.displayName = name
		id.displayNameCode = code
		return id
	}
	if name, code := extractBungieGlobalName(bnetInfo); name != "" {
		id.displayName = name
		id.displayNameCode = code
		return id
	}
	if destinyInfo != nil {
		if name := strings.TrimSpace(ptr.Deref(destinyInfo.DisplayName)); name != "" {
			id.displayName = name
			return id
		}
	}
	if bnetInfo != nil {
		id.displayName = strings.TrimSpace(ptr.Deref(bnetInfo.DisplayName))
	}
	return id
}

func extractBungieGlobalName(info *bungie.UserUserInfoCard) (string, string) {
	if info == nil {
		return "", ""
	}
	name := strings.TrimSpace(ptr.Deref(info.BungieGlobalDisplayName))
	if name == "" {
		return "", ""
	}
	code := ""
	if info.BungieGlobalDisplayNameCode != nil {
		code = fmt.Sprintf("%04d", *info.BungieGlobalDisplayNameCode)
	}
	return name, code
}
