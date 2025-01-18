package user

import (
	"cloud.google.com/go/firestore"
	"context"
)

type Service interface {
	GetUser(ctx context.Context, membershipID string) (*User, error)
	CreateUser(ctx context.Context, user User) (*User, error)
}
type userService struct {
	DB *firestore.Client
}

var _ Service = (*userService)(nil)

func NewUserService(client *firestore.Client) Service {
	return &userService{
		DB: client,
	}
}

func (s *userService) GetUser(ctx context.Context, membershipID string) (*User, error) {
	user := User{}
	result, err := s.DB.Collection("users").Doc(membershipID).Get(ctx)
	if err != nil {
		return nil, err
	}
	err = result.DataTo(&user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *userService) CreateUser(ctx context.Context, user User) (*User, error) {
	_, err := s.DB.Collection("users").Doc(user.ID).Set(ctx, user)
	if err != nil {
		return nil, err
	}
	return &user, nil
}
