package fireteam

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/user"
	"oneTrick/set"
	"oneTrick/utils"
	"strings"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

const defaultBungiePartyTimeout = 4 * time.Second

type Service interface {
	GetFireteam(ctx context.Context, userID string) ([]Member, error)
}

type service struct {
	users        UserReader
	party        PartyReader
	sessions     ActiveSessionReader
	partyTimeout time.Duration
}

var _ Service = (*service)(nil)

func NewService(users UserReader, party PartyReader, sessions ActiveSessionReader) Service {
	return &service{
		users:    users,
		party:    party,
		sessions: sessions,
	}
}

func (s *service) bungiePartyTimeout() time.Duration {
	if s.partyTimeout > 0 {
		return s.partyTimeout
	}
	return defaultBungiePartyTimeout
}

func (s *service) GetFireteam(ctx context.Context, userID string) ([]Member, error) {
	clog.WithGroupField(ctx, "fireteamService.getFireteam", "userId", userID)

	caller, err := s.users.GetUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user: %w", err)
	}

	pmID, membershipType, err := caller.PrimaryMembership()
	if err != nil {
		return nil, err
	}

	partyCtx, cancel := context.WithTimeout(ctx, s.bungiePartyTimeout())
	partyMembers, err := s.party.GetPartyMembers(partyCtx, pmID, membershipType)
	cancel()
	if err != nil {
		if errors.Is(err, destiny.ErrDestinyServerDown) || (ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded)) {
			return nil, fmt.Errorf("%w: %w", ErrDestinyUnavailable, err)
		}
		return nil, fmt.Errorf("failed to fetch party members: %w", err)
	}

	if len(partyMembers) == 0 {
		clog.WithGroupFields(ctx, "fireteamService.getFireteam", clog.Fields{
			"partySize":      0,
			"matchedMembers": 0,
			"activeSessions": 0,
		})
		return []Member{}, nil
	}

	usersByMembership := make(map[string]user.User, len(partyMembers)*2+2)
	indexUserByMemberships(usersByMembership, *caller)

	partyMembershipIDs := make([]string, 0, len(partyMembers))
	lookupIDs := make([]string, 0, len(partyMembers))
	missingMembershipIDs := 0
	for _, member := range partyMembers {
		if member.MembershipId == nil {
			missingMembershipIDs++
			continue
		}
		mID := strings.TrimSpace(*member.MembershipId)
		if mID == "" {
			missingMembershipIDs++
			continue
		}
		partyMembershipIDs = append(partyMembershipIDs, mID)
		if _, ok := usersByMembership[mID]; !ok {
			lookupIDs = append(lookupIDs, mID)
		}
	}

	lookupIDs = utils.UniqueTrimmedStrings(lookupIDs)
	if len(lookupIDs) > 0 {
		matchedUsers, err := s.users.FindByPartyMembershipIDs(ctx, lookupIDs)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch fireteam users: %w", err)
		}
		for _, u := range matchedUsers {
			indexUserByMemberships(usersByMembership, u)
		}
	}

	matchedUserIDs := set.NewOrdered[string]()
	matchedInOrder := make([]user.User, 0, len(partyMembershipIDs))
	for _, mID := range partyMembershipIDs {
		u, ok := usersByMembership[mID]
		if !ok || !matchedUserIDs.Add(u.ID) {
			continue
		}
		matchedInOrder = append(matchedInOrder, u)
	}

	sessionsByUser := make(map[string][]session.ActiveSession, matchedUserIDs.Size())
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
	for _, u := range matchedInOrder {
		chars := u.Characters
		if chars == nil {
			chars = []api.Character{}
		}
		active := sessionsByUser[u.ID]
		if active == nil {
			active = []session.ActiveSession{}
		}
		members = append(members, Member{
			ID:             u.ID,
			MembershipID:   u.PrimaryMembershipID,
			DisplayName:    u.DisplayName,
			Characters:     chars,
			ActiveSessions: active,
		})
	}

	clog.WithGroupFields(ctx, "fireteamService.getFireteam", clog.Fields{
		"partySize":            len(partyMembers),
		"missingMembershipIds": missingMembershipIDs,
		"matchedMembers":       len(members),
		"activeSessions":       totalActiveSessions,
	})
	return members, nil
}

func indexUserByMemberships(dst map[string]user.User, u user.User) {
	if id := strings.TrimSpace(u.PrimaryMembershipID); id != "" {
		dst[id] = u
	}
	if id := strings.TrimSpace(u.MemberID); id != "" {
		dst[id] = u
	}
	for _, m := range u.Memberships {
		if id := strings.TrimSpace(m.ID); id != "" {
			dst[id] = u
		}
	}
}
