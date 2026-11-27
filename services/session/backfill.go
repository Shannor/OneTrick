package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/set"
	"slices"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

func (s service) BackfillSessionSummaries(ctx context.Context, dryRun bool) (BackfillSessionSummariesResult, error) {
	res := BackfillSessionSummariesResult{DryRun: dryRun}
	clog.WithGroupField(ctx, "sessionService.backfillSessionSummaries", "dryRun", dryRun)

	if s.aggregateService == nil {
		return res, fmt.Errorf("aggregate service is not configured")
	}

	allAggs, err := s.aggregateService.GetAllAggregates(ctx)
	if err != nil {
		return res, fmt.Errorf("failed to list aggregates: %w", err)
	}

	aggsByID := make(map[string]api.Aggregate, len(allAggs))
	multiCharAggIDs := make([]string, 0)
	missingCharIDs := set.NewOrdered[string]()
	affectedSessionIDs := set.NewOrdered[string]()

	for _, agg := range allAggs {
		if agg.ID == "" {
			continue
		}
		aggsByID[agg.ID] = agg
		if len(agg.SnapshotLinks) <= 1 {
			continue
		}
		multiCharAggIDs = append(multiCharAggIDs, agg.ID)
		for mapCharID, link := range agg.SnapshotLinks {
			charID := firstNonEmpty(mapCharID, link.CharacterID)
			if charID != "" && linkNeedsParticipantBackfill(link) {
				missingCharIDs.Add(charID)
			}
			if link.SessionID != nil && *link.SessionID != "" {
				affectedSessionIDs.Add(*link.SessionID)
			}
		}
		for _, sid := range agg.SessionIds {
			if sid != "" {
				affectedSessionIDs.Add(sid)
			}
		}
	}

	slices.Sort(multiCharAggIDs)
	missingCharSlice := missingCharIDs.ToSlice()
	slices.Sort(missingCharSlice)
	sessionIDs := affectedSessionIDs.ToSlice()
	slices.Sort(sessionIDs)
	res.AggregatesScanned = len(multiCharAggIDs)

	var metaByChar map[string]companionParticipantMeta
	if len(missingCharSlice) > 0 && s.userService != nil {
		resolved, err := s.userService.FindByCharacterIDs(ctx, missingCharSlice)
		if err != nil {
			return res, fmt.Errorf("failed to resolve participant users: %w", err)
		}
		metaByChar = indexUserCharacterMeta(resolved)
	}

	if len(multiCharAggIDs) > 0 {
		_ = clog.LoopRollup(ctx, "sessionService.backfillAggregateParticipants", multiCharAggIDs, func(iterCtx context.Context, _ int, aggID string) error {
			clog.WithGroupField(iterCtx, "aggregate", "aggregateId", aggID)
			agg := aggsByID[aggID]
			patches := buildAggregateParticipantPatches(agg, metaByChar)
			if len(patches) == 0 {
				return nil
			}
			if !dryRun {
				if err := s.aggregateService.StampSnapshotLinkParticipants(iterCtx, aggID, patches); err != nil {
					res.AggregatesFailed++
					return err
				}
			}
			aggsByID[aggID] = applyParticipantPatches(agg, patches)
			res.AggregatesUpdated++
			return nil
		})
	}

	if len(sessionIDs) > 0 {
		_ = clog.LoopRollup(ctx, "sessionService.backfillSessionSummary", sessionIDs, func(iterCtx context.Context, _ int, sessionID string) error {
			clog.WithGroupField(iterCtx, "session", "sessionId", sessionID)
			ses, err := s.store.GetDoc(iterCtx, sessionID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return nil
				}
				res.SessionsScanned++
				res.SessionsFailed++
				return err
			}
			if ses.ID == "" {
				ses.ID = sessionID
			}
			res.SessionsScanned++

			sessionAggs := collectSessionAggregates(ses.AggregateIDs, aggsByID)
			summary := s.computeSessionSummaryFromAggregates(iterCtx, *ses, sessionAggs, companionResolveOptions{
				preResolvedMeta: metaByChar,
				skipUserLookup:  true,
			})
			if sessionSummariesEqual(ses.Summary, summary) {
				return nil
			}
			if !dryRun {
				if err := s.UpdateSessionSummary(iterCtx, ses.ID, summary); err != nil {
					res.SessionsFailed++
					return err
				}
			}
			res.SessionsUpdated++
			return nil
		})
	}

	clog.WithGroupFields(ctx, "sessionService.backfillSessionSummaries", clog.Fields{
		"aggregatesScanned": res.AggregatesScanned,
		"aggregatesUpdated": res.AggregatesUpdated,
		"aggregatesFailed":  res.AggregatesFailed,
		"sessionsScanned":   res.SessionsScanned,
		"sessionsUpdated":   res.SessionsUpdated,
		"sessionsFailed":    res.SessionsFailed,
	})
	return res, nil
}

func linkNeedsParticipantBackfill(link api.SnapshotLink) bool {
	return ptr.Deref(link.UserID) == "" ||
		ptr.Deref(link.DisplayName) == "" ||
		ptr.Deref(link.CharacterClass) == "" ||
		ptr.Deref(link.EmblemURL) == ""
}

