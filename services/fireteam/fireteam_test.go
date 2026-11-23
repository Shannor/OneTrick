package fireteam

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/user"
	"reflect"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type fakeUserReader struct {
	usersByID        map[string]user.User
	getUserErr       error
	getUserCalls     int
	partyUsers       []user.User
	partyErr         error
	partyLookupCalls int
	capturedPartyIDs []string
}

func (f *fakeUserReader) GetUser(_ context.Context, id string) (*user.User, error) {
	f.getUserCalls++
	if f.getUserErr != nil {
		return nil, f.getUserErr
	}
	if u, ok := f.usersByID[id]; ok {
		cp := u
		return &cp, nil
	}
	return nil, user.NotFound
}

func (f *fakeUserReader) FindByPartyMembershipIDs(_ context.Context, membershipIDs []string) ([]user.User, error) {
	f.partyLookupCalls++
	f.capturedPartyIDs = append([]string(nil), membershipIDs...)
	if f.partyErr != nil {
		return nil, f.partyErr
	}
	return f.partyUsers, nil
}

type fakePartyReader struct {
	members        []bungie.PartyMember
	err            error
	fn             func(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]bungie.PartyMember, error)
	calls          int
	capturedPMID   int64
	capturedPMType int64
}

func (f *fakePartyReader) GetPartyMembers(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]bungie.PartyMember, error) {
	f.calls++
	f.capturedPMID = primaryMembershipID
	f.capturedPMType = membershipType
	if f.fn != nil {
		return f.fn(ctx, primaryMembershipID, membershipType)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.members, nil
}

type fakeActiveSessionReader struct {
	sessions        []session.ActiveSession
	err             error
	calls           int
	capturedUserIDs []string
}

func (f *fakeActiveSessionReader) GetActiveByUserIDs(_ context.Context, userIDs []string) ([]session.ActiveSession, error) {
	f.calls++
	f.capturedUserIDs = append([]string(nil), userIDs...)
	if f.err != nil {
		return nil, f.err
	}
	return f.sessions, nil
}

func TestFireteamServiceGetFireteam(t *testing.T) {
	hunter := api.Character{Id: "char-1", Class: "Hunter", Light: 2010}
	titan := api.Character{Id: "char-2", Class: "Titan", Light: 2010}
	warlock := api.Character{Id: "char-3", Class: "Warlock", Light: 2005}
	t1 := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 19, 0, 0, 0, time.UTC)

	caller := user.User{
		ID:                  "u-caller",
		MemberID:            "bnet-caller",
		PrimaryMembershipID: "4611686018467484863",
		DisplayName:         "Caller",
		Memberships: []user.Membership{
			{ID: "4611686018467484863", Type: 3},
			{ID: "4611686018467480000", Type: 1},
		},
		Characters: []api.Character{hunter},
	}

	t.Run("0 party members returns empty slice without batch user or session queries", func(t *testing.T) {
		users := &fakeUserReader{usersByID: map[string]user.User{"u-caller": caller}}
		party := &fakePartyReader{members: nil}
		sessions := &fakeActiveSessionReader{}
		svc := NewService(users, party, sessions)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("expected non-nil empty slice, got %#v", got)
		}
		if users.getUserCalls != 1 {
			t.Fatalf("expected 1 caller lookup, got %d", users.getUserCalls)
		}
		if users.partyLookupCalls != 0 {
			t.Fatalf("expected 0 party batch lookups, got %d", users.partyLookupCalls)
		}
		if sessions.calls != 0 {
			t.Fatalf("expected 0 active session lookups, got %d", sessions.calls)
		}
	})

	t.Run("1 party member (caller solo) reuses caller without redundant user batch query and hydrates active sessions", func(t *testing.T) {
		users := &fakeUserReader{usersByID: map[string]user.User{"u-caller": caller}}
		party := &fakePartyReader{
			members: []bungie.PartyMember{
				{MembershipId: new(caller.PrimaryMembershipID)},
			},
		}
		sessions := &fakeActiveSessionReader{
			sessions: []session.ActiveSession{
				{
					SessionID:   "ses-1",
					UserID:      "u-caller",
					CharacterID: "char-1",
					StartedAt:   t1,
					TrackComp:   true,
				},
			},
		}
		svc := NewService(users, party, sessions)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if party.calls != 1 || party.capturedPMID != 4611686018467484863 || party.capturedPMType != 3 {
			t.Fatalf("unexpected Bungie GetPartyMembers call: calls=%d pmID=%d type=%d", party.calls, party.capturedPMID, party.capturedPMType)
		}
		if users.getUserCalls != 1 {
			t.Fatalf("expected 1 caller lookup, got %d", users.getUserCalls)
		}
		if users.partyLookupCalls != 0 {
			t.Fatalf("expected 0 extra party user queries when only caller is in party, got %d", users.partyLookupCalls)
		}
		if sessions.calls != 1 || !reflect.DeepEqual(sessions.capturedUserIDs, []string{"u-caller"}) {
			t.Fatalf("expected 1 active session query for [u-caller], got calls=%d ids=%v", sessions.calls, sessions.capturedUserIDs)
		}
		if len(got) != 1 || got[0].ID != "u-caller" || len(got[0].ActiveSessions) != 1 {
			t.Fatalf("expected 1 active session on caller, got %+v", got)
		}
		ses := got[0].ActiveSessions[0]
		if ses.SessionID != "ses-1" || ses.CharacterID != "char-1" || !ses.StartedAt.Equal(t1) || !ses.TrackComp {
			t.Fatalf("unexpected active session payload: %+v", ses)
		}
	})

	t.Run("non-Cross-Save caller matched via secondary linked membership in User.Memberships", func(t *testing.T) {
		users := &fakeUserReader{usersByID: map[string]user.User{"u-caller": caller}}
		party := &fakePartyReader{
			members: []bungie.PartyMember{
				{MembershipId: new(" 4611686018467480000 ")},
			},
		}
		sessions := &fakeActiveSessionReader{}
		svc := NewService(users, party, sessions)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if users.partyLookupCalls != 0 {
			t.Fatalf("expected 0 party batch lookups when secondary caller membership matches locally, got %d", users.partyLookupCalls)
		}
		if len(got) != 1 || got[0].ID != "u-caller" {
			t.Fatalf("expected caller matched via linked membership, got %+v", got)
		}
	})

	t.Run("multiple party members uses 1 batched user lookup, trims whitespace, deduplicates lookupIDs, and preserves order", func(t *testing.T) {
		mate1 := user.User{
			ID:                  "u-mate1",
			MemberID:            "bnet-mate1",
			PrimaryMembershipID: "4611686018467484001",
			DisplayName:         "MateOne",
			Memberships:         []user.Membership{{ID: "4611686018467484001", Type: 3}, {ID: "alt-mate1", Type: 2}},
			Characters:          []api.Character{titan},
		}
		mate2 := user.User{
			ID:                  "u-mate2",
			MemberID:            "bnet-mate2",
			PrimaryMembershipID: "4611686018467484002",
			DisplayName:         "MateTwo",
			Characters:          nil,
		}
		mate3ByMemberID := user.User{
			ID:                  "u-mate3",
			MemberID:            "999000333",
			PrimaryMembershipID: "4611686018467484003",
			DisplayName:         "MateThree",
			Characters:          []api.Character{warlock},
		}

		users := &fakeUserReader{
			usersByID:  map[string]user.User{"u-caller": caller},
			partyUsers: []user.User{mate1, mate2, mate3ByMemberID},
		}
		party := &fakePartyReader{
			members: []bungie.PartyMember{
				{MembershipId: new(" " + caller.PrimaryMembershipID + " ")},
				{MembershipId: nil},
				{MembershipId: new("   ")},
				{MembershipId: new(" " + mate1.PrimaryMembershipID + " ")},
				{MembershipId: new("4611686018499999999")},
				{MembershipId: new(mate2.PrimaryMembershipID)},
				{MembershipId: new(mate1.PrimaryMembershipID)},
				{MembershipId: new("alt-mate1")},
				{MembershipId: new("999000333")},
			},
		}
		sessions := &fakeActiveSessionReader{
			sessions: []session.ActiveSession{
				{
					SessionID:   "ses-mate1-a",
					UserID:      "u-mate1",
					CharacterID: "char-2",
					StartedAt:   t2,
					TrackComp:   true,
				},
				{
					SessionID:   "ses-mate1-b",
					UserID:      "u-mate1",
					CharacterID: "char-2-alt",
					StartedAt:   t1,
					TrackComp:   false,
				},
				{
					SessionID:   "ses-mate3",
					UserID:      "u-mate3",
					CharacterID: "char-3",
					StartedAt:   t1,
					TrackComp:   false,
				},
			},
		}
		svc := NewService(users, party, sessions)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if users.getUserCalls != 1 {
			t.Fatalf("expected exactly 1 caller GetUser lookup (no N+1 loop), got %d", users.getUserCalls)
		}
		if users.partyLookupCalls != 1 {
			t.Fatalf("expected exactly 1 batched party user lookup, got %d", users.partyLookupCalls)
		}
		wantLookupIDs := []string{
			mate1.PrimaryMembershipID,
			"4611686018499999999",
			mate2.PrimaryMembershipID,
			"alt-mate1",
			"999000333",
		}
		if !reflect.DeepEqual(users.capturedPartyIDs, wantLookupIDs) {
			t.Fatalf("expected trimmed deduplicated lookupIDs %v, got %v", wantLookupIDs, users.capturedPartyIDs)
		}

		if sessions.calls != 1 {
			t.Fatalf("expected exactly 1 batched active session lookup, got %d", sessions.calls)
		}
		wantSessionUserIDs := []string{"u-caller", "u-mate1", "u-mate2", "u-mate3"}
		if !reflect.DeepEqual(sessions.capturedUserIDs, wantSessionUserIDs) {
			t.Fatalf("expected active session query for %v, got %v", wantSessionUserIDs, sessions.capturedUserIDs)
		}

		if len(got) != 4 {
			t.Fatalf("expected 4 matched registered members, got %d: %+v", len(got), got)
		}
		if got[0].ID != "u-caller" || got[0].ActiveSessions == nil || len(got[0].ActiveSessions) != 0 {
			t.Fatalf("expected caller with empty activeSessions slice, got %+v", got[0])
		}
		if got[1].ID != "u-mate1" || len(got[1].ActiveSessions) != 2 {
			t.Fatalf("expected mate1 with 2 active sessions, got %+v", got[1])
		}
		if got[1].ActiveSessions[0].SessionID != "ses-mate1-a" || !got[1].ActiveSessions[0].TrackComp {
			t.Fatalf("unexpected mate1 first session: %+v", got[1].ActiveSessions[0])
		}
		if got[1].ActiveSessions[1].SessionID != "ses-mate1-b" || got[1].ActiveSessions[1].TrackComp {
			t.Fatalf("unexpected mate1 second session: %+v", got[1].ActiveSessions[1])
		}
		if got[2].ID != "u-mate2" || got[2].Characters == nil || got[2].ActiveSessions == nil || len(got[2].ActiveSessions) != 0 {
			t.Fatalf("expected mate2 with empty non-nil characters and activeSessions, got %+v", got[2])
		}
		if got[3].ID != "u-mate3" || got[3].MembershipID != mate3ByMemberID.PrimaryMembershipID || len(got[3].ActiveSessions) != 1 {
			t.Fatalf("expected mate3 with 1 active session, got %+v", got[3])
		}
	})

	t.Run("unregistered-only party skips active session lookup and returns non-nil empty slice", func(t *testing.T) {
		users := &fakeUserReader{
			usersByID:  map[string]user.User{"u-caller": caller},
			partyUsers: []user.User{},
		}
		party := &fakePartyReader{
			members: []bungie.PartyMember{
				{MembershipId: new("4611686018499999998")},
				{MembershipId: new("4611686018499999999")},
			},
		}
		sessions := &fakeActiveSessionReader{}
		svc := NewService(users, party, sessions)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("expected non-nil empty slice, got %#v", got)
		}
		if users.partyLookupCalls != 1 {
			t.Fatalf("expected 1 batched party lookup, got %d", users.partyLookupCalls)
		}
		if sessions.calls != 0 {
			t.Fatalf("expected 0 active session queries when no party members are registered, got %d", sessions.calls)
		}
	})

	t.Run("propagates caller user.NotFound without calling Bungie or sessions", func(t *testing.T) {
		users := &fakeUserReader{usersByID: map[string]user.User{}}
		party := &fakePartyReader{}
		sessions := &fakeActiveSessionReader{}
		svc := NewService(users, party, sessions)

		_, err := svc.GetFireteam(clog.NewContext(context.Background()), "missing-user")
		if !errors.Is(err, user.NotFound) {
			t.Fatalf("expected error wrapping user.NotFound, got %v", err)
		}
		if party.calls != 0 {
			t.Fatalf("expected 0 Bungie calls when caller is not found, got %d", party.calls)
		}
		if sessions.calls != 0 {
			t.Fatalf("expected 0 session calls when caller is not found, got %d", sessions.calls)
		}
	})

	t.Run("maps Destiny server down and Bungie party timeout to ErrDestinyUnavailable while preserving caller cancellation", func(t *testing.T) {
		users := &fakeUserReader{usersByID: map[string]user.User{"u-caller": caller}}

		partyDown := &fakePartyReader{err: destiny.ErrDestinyServerDown}
		svcDown := NewService(users, partyDown, &fakeActiveSessionReader{})
		_, err := svcDown.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if !errors.Is(err, ErrDestinyUnavailable) || !errors.Is(err, destiny.ErrDestinyServerDown) {
			t.Fatalf("expected error matching ErrDestinyUnavailable and destiny.ErrDestinyServerDown, got %v", err)
		}

		partyTimeout := &fakePartyReader{
			fn: func(ctx context.Context, _ int64, _ int64) ([]bungie.PartyMember, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		svcTimeout := &service{
			users:        users,
			party:        partyTimeout,
			sessions:     &fakeActiveSessionReader{},
			partyTimeout: 20 * time.Millisecond,
		}
		_, err = svcTimeout.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if !errors.Is(err, ErrDestinyUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected timeout to map to ErrDestinyUnavailable wrapping DeadlineExceeded, got %v", err)
		}

		parentCtx, cancelParent := context.WithCancel(clog.NewContext(context.Background()))
		cancelParent()
		_, err = svcTimeout.GetFireteam(parentCtx, "u-caller")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected caller cancellation to propagate context.Canceled, got %v", err)
		}
		if errors.Is(err, ErrDestinyUnavailable) {
			t.Fatalf("did not expect caller cancellation to map to ErrDestinyUnavailable: %v", err)
		}
	})

	t.Run("propagates party batch lookup and active session reader errors", func(t *testing.T) {
		usersPartyErr := &fakeUserReader{
			usersByID: map[string]user.User{"u-caller": caller},
			partyErr:  errors.New("firestore batch error"),
		}
		partyWithMate := &fakePartyReader{
			members: []bungie.PartyMember{{MembershipId: new("4611686018467484001")}},
		}
		svcPartyErr := NewService(usersPartyErr, partyWithMate, &fakeActiveSessionReader{})
		if _, err := svcPartyErr.GetFireteam(clog.NewContext(context.Background()), "u-caller"); err == nil {
			t.Fatal("expected error when party batch lookup fails")
		}

		users := &fakeUserReader{usersByID: map[string]user.User{"u-caller": caller}}
		partySolo := &fakePartyReader{
			members: []bungie.PartyMember{{MembershipId: new(caller.PrimaryMembershipID)}},
		}
		sessionsErr := &fakeActiveSessionReader{err: errors.New("sessions query failed")}
		svcSesErr := NewService(users, partySolo, sessionsErr)
		if _, err := svcSesErr.GetFireteam(clog.NewContext(context.Background()), "u-caller"); err == nil {
			t.Fatal("expected error when active session lookup fails")
		}
	})
}
