package user

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"fmt"
	"google.golang.org/api/iterator"
	"time"
)

type Service interface {
	GetUser(ctx context.Context, ID string) (*User, error)
	CreateUser(ctx context.Context, user *User) (*User, error)
	GetMembershipType(ctx context.Context, userID string, membershipID string) (int64, error)
}
type userService struct {
	DB *firestore.Client
}

var _ Service = (*userService)(nil)

const (
	userCollection = "users"
)

func NewUserService(client *firestore.Client) Service {
	return &userService{
		DB: client,
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

	iter := s.DB.Collection(userCollection).WhereEntity(orFilter).Documents(ctx)

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

	ref := s.DB.Collection(userCollection).NewDoc()
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