func buildAggregateParticipantPatches(
	agg api.Aggregate,
	metaByChar map[string]companionParticipantMeta,
) map[string]aggregate.SnapshotLinkParticipant {
	var patches map[string]aggregate.SnapshotLinkParticipant
	for mapCharID, link := range agg.SnapshotLinks {
		key := mapCharID
		lookupCharID := firstNonEmpty(mapCharID, link.CharacterID)
		if key == "" || lookupCharID == "" {
			continue
		}
		meta, ok := metaByChar[lookupCharID]
		if !ok {
			continue
		}
		var patch aggregate.SnapshotLinkParticipant
		if ptr.Deref(link.UserID) == "" && meta.userID != "" {
			patch.UserID = meta.userID
		}
		if ptr.Deref(link.DisplayName) == "" && meta.displayName != "" {
			patch.DisplayName = meta.displayName
		}
		if ptr.Deref(link.CharacterClass) == "" && meta.characterClass != "" {
			patch.CharacterClass = meta.characterClass
		}
		if ptr.Deref(link.EmblemURL) == "" && meta.emblemURL != "" {
			patch.EmblemURL = meta.emblemURL
		}
		if patch == (aggregate.SnapshotLinkParticipant{}) {
			continue
		}
		if patches == nil {
			patches = make(map[string]aggregate.SnapshotLinkParticipant, len(agg.SnapshotLinks))
		}
		patches[key] = patch
	}
	return patches
}

func applyParticipantPatches(agg api.Aggregate, patches map[string]aggregate.SnapshotLinkParticipant) api.Aggregate {
	if len(patches) == 0 {
		return agg
	}
	updatedLinks := make(map[string]api.SnapshotLink, len(agg.SnapshotLinks))
	maps.Copy(updatedLinks, agg.SnapshotLinks)
	for charID, patch := range patches {
		link, exists := updatedLinks[charID]
		if !exists {
			continue
		}
		if patch.UserID != "" {
			link.UserID = new(patch.UserID)
		}
		if patch.DisplayName != "" {
			link.DisplayName = new(patch.DisplayName)
		}
		if patch.CharacterClass != "" {
			link.CharacterClass = new(patch.CharacterClass)
		}
		if patch.EmblemURL != "" {
			link.EmblemURL = new(patch.EmblemURL)
		}
		updatedLinks[charID] = link
	}
	agg.SnapshotLinks = updatedLinks
	return agg
}

func collectSessionAggregates(aggIDs []string, aggsByID map[string]api.Aggregate) []api.Aggregate {
	if len(aggIDs) == 0 {
		return nil
	}
	seen := set.New[string]()
	out := make([]api.Aggregate, 0, len(aggIDs))
	for _, id := range aggIDs {
		if id == "" || seen.Contains(id) {
			continue
		}
		seen.Add(id)
		if agg, ok := aggsByID[id]; ok {
			out = append(out, agg)
		}
	}
	slices.SortFunc(out, func(a, b api.Aggregate) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	return out
}

func sessionSummariesEqual(a, b *api.SessionSummary) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if ptr.Deref(a.TotalMatches) != ptr.Deref(b.TotalMatches) ||
		ptr.Deref(a.Wins) != ptr.Deref(b.Wins) ||
		ptr.Deref(a.Losses) != ptr.Deref(b.Losses) ||
		ptr.Deref(a.WinRate) != ptr.Deref(b.WinRate) ||
		ptr.Deref(a.Kills) != ptr.Deref(b.Kills) ||
		ptr.Deref(a.Deaths) != ptr.Deref(b.Deaths) ||
		ptr.Deref(a.Assists) != ptr.Deref(b.Assists) ||
		ptr.Deref(a.KDRatio) != ptr.Deref(b.KDRatio) ||
		ptr.Deref(a.KDARatio) != ptr.Deref(b.KDARatio) {
		return false
	}
	if !slices.Equal(ptr.Deref(a.ModesPlayed), ptr.Deref(b.ModesPlayed)) {
		return false
	}
	if !weaponSummariesEqual(ptr.Deref(a.TopWeapons), ptr.Deref(b.TopWeapons)) {
		return false
	}
	return sessionCompanionsEqual(ptr.Deref(a.Companions), ptr.Deref(b.Companions))
}

func weaponSummariesEqual(a, b []api.SessionWeaponSummary) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if ptr.Deref(a[i].Name) != ptr.Deref(b[i].Name) ||
			ptr.Deref(a[i].Icon) != ptr.Deref(b[i].Icon) ||
			ptr.Deref(a[i].Kills) != ptr.Deref(b[i].Kills) {
			return false
		}
	}
	return true
}

func sessionCompanionsEqual(a, b []api.SessionCompanion) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].UserID != b[i].UserID ||
			a[i].DisplayName != b[i].DisplayName ||
			a[i].SharedMatches != b[i].SharedMatches ||
			a[i].Wins != b[i].Wins ||
			a[i].Losses != b[i].Losses ||
			len(a[i].Sessions) != len(b[i].Sessions) {
			return false
		}
		for j := range a[i].Sessions {
			sa, sb := a[i].Sessions[j], b[i].Sessions[j]
			if sa.SessionID != sb.SessionID ||
				sa.CharacterID != sb.CharacterID ||
				sa.SharedMatches != sb.SharedMatches ||
				ptr.Deref(sa.Class) != ptr.Deref(sb.Class) ||
				ptr.Deref(sa.EmblemURL) != ptr.Deref(sb.EmblemURL) {
				return false
			}
		}
	}
	return true
}
