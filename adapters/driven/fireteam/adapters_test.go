package fireteam

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	corefireteam "oneTrick/core/fireteam"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
	"reflect"
	"testing"
	"time"
)

type stubUserService struct {
	user.Service
	u          *user.User
	getErr     error
	partyUsers []user.User
	partyErr   error
}

func (s *stubUserService) GetUser(_ context.Context, _ string) (*user.User, error) {
	return s.u, s.getErr
}

func (s *stubUserService) FindByPartyMembershipIDs(_ context.Context, _ []string) ([]user.User, error) {
	return s.partyUsers, s.partyErr
}

type stubDestinyService struct {
	destiny.Service
	members  []bungie.PartyMember
	err      error
	compSnap *api.CompRankSnapshot
	compErr  error
}

func (s *stubDestinyService) GetPartyMembers(_ context.Context, _ int64, _ int64) ([]bungie.PartyMember, error) {
	return s.members, s.err
}

func (s *stubDestinyService) GetCompetitiveRank(_ context.Context, _ int64, _ int64, _ string) (*api.CompRankSnapshot, error) {
	return s.compSnap, s.compErr
}

func TestUserReaderAdapter(t *testing.T) {
	t.Run("maps user.NotFound to corefireteam.ErrUserNotFound", func(t *testing.T) {
		adapter := NewUserReader(&stubUserService{getErr: user.NotFound})
		_, err := adapter.GetPlayer(context.Background(), "missing")
		if !errors.Is(err, corefireteam.ErrUserNotFound) {
			t.Fatalf("expected ErrUserNotFound, got %v", err)
		}
	})

	t.Run("converts user, memberships, characters, color, and stats into pure domain types", func(t *testing.T) {
		apiStats := map[string]api.ClassStat{
			"mobility": {
				Name:            "Mobility",
				Description:     "Move speed",
				Icon:            "/icon.png",
				HasIcon:         true,
				StatCategory:    1,
				AggregationType: 2,
				Value:           100,
			},
		}
		u := &user.User{
			ID:                  "u-1",
			MemberID:            "bnet-1",
			PrimaryMembershipID: "4611686018467484863",
			DisplayName:         "Guardian",
			Memberships:         []user.Membership{{ID: "4611686018467484863", Type: 3, DisplayName: "Guardian"}},
			Characters: []api.Character{
				{
					Id:                  "char-1",
					Class:               "Hunter",
					Race:                "Exo",
					CurrentTitle:        "Flawless",
					Light:               2010,
					EmblemURL:           "/emblem.png",
					EmblemBackgroundURL: "/bg.png",
					EmblemColor:         api.Color{Red: 10, Green: 20, Blue: 30, Alpha: 255},
					Stats:               &apiStats,
				},
			},
		}

		adapter := NewUserReader(&stubUserService{u: u, partyUsers: []user.User{*u}})
		got, err := adapter.GetPlayer(context.Background(), "u-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != "u-1" || len(got.Memberships) != 1 || len(got.Characters) != 1 {
			t.Fatalf("unexpected converted player: %+v", got)
		}
		ch := got.Characters[0]
		if ch.ID != "char-1" || ch.EmblemColor != (corefireteam.Color{Red: 10, Green: 20, Blue: 30, Alpha: 255}) {
			t.Fatalf("unexpected character conversion: %+v", ch)
		}
		if st := ch.Stats["mobility"]; st.Name != "Mobility" || st.Value != 100 || !st.HasIcon {
			t.Fatalf("unexpected stat conversion: %+v", st)
		}

		batch, err := adapter.FindPlayersByPartyMembershipIDs(context.Background(), []string{"4611686018467484863"})
		if err != nil || len(batch) != 1 || batch[0].ID != "u-1" {
			t.Fatalf("unexpected batch conversion: batch=%+v err=%v", batch, err)
		}
	})
}

func TestPartyReaderAdapter(t *testing.T) {
	t.Run("maps destiny.ErrDestinyServerDown to corefireteam.ErrDestinyUnavailable", func(t *testing.T) {
		adapter := NewPartyReader(&stubDestinyService{err: destiny.ErrDestinyServerDown})
		_, err := adapter.GetPartyMembershipIDs(context.Background(), 123, 3)
		if !errors.Is(err, corefireteam.ErrDestinyUnavailable) {
			t.Fatalf("expected ErrDestinyUnavailable, got %v", err)
		}
	})

	t.Run("unwraps bungie PartyMember IDs preserving empty placeholders for nil pointers", func(t *testing.T) {
		adapter := NewPartyReader(&stubDestinyService{
			members: []bungie.PartyMember{
				{MembershipId: new("111")},
				{MembershipId: nil},
				{MembershipId: new("222")},
			},
		})
		got, err := adapter.GetPartyMembershipIDs(context.Background(), 123, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"111", "", "222"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("expected %v, got %v", want, got)
		}
	})
}

func TestCompRankReaderAdapter(t *testing.T) {
	recorded := time.Date(2026, 5, 1, 20, 0, 0, 0, time.UTC)
	step := 3
	adapter := NewCompRankReader(&stubDestinyService{
		compSnap: &api.CompRankSnapshot{
			Points:       5800,
			StepIndex:    &step,
			DivisionName: new("Platinum III"),
			Icon:         new("/icon/plat.png"),
			RecordedAt:   &recorded,
		},
	})
	got, ok, err := adapter.GetCompetitiveRank(context.Background(), 123, 3, "char-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected snapshot ok=true")
	}
	want := corefireteam.CompRankSnapshot{
		Points:       5800,
		StepIndex:    &step,
		DivisionName: "Platinum III",
		Icon:         "/icon/plat.png",
		RecordedAt:   &recorded,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}
