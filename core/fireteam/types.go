package fireteam

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrDestinyUnavailable = errors.New("destiny 2 servers unavailable")
)

type Color struct {
	Red   int
	Green int
	Blue  int
	Alpha int
}

type ClassStat struct {
	Name            string
	Description     string
	Icon            string
	HasIcon         bool
	StatCategory    int
	AggregationType int
	Value           int32
}

type Character struct {
	ID                  string
	Class               string
	Race                string
	CurrentTitle        string
	Light               int64
	EmblemURL           string
	EmblemBackgroundURL string
	EmblemColor         Color
	Stats               map[string]ClassStat
}

type Membership struct {
	ID          string
	Type        int64
	DisplayName string
}

type Player struct {
	ID                  string
	MemberID            string
	PrimaryMembershipID string
	DisplayName         string
	Memberships         []Membership
	Characters          []Character
}

func (p Player) PrimaryMembership() (int64, int64, error) {
	if p.PrimaryMembershipID == "" {
		return 0, 0, errors.New("user missing primary membership id")
	}
	membershipType := int64(0)
	for _, membership := range p.Memberships {
		if membership.ID == p.PrimaryMembershipID {
			membershipType = membership.Type
			break
		}
	}
	if membershipType == 0 && len(p.Memberships) > 0 {
		membershipType = p.Memberships[0].Type
	}
	if membershipType == 0 {
		return 0, 0, errors.New("user missing valid membership type")
	}
	pmID, err := strconv.ParseInt(p.PrimaryMembershipID, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse primary membership id: %w", err)
	}
	return pmID, membershipType, nil
}

type ActiveSession struct {
	SessionID   string
	UserID      string
	CharacterID string
	StartedAt   time.Time
	TrackComp   bool
}

type Member struct {
	ID             string
	MembershipID   string
	DisplayName    string
	Characters     []Character
	ActiveSessions []ActiveSession
}

type UserReader interface {
	GetPlayer(ctx context.Context, userID string) (Player, error)
	FindPlayersByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]Player, error)
}

type PartyReader interface {
	GetPartyMembershipIDs(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]string, error)
}

type ActiveSessionReader interface {
	GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]ActiveSession, error)
}
