package session

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/set"

	clog "github.com/Shannor/canonical-logger"
)

func (s service) GetSessionAggregates(ctx context.Context, sessionID string) (SessionAggregatesResult, error) {
	clog.WithGroupField(ctx, "sessionService.getSessionAggregates", "sessionId", sessionID)

	ses, err := s.store.GetDoc(ctx, sessionID)
	if err != nil {
		return SessionAggregatesResult{}, err
	}
	if len(ses.AggregateIDs) == 0 {
		clog.WithGroupFields(ctx, "sessionService.getSessionAggregates", clog.Fields{
			"aggregateCount":   0,
			"snapshotCount":    0,
			"participantCount": 0,
		})
		return SessionAggregatesResult{
			Aggregates:   []api.Aggregate{},
			Snapshots:    map[string]api.CharacterSnapshot{},
			Participants: map[string]SessionParticipant{},
		}, nil
	}
	if s.aggregateService == nil {
		return SessionAggregatesResult{}, fmt.Errorf("aggregate service is not configured")
	}

	aggregates, err := s.aggregateService.GetAggregates(ctx, ses.AggregateIDs)
	if err != nil {
		return SessionAggregatesResult{}, err
	}

	snapshotIDs := set.NewOrdered[string]()
	companionChars := set.NewOrdered[string]()
	stampedMeta := make(map[string]companionParticipantMeta)
	companionSnapIDs := make(map[string]*set.OrderedSet[string])

	for _, agg := range aggregates {
		if ownerLink, ok := agg.SnapshotLinks[ses.CharacterID]; ok && ownerLink.SnapshotID != nil && *ownerLink.SnapshotID != "" {
			snapshotIDs.Add(*ownerLink.SnapshotID)
		}

		ownerPerf, ok := agg.Performance[ses.CharacterID]
		if !ok {
			continue
		}
		for _, cl := range verifiedCompanionLinks(agg, *ses, ownerPerf.PlayerStats) {
			companionChars.Add(cl.characterID)
			if cl.meta.userID != "" || cl.meta.displayName != "" || cl.meta.characterClass != "" || cl.meta.emblemURL != "" {
				stampedMeta[cl.characterID] = mergeParticipantMeta(stampedMeta[cl.characterID], cl.meta)
			}
			if cl.snapshotID != "" {
				snapSet, exists := companionSnapIDs[cl.characterID]
				if !exists {
					snapSet = set.NewOrdered[string]()
					companionSnapIDs[cl.characterID] = snapSet
				}
				snapSet.Add(cl.snapshotID)
			}
		}
	}

	orderedCompanions := companionChars.ToSlice()
	missingCharIDs := make([]string, 0)
	for _, charID := range orderedCompanions {
		if meta, ok := stampedMeta[charID]; !ok || meta.userID == "" {
			missingCharIDs = append(missingCharIDs, charID)
		}
	}

	metaByChar := s.resolveParticipantMetadata(ctx, stampedMeta, missingCharIDs, "sessionService.getSessionAggregates")

	participants := make(map[string]SessionParticipant, len(orderedCompanions))
	for _, charID := range orderedCompanions {
		meta := metaByChar[charID]
		if meta.userID == "" || (ses.UserID != "" && meta.userID == ses.UserID) {
			continue
		}
		participants[charID] = SessionParticipant{
			UserID:         meta.userID,
			DisplayName:    meta.displayName,
			CharacterID:    charID,
			CharacterClass: meta.characterClass,
			EmblemURL:      meta.emblemURL,
		}
		if snapSet, ok := companionSnapIDs[charID]; ok {
			for _, snapID := range snapSet.ToSlice() {
				snapshotIDs.Add(snapID)
			}
		}
	}

	uniqueSnapshotIDs := snapshotIDs.ToSlice()
	snapshotByID := make(map[string]api.CharacterSnapshot, len(uniqueSnapshotIDs))
	if len(uniqueSnapshotIDs) > 0 {
		if s.snapshotService == nil {
			return SessionAggregatesResult{}, fmt.Errorf("snapshot service is not configured")
		}
		snapshots, err := s.snapshotService.GetByIDs(ctx, uniqueSnapshotIDs)
		if err != nil {
			return SessionAggregatesResult{}, err
		}
		for _, snap := range snapshots {
			snapshotByID[snap.ID] = snap
		}
	}

	clog.WithGroupFields(ctx, "sessionService.getSessionAggregates", clog.Fields{
		"aggregateCount":   len(aggregates),
		"snapshotCount":    len(snapshotByID),
		"participantCount": len(participants),
	})

	return SessionAggregatesResult{
		Aggregates:   aggregates,
		Snapshots:    snapshotByID,
		Participants: participants,
	}, nil
}
