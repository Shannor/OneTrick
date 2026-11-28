package activity

import (
	"context"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/user"
	"testing"
)

type stubAggregateService struct {
	aggregate.Service
	agg *api.Aggregate
	err error
}

func (s stubAggregateService) GetAggregate(_ context.Context, _ string) (*api.Aggregate, error) {
	return s.agg, s.err
}

type stubUserService struct {
	user.Service
	partyUsers []user.User
	byIDUsers  []user.User
}

func (s stubUserService) FindByPartyMembershipIDs(_ context.Context, _ []string) ([]user.User, error) {
	return s.partyUsers, nil
}

func (s stubUserService) GetByIDs(_ context.Context, _ []string) ([]user.User, error) {
	return s.byIDUsers, nil
}

func TestAggregateReader_NotFoundTranslatesToFalse(t *testing.T) {
	reader := NewAggregateReader(stubAggregateService{err: aggregate.NotFound})
	_, found, err := reader.GetByActivityID(context.Background(), "123")
	if err != nil || found {
		t.Fatalf("expected (zero, false, nil) for aggregate.NotFound, got found=%v err=%v", found, err)
	}
}

func TestUserLookup_ConvertsUsers(t *testing.T) {
	lookup := NewUserLookup(stubUserService{
		partyUsers: []user.User{{ID: "u1", DisplayName: "Alpha", Memberships: []user.Membership{{ID: "m1", Type: 3}}}},
		byIDUsers:  []user.User{{ID: "u2", DisplayName: "Bravo"}},
	})
	party, err := lookup.FindByPartyMembershipIDs(context.Background(), []string{"m1"})
	if err != nil || len(party) != 1 || party[0].ID != "u1" {
		t.Fatalf("unexpected party users: %+v, err=%v", party, err)
	}
	byID, err := lookup.GetByIDs(context.Background(), []string{"u2"})
	if err != nil || len(byID) != 1 || byID[0].ID != "u2" {
		t.Fatalf("unexpected byID users: %+v, err=%v", byID, err)
	}
}
