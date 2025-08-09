package user

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/iterator"
)

type Service interface {
	GetUser(ctx context.Context, ID string) (*User, error)
	CreateUser(ctx context.Context, user *User) (*User, error)
	GetMembershipType(ctx context.Context, userID string, membershipID string) (int64, error)
	GetFireteam(ctx context.Context, userID string) ([]api.FireteamMember, error)
}
type userService struct {
	db        *firestore.Client
	d2Service destiny.Service
}

var _ Service = (*userService)(nil)

const (
	userCollection = "users"
)

func NewUserService(client *firestore.Client, service destiny.Service) Service {
	return &userService{
		db:        client,
		d2Service: service,
	}
}

var NotFound = errors.New("user not found")

func (s *userService) GetUser(ctx context.Context, ID string) (*User, error) {
	user := User{}

	q1 := firestore.PropertyFilter{
		Path:     "id",
		Operator: "==",
		Value:    ID,
	}

	q2 := firestore.PropertyFilter{
		Path:     "memberId",
		Operator: "==",
		Value:    ID,
	}
	q3 := firestore.PropertyFilter{
		Path:     "primaryMembershipId",
		Operator: "==",
		Value:    ID,
	}
	orFilter := firestore.OrFilter{
		Filters: []firestore.EntityFilter{q1, q2, q3},
	}

	iter := s.db.Collection(userCollection).WhereEntity(orFilter).Documents(ctx)

	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		err = doc.DataTo(&user)
		if err != nil {
			return nil, err
		}
		return &user, nil
	}

	return nil, NotFound
}

func (s *userService) CreateUser(ctx context.Context, user *User) (*User, error) {
	if user == nil {
		return nil, errors.New("user is nil")
	}

	now := time.Now()
	user.CreatedAt = now

	ref := s.db.Collection(userCollection).NewDoc()
	user.ID = ref.ID

	_, err := ref.Set(ctx, user)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *userService) GetMembershipType(ctx context.Context, userID string, membershipID string) (int64, error) {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch user: %w", err)
	}
	membershipType := int64(0)
	for _, membership := range u.Memberships {
		if membership.ID == membershipID {
			membershipType = membership.Type
		}
	}
	return membershipType, nil
}

func (s *userService) GetFireteam(ctx context.Context, userID string) ([]api.FireteamMember, error) {
	u, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	t := int64(0)
	for _, membership := range u.Memberships {
		if membership.ID == u.PrimaryMembershipID {
			t = membership.Type
			break
		}
	}
	pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse primary membership id")
	}
	partyMembers, err := s.d2Service.GetPartyMembers(ctx, pmId, t)
	if err != nil {
		if errors.Is(err, destiny.ErrDestinyServerDown) {
			return nil, fmt.Errorf("destiny 3rd party service is down")
		}
		return nil, fmt.Errorf("failed to fetch characters: %w", err)
	}

	fireteam := make([]api.FireteamMember, 0)
	for _, member := range partyMembers {
		if member.MembershipId == nil {
			log.Warn().Msg("missing membership id for party member")
			continue
		}
		member, err := s.GetUser(ctx, *member.MembershipId)
		if err != nil {
			// TODO: Add a case here for telling people to join one trick
			continue
		}
		fireteam = append(fireteam, api.FireteamMember{
			DisplayName:  member.DisplayName,
			ID:           member.ID,
			MembershipID: member.PrimaryMembershipID,
		})
	}

	return fireteam, nil
}
