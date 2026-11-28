package fireteam

import (
	"context"
	"errors"
	"oneTrick/api"
	corefireteam "oneTrick/core/fireteam"
	"oneTrick/services/user"
	"testing"
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
