package fireteam

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"oneTrick/set"
	"oneTrick/utils"
	"slices"
	"strings"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

const defaultBungiePartyTimeout = 4 * time.Second

type Service interface {
	GetFireteam(ctx context.Context, userID string) ([]Member, error)
	StartSession(ctx context.Context, actorUserID string, target StartSessionTarget, trackComp bool) (Session, error)
	StartSessions(ctx context.Context, actorUserID string, targets []StartSessionTarget, trackComp bool) ([]StartSessionResult, error)
}

type service struct {
	users        UserReader
	party        PartyReader
	sessions     SessionStore
	compRanks    CompRankReader
	partyTimeout time.Duration
	now          func() time.Time
}

var _ Service = (*service)(nil)

func NewService(users UserReader, party PartyReader, sessions SessionStore, compRanks CompRankReader) Service {
	return &service{
		users:     users,
		party:     party,
		sessions:  sessions,
		compRanks: compRanks,
		now:       time.Now,
	}
}

func (s *service) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *service) bungiePartyTimeout() time.Duration {
	if s.partyTimeout > 0 {
		return s.partyTimeout
	}
	return defaultBungiePartyTimeout
}

func (s *service) GetFireteam(ctx context.Context, userID string) ([]Member, error) {
	clog.WithGroupField(ctx, "fireteamService.getFireteam", "userId", userID)

	caller, err := s.users.GetPlayer(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}

	pmID, membershipType, err := caller.PrimaryMembership()
	if err != nil {
		return nil, err
	}

	rawPartyIDs, err := fetchPartyMembershipIDs(ctx, s.party, s.bungiePartyTimeout(), pmID, membershipType)
	if err != nil {
		return nil, err
	}

	if len(rawPartyIDs) == 0 {
		clog.WithGroupFields(ctx, "fireteamService.getFireteam", clog.Fields{
			"partySize":      0,
			"matchedMembers": 0,
			"activeSessions": 0,
		})
		return []Member{}, nil
	}

	playersByMembership := indexPlayersByMembership(nil, caller)

	partyMembershipIDs := make([]string, 0, len(rawPartyIDs))
	lookupIDs := make([]string, 0, len(rawPartyIDs))
	missingMembershipIDs := 0
	for _, rawID := range rawPartyIDs {
		mID := strings.TrimSpace(rawID)
		if mID == "" {
			missingMembershipIDs++
			continue
		}
		partyMembershipIDs = append(partyMembershipIDs, mID)
		if _, ok := playersByMembership[mID]; !ok {
			lookupIDs = append(lookupIDs, mID)
		}
	}

	lookupIDs = utils.UniqueTrimmedStrings(lookupIDs)
	if len(lookupIDs) > 0 {
		matchedPlayers, err := s.users.FindPlayersByPartyMembershipIDs(ctx, lookupIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch fireteam users: %w", err)
		}
		playersByMembership = indexPlayersByMembership(playersByMembership, matchedPlayers...)
	}

	matchedUserIDs := set.NewOrdered[string]()
	matchedInOrder := make([]Player, 0, len(partyMembershipIDs))
	for _, mID := range partyMembershipIDs {
		p, ok := playersByMembership[mID]
		if !ok || !matchedUserIDs.Add(p.ID) {
			continue
		}
		matchedInOrder = append(matchedInOrder, p)
	}

	sessionsByUser := make(map[string][]ActiveSession, matchedUserIDs.Size())
	totalActiveSessions := 0
	if matchedUserIDs.Size() > 0 && s.sessions != nil {
		activeSessions, err := s.sessions.GetActiveByUserIDs(ctx, matchedUserIDs.ToSlice())
		if err != nil {
			return nil, fmt.Errorf("failed to fetch active sessions for fireteam: %w", err)
		}
		totalActiveSessions = len(activeSessions)
		for _, ses := range activeSessions {
			sessionsByUser[ses.UserID] = append(sessionsByUser[ses.UserID], ses)
		}
	}

	members := make([]Member, 0, len(matchedInOrder))
	for _, p := range matchedInOrder {
		chars := p.Characters
		if chars == nil {
			chars = []Character{}
		}
		members = append(members, Member{
			ID:             p.ID,
			MembershipID:   p.PrimaryMembershipID,
			DisplayName:    p.DisplayName,
			Characters:     chars,
			ActiveSessions: sortActiveSessions(sessionsByUser[p.ID]),
		})
	}

	clog.WithGroupFields(ctx, "fireteamService.getFireteam", clog.Fields{
		"partySize":            len(rawPartyIDs),
		"missingMembershipIds": missingMembershipIDs,
		"matchedMembers":       len(members),
		"activeSessions":       totalActiveSessions,
	})
	return members, nil
}

func sortActiveSessions(sessions []ActiveSession) []ActiveSession {
	if len(sessions) == 0 {
		return []ActiveSession{}
	}
	sorted := slices.Clone(sessions)
	slices.SortFunc(sorted, func(a, b ActiveSession) int {
		if a.StartedAt.After(b.StartedAt) {
			return -1
		}
		if a.StartedAt.Before(b.StartedAt) {
			return 1
		}
		return cmp.Compare(a.SessionID, b.SessionID)
	})
	return sorted
}

func indexPlayersByMembership(base map[string]Player, players ...Player) map[string]Player {
	indexed := maps.Clone(base)
	if indexed == nil {
		indexed = make(map[string]Player, len(players)*2)
	}
	for _, p := range players {
		if id := strings.TrimSpace(p.PrimaryMembershipID); id != "" {
			indexed[id] = p
		}
		if id := strings.TrimSpace(p.MemberID); id != "" {
			indexed[id] = p
		}
		for _, m := range p.Memberships {
			if id := strings.TrimSpace(m.ID); id != "" {
				indexed[id] = p
			}
		}
	}
	return indexed
}
