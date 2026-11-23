package fireteam

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/services/session"
	"oneTrick/services/user"
)

var ErrDestinyUnavailable = errors.New("destiny 2 servers unavailable")

type Member struct {
	ID             string
	MembershipID   string
	DisplayName    string
	Characters     []api.Character
	ActiveSessions []session.ActiveSession
}

type UserReader interface {
	GetUser(ctx context.Context, id string) (*user.User, error)
	FindByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]user.User, error)
}

type PartyReader interface {
	GetPartyMembers(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]bungie.PartyMember, error)
}

type ActiveSessionReader interface {
	GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]session.ActiveSession, error)
}
