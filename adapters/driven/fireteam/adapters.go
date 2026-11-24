// Package fireteam provides DRIVEN adapters (called by core/fireteam via its outbound ports).
//
// WARNING — TEMPORARY ANTI-CORRUPTION BRIDGE (INTENTIONAL TRANSITIONAL ANTI-PATTERN):
//
// In standard Hexagonal Architecture, driven adapters are organized by infrastructure
// (adapters/driven/firestore, adapters/driven/bungie, adapters/driven/cache) and talk
// directly to external systems rather than wrapping sibling application services.
//
// This package intentionally wraps legacy services/user and services/destiny
// (including a temporary api.Character / api.CompRankSnapshot -> core/fireteam
// double-conversion) ONLY so core/fireteam can stay strictly isolated from api.*,
// clients/bungie.*, firestore, and legacy services/* while the rest of the codebase
// migrates incrementally.
//
// Migration & Removal Checklist (delete each adapter as its backing domain migrates):
//   1. NewPartyReader & NewCompRankReader: move into adapters/driven/bungie once
//      Bungie gateway calls are extracted out of services/destiny.
//   2. NewUserReader (+ toCorePlayer / toCoreCharacter): remove once services/user moves
//      into core/user + adapters/driven/firestore returning pure domain models instead of
//      api.Character.
//   3. Delete this entire package once both backing domains have migrated.
package fireteam

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	corefireteam "oneTrick/core/fireteam"
	"oneTrick/ptr"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
)

type userReaderAdapter struct {
	svc user.Service
}

func NewUserReader(svc user.Service) corefireteam.UserReader {
	return userReaderAdapter{svc: svc}
}

func (a userReaderAdapter) GetPlayer(ctx context.Context, userID string) (corefireteam.Player, error) {
	u, err := a.svc.GetUser(ctx, userID)
	if err != nil {
		if errors.Is(err, user.NotFound) {
			return corefireteam.Player{}, fmt.Errorf("%w: %w", corefireteam.ErrUserNotFound, err)
		}
		return corefireteam.Player{}, err
	}
	if u == nil {
		return corefireteam.Player{}, corefireteam.ErrUserNotFound
	}
	return toCorePlayer(*u), nil
}

func (a userReaderAdapter) FindPlayersByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]corefireteam.Player, error) {
	users, err := a.svc.FindByPartyMembershipIDs(ctx, membershipIDs)
	if err != nil {
		return nil, err
	}
	players := make([]corefireteam.Player, 0, len(users))
	for _, u := range users {
		players = append(players, toCorePlayer(u))
	}
	return players, nil
}

type partyReaderAdapter struct {
	svc destiny.Service
}

func NewPartyReader(svc destiny.Service) corefireteam.PartyReader {
	return partyReaderAdapter{svc: svc}
}

func (a partyReaderAdapter) GetPartyMembershipIDs(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]string, error) {
	members, err := a.svc.GetPartyMembers(ctx, primaryMembershipID, membershipType)
	if err != nil {
		if errors.Is(err, destiny.ErrDestinyServerDown) {
			return nil, fmt.Errorf("%w: %w", corefireteam.ErrDestinyUnavailable, err)
		}
		return nil, err
	}
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, ptr.Deref(member.MembershipId))
	}
	return ids, nil
}

type compRankReaderAdapter struct {
	svc destiny.Service
}

func NewCompRankReader(svc destiny.Service) corefireteam.CompRankReader {
	return compRankReaderAdapter{svc: svc}
}

func (a compRankReaderAdapter) GetCompetitiveRank(
	ctx context.Context,
	primaryMembershipID int64,
	membershipType int64,
	characterID string,
) (corefireteam.CompRankSnapshot, bool, error) {
	snap, err := a.svc.GetCompetitiveRank(ctx, primaryMembershipID, membershipType, characterID)
	if err != nil {
		return corefireteam.CompRankSnapshot{}, false, err
	}
	if snap == nil {
		return corefireteam.CompRankSnapshot{}, false, nil
	}
	return corefireteam.CompRankSnapshot{
		Points:       snap.Points,
		StepIndex:    snap.StepIndex,
		DivisionName: ptr.Deref(snap.DivisionName),
		Icon:         ptr.Deref(snap.Icon),
		RecordedAt:   snap.RecordedAt,
	}, true, nil
}

func toCorePlayer(u user.User) corefireteam.Player {
	memberships := make([]corefireteam.Membership, 0, len(u.Memberships))
	for _, m := range u.Memberships {
		memberships = append(memberships, corefireteam.Membership{
			ID:          m.ID,
			Type:        m.Type,
			DisplayName: m.DisplayName,
		})
	}

	characters := make([]corefireteam.Character, 0, len(u.Characters))
	for _, c := range u.Characters {
		characters = append(characters, toCoreCharacter(c))
	}

	return corefireteam.Player{
		ID:                  u.ID,
		MemberID:            u.MemberID,
		PrimaryMembershipID: u.PrimaryMembershipID,
		DisplayName:         u.DisplayName,
		Memberships:         memberships,
		Characters:          characters,
	}
}

func toCoreCharacter(c api.Character) corefireteam.Character {
	var stats map[string]corefireteam.ClassStat
	if c.Stats != nil {
		stats = make(map[string]corefireteam.ClassStat, len(*c.Stats))
		for k, st := range *c.Stats {
			stats[k] = corefireteam.ClassStat{
				Name:            st.Name,
				Description:     st.Description,
				Icon:            st.Icon,
				HasIcon:         st.HasIcon,
				StatCategory:    st.StatCategory,
				AggregationType: st.AggregationType,
				Value:           st.Value,
			}
		}
	}

	return corefireteam.Character{
		ID:                  c.Id,
		Class:               c.Class,
		Race:                c.Race,
		CurrentTitle:        c.CurrentTitle,
		Light:               c.Light,
		EmblemURL:           c.EmblemURL,
		EmblemBackgroundURL: c.EmblemBackgroundURL,
		EmblemColor: corefireteam.Color{
			Red:   c.EmblemColor.Red,
			Green: c.EmblemColor.Green,
			Blue:  c.EmblemColor.Blue,
			Alpha: c.EmblemColor.Alpha,
		},
		Stats: stats,
	}
}
