package fireteam

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type fakeUserReader struct {
	playersByID      map[string]Player
	getPlayerErr     error
	getPlayerCalls   int
	partyPlayers     []Player
	partyErr         error
	partyLookupCalls int
	capturedPartyIDs []string
}

func (f *fakeUserReader) GetPlayer(_ context.Context, id string) (Player, error) {
	f.getPlayerCalls++
	if f.getPlayerErr != nil {
		return Player{}, f.getPlayerErr
	}
	if p, ok := f.playersByID[id]; ok {
		return p, nil
	}
	return Player{}, ErrUserNotFound
}

func (f *fakeUserReader) FindPlayersByPartyMembershipIDs(_ context.Context, membershipIDs []string) ([]Player, error) {
	f.partyLookupCalls++
	f.capturedPartyIDs = append([]string(nil), membershipIDs...)
	if f.partyErr != nil {
		return nil, f.partyErr
	}
	return f.partyPlayers, nil
}

type fakePartyReader struct {
	membershipIDs  []string
	err            error
	fn             func(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]string, error)
	calls          int
	capturedPMID   int64
	capturedPMType int64
}

func (f *fakePartyReader) GetPartyMembershipIDs(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]string, error) {
	f.calls++
	f.capturedPMID = primaryMembershipID
	f.capturedPMType = membershipType
	if f.fn != nil {
		return f.fn(ctx, primaryMembershipID, membershipType)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.membershipIDs, nil
}

type fakeSessionStore struct {
	activeSummaries    []ActiveSession
	activeSummaryErr   error
	activeSummaryCalls int
	capturedSummaryIDs []string

	activeDocs      []Session
	activeDocsErr   error
	activeDocsCalls int
	capturedDocsIDs []string

	createErr   error
	createFn    func(ctx context.Context, sessions []Session) ([]CreateSessionResult, error)
	createCalls int
	createdDocs []Session
	nextID      int
}

func (f *fakeSessionStore) GetActiveByUserIDs(_ context.Context, userIDs []string) ([]ActiveSession, error) {
	f.activeSummaryCalls++
	f.capturedSummaryIDs = append([]string(nil), userIDs...)
	if f.activeSummaryErr != nil {
		return nil, f.activeSummaryErr
	}
	return f.activeSummaries, nil
}

func (f *fakeSessionStore) GetActiveSessionsByUserIDs(_ context.Context, userIDs []string) ([]Session, error) {
	f.activeDocsCalls++
	f.capturedDocsIDs = append([]string(nil), userIDs...)
	if f.activeDocsErr != nil {
		return nil, f.activeDocsErr
	}
	out := make([]Session, 0, len(f.activeDocs))
	allowed := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		allowed[id] = struct{}{}
	}
	for _, doc := range f.activeDocs {
		if _, ok := allowed[doc.UserID]; ok {
			out = append(out, doc)
		}
	}
	return out, nil
}

func (f *fakeSessionStore) CreateSessionsIfInactive(ctx context.Context, sessions []Session) ([]CreateSessionResult, error) {
	f.createCalls++
	if f.createFn != nil {
		return f.createFn(ctx, sessions)
	}
	if f.createErr != nil {
		return nil, f.createErr
	}
	results := make([]CreateSessionResult, len(sessions))
	for i, ses := range sessions {
		f.nextID++
		ses.ID = fmt.Sprintf("ses-new-%d", f.nextID)
		results[i] = CreateSessionResult{Session: ses, Created: true}
		f.createdDocs = append(f.createdDocs, ses)
	}
	return results, nil
}

type fakeCompRankReader struct {
	mu    sync.Mutex
	calls int
	fn    func(ctx context.Context, primaryMembershipID int64, membershipType int64, characterID string) (CompRankSnapshot, bool, error)
}

func (f *fakeCompRankReader) GetCompetitiveRank(ctx context.Context, primaryMembershipID int64, membershipType int64, characterID string) (CompRankSnapshot, bool, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(ctx, primaryMembershipID, membershipType, characterID)
	}
	return CompRankSnapshot{Points: 5000, DivisionName: "Platinum II"}, true, nil
}

func TestIndexPlayersByMembershipDoesNotMutateInput(t *testing.T) {
	p1 := Player{ID: "u-1", PrimaryMembershipID: "111", MemberID: "m-1"}
	p2 := Player{
		ID:                  "u-2",
		PrimaryMembershipID: "222",
		Memberships:         []Membership{{ID: "alt-222", Type: 2}},
	}

	base := indexPlayersByMembership(nil, p1)
	merged := indexPlayersByMembership(base, p2)

	if len(base) != 2 {
		t.Fatalf("expected original map untouched with 2 keys, got %d: %+v", len(base), base)
	}
	if _, exists := base["222"]; exists {
		t.Fatalf("expected original map not to be mutated with p2 key")
	}
	if len(merged) != 4 || merged["111"].ID != "u-1" || merged["222"].ID != "u-2" || merged["alt-222"].ID != "u-2" {
		t.Fatalf("unexpected merged index: %+v", merged)
	}
}

func TestFireteamServiceGetFireteam(t *testing.T) {
	hunter := Character{ID: "char-1", Class: "Hunter", Light: 2010}
	titan := Character{ID: "char-2", Class: "Titan", Light: 2010}
	warlock := Character{ID: "char-3", Class: "Warlock", Light: 2005}
	t1 := time.Date(2026, 5, 1, 18, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 19, 0, 0, 0, time.UTC)

	caller := Player{
		ID:                  "u-caller",
		MemberID:            "bnet-caller",
		PrimaryMembershipID: "4611686018467484863",
		DisplayName:         "Caller",
		Memberships: []Membership{
			{ID: "4611686018467484863", Type: 3},
			{ID: "4611686018467480000", Type: 1},
		},
		Characters: []Character{hunter},
	}

	t.Run("0 party members returns empty slice without batch user or session queries", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		party := &fakePartyReader{membershipIDs: nil}
		sessions := &fakeSessionStore{}
		svc := NewService(users, party, sessions, nil)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("expected non-nil empty slice, got %#v", got)
		}
		if users.getPlayerCalls != 1 {
			t.Fatalf("expected 1 caller lookup, got %d", users.getPlayerCalls)
		}
		if users.partyLookupCalls != 0 {
			t.Fatalf("expected 0 party batch lookups, got %d", users.partyLookupCalls)
		}
		if sessions.activeSummaryCalls != 0 {
			t.Fatalf("expected 0 active session lookups, got %d", sessions.activeSummaryCalls)
		}
	})

	t.Run("1 party member (caller solo) reuses caller without redundant user batch query and hydrates active sessions", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		party := &fakePartyReader{
			membershipIDs: []string{caller.PrimaryMembershipID},
		}
		sessions := &fakeSessionStore{
			activeSummaries: []ActiveSession{
				{
					SessionID:   "ses-1",
					UserID:      "u-caller",
					CharacterID: "char-1",
					StartedAt:   t1,
					TrackComp:   true,
				},
			},
		}
		svc := NewService(users, party, sessions, nil)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if party.calls != 1 || party.capturedPMID != 4611686018467484863 || party.capturedPMType != 3 {
			t.Fatalf("unexpected Bungie GetPartyMembershipIDs call: calls=%d pmID=%d type=%d", party.calls, party.capturedPMID, party.capturedPMType)
		}
		if users.getPlayerCalls != 1 {
			t.Fatalf("expected 1 caller lookup, got %d", users.getPlayerCalls)
		}
		if users.partyLookupCalls != 0 {
			t.Fatalf("expected 0 extra party user queries when only caller is in party, got %d", users.partyLookupCalls)
		}
		if sessions.activeSummaryCalls != 1 || !reflect.DeepEqual(sessions.capturedSummaryIDs, []string{"u-caller"}) {
			t.Fatalf("expected 1 active session query for [u-caller], got calls=%d ids=%v", sessions.activeSummaryCalls, sessions.capturedSummaryIDs)
		}
		if len(got) != 1 || got[0].ID != "u-caller" || len(got[0].ActiveSessions) != 1 {
			t.Fatalf("expected 1 active session on caller, got %+v", got)
		}
		ses := got[0].ActiveSessions[0]
		if ses.SessionID != "ses-1" || ses.CharacterID != "char-1" || !ses.StartedAt.Equal(t1) || !ses.TrackComp {
			t.Fatalf("unexpected active session payload: %+v", ses)
		}
	})

	t.Run("non-Cross-Save caller matched via secondary linked membership in Player.Memberships", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		party := &fakePartyReader{
			membershipIDs: []string{" 4611686018467480000 "},
		}
		sessions := &fakeSessionStore{}
		svc := NewService(users, party, sessions, nil)

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
		mate1 := Player{
			ID:                  "u-mate1",
			MemberID:            "bnet-mate1",
			PrimaryMembershipID: "4611686018467484001",
			DisplayName:         "MateOne",
			Memberships:         []Membership{{ID: "4611686018467484001", Type: 3}, {ID: "alt-mate1", Type: 2}},
			Characters:          []Character{titan},
		}
		mate2 := Player{
			ID:                  "u-mate2",
			MemberID:            "bnet-mate2",
			PrimaryMembershipID: "4611686018467484002",
			DisplayName:         "MateTwo",
			Characters:          nil,
		}
		mate3ByMemberID := Player{
			ID:                  "u-mate3",
			MemberID:            "999000333",
			PrimaryMembershipID: "4611686018467484003",
			DisplayName:         "MateThree",
			Characters:          []Character{warlock},
		}

		users := &fakeUserReader{
			playersByID:  map[string]Player{"u-caller": caller},
			partyPlayers: []Player{mate1, mate2, mate3ByMemberID},
		}
		party := &fakePartyReader{
			membershipIDs: []string{
				" " + caller.PrimaryMembershipID + " ",
				"",
				"   ",
				" " + mate1.PrimaryMembershipID + " ",
				"4611686018499999999",
				mate2.PrimaryMembershipID,
				mate1.PrimaryMembershipID,
				"alt-mate1",
				"999000333",
			},
		}
		sessions := &fakeSessionStore{
			activeSummaries: []ActiveSession{
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
		svc := NewService(users, party, sessions, nil)

		got, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if users.getPlayerCalls != 1 {
			t.Fatalf("expected exactly 1 caller lookup (no N+1 loop), got %d", users.getPlayerCalls)
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

		if sessions.activeSummaryCalls != 1 {
			t.Fatalf("expected exactly 1 batched active session lookup, got %d", sessions.activeSummaryCalls)
		}
		wantSessionUserIDs := []string{"u-caller", "u-mate1", "u-mate2", "u-mate3"}
		if !reflect.DeepEqual(sessions.capturedSummaryIDs, wantSessionUserIDs) {
			t.Fatalf("expected active session query for %v, got %v", wantSessionUserIDs, sessions.capturedSummaryIDs)
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
			playersByID:  map[string]Player{"u-caller": caller},
			partyPlayers: []Player{},
		}
		party := &fakePartyReader{
			membershipIDs: []string{"4611686018499999998", "4611686018499999999"},
		}
		sessions := &fakeSessionStore{}
		svc := NewService(users, party, sessions, nil)

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
		if sessions.activeSummaryCalls != 0 {
			t.Fatalf("expected 0 active session queries when no party members are registered, got %d", sessions.activeSummaryCalls)
		}
	})

	t.Run("propagates caller ErrUserNotFound without calling Bungie or sessions", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{}}
		party := &fakePartyReader{}
		sessions := &fakeSessionStore{}
		svc := NewService(users, party, sessions, nil)

		_, err := svc.GetFireteam(clog.NewContext(context.Background()), "missing-user")
		if !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("expected error wrapping ErrUserNotFound, got %v", err)
		}
		if party.calls != 0 {
			t.Fatalf("expected 0 Bungie calls when caller is not found, got %d", party.calls)
		}
		if sessions.activeSummaryCalls != 0 {
			t.Fatalf("expected 0 session calls when caller is not found, got %d", sessions.activeSummaryCalls)
		}
	})

	t.Run("maps Destiny unavailable and Bungie party timeout to ErrDestinyUnavailable while preserving caller cancellation", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}

		partyDown := &fakePartyReader{err: ErrDestinyUnavailable}
		svcDown := NewService(users, partyDown, &fakeSessionStore{}, nil)
		_, err := svcDown.GetFireteam(clog.NewContext(context.Background()), "u-caller")
		if !errors.Is(err, ErrDestinyUnavailable) {
			t.Fatalf("expected ErrDestinyUnavailable, got %v", err)
		}

		partyTimeout := &fakePartyReader{
			fn: func(ctx context.Context, _ int64, _ int64) ([]string, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		svcTimeout := &service{
			users:        users,
			party:        partyTimeout,
			sessions:     &fakeSessionStore{},
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
			playersByID: map[string]Player{"u-caller": caller},
			partyErr:    errors.New("firestore batch error"),
		}
		partyWithMate := &fakePartyReader{
			membershipIDs: []string{"4611686018467484001"},
		}
		svcPartyErr := NewService(usersPartyErr, partyWithMate, &fakeSessionStore{}, nil)
		if _, err := svcPartyErr.GetFireteam(clog.NewContext(context.Background()), "u-caller"); err == nil {
			t.Fatal("expected error when party batch lookup fails")
		}

		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		partySolo := &fakePartyReader{
			membershipIDs: []string{caller.PrimaryMembershipID},
		}
		sessionsErr := &fakeSessionStore{activeSummaryErr: errors.New("sessions query failed")}
		svcSesErr := NewService(users, partySolo, sessionsErr, nil)
		if _, err := svcSesErr.GetFireteam(clog.NewContext(context.Background()), "u-caller"); err == nil {
			t.Fatal("expected error when active session lookup fails")
		}
	})
}

func TestFireteamServiceStartSession(t *testing.T) {
	fixedNow := time.Date(2026, 5, 2, 18, 0, 0, 0, time.UTC)
	caller := Player{
		ID:                  "u-caller",
		MemberID:            "bnet-caller",
		PrimaryMembershipID: "4611686018467484863",
		DisplayName:         "Caller",
		Memberships:         []Membership{{ID: "4611686018467484863", Type: 3}},
	}
	mate1 := Player{
		ID:                  "u-mate1",
		MemberID:            "bnet-mate1",
		PrimaryMembershipID: "4611686018467484001",
		DisplayName:         "MateOne",
		Memberships:         []Membership{{ID: "4611686018467484001", Type: 3}},
	}

	t.Run("self-start succeeds offline with 0 Bungie party calls", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		party := &fakePartyReader{err: errors.New("should not be called for self")}
		store := &fakeSessionStore{}
		svc := &service{users: users, party: party, sessions: store, now: func() time.Time { return fixedNow }}

		created, err := svc.StartSession(clog.NewContext(context.Background()), "u-caller", StartSessionTarget{UserID: "u-caller", CharacterID: "char-1"}, false)
		if err != nil {
			t.Fatalf("expected self-start to succeed offline, got %v", err)
		}
		if party.calls != 0 {
			t.Fatalf("expected 0 Bungie party calls for self-start, got %d", party.calls)
		}
		if created.UserID != "u-caller" || created.CharacterID != "char-1" || created.Status != SessionStatusPending {
			t.Fatalf("unexpected created session: %+v", created)
		}
		if created.StartedBy == nil || created.StartedBy.ID != "u-caller" || created.StartedBy.Username != "Caller" {
			t.Fatalf("expected StartedBy populated from caller, got %+v", created.StartedBy)
		}
	})

	t.Run("non-self target not in Bungie party returns ErrNotInFireteam without creating session", func(t *testing.T) {
		users := &fakeUserReader{
			playersByID:  map[string]Player{"u-caller": caller},
			partyPlayers: []Player{mate1},
		}
		party := &fakePartyReader{membershipIDs: []string{caller.PrimaryMembershipID, mate1.PrimaryMembershipID}}
		store := &fakeSessionStore{}
		svc := NewService(users, party, store, nil)

		_, err := svc.StartSession(clog.NewContext(context.Background()), "u-caller", StartSessionTarget{UserID: "u-stranger", CharacterID: "char-9"}, false)
		if !errors.Is(err, ErrNotInFireteam) {
			t.Fatalf("expected ErrNotInFireteam, got %v", err)
		}
		if party.calls != 1 {
			t.Fatalf("expected 1 Bungie party call, got %d", party.calls)
		}
		if store.activeDocsCalls != 0 || store.createCalls != 0 {
			t.Fatalf("expected 0 store calls on unauthorized target, got active=%d create=%d", store.activeDocsCalls, store.createCalls)
		}
	})

	t.Run("non-self fireteam member succeeds with StartedBy caller and trackComp", func(t *testing.T) {
		users := &fakeUserReader{
			playersByID:  map[string]Player{"u-caller": caller},
			partyPlayers: []Player{mate1},
		}
		party := &fakePartyReader{membershipIDs: []string{caller.PrimaryMembershipID, mate1.PrimaryMembershipID}}
		store := &fakeSessionStore{}
		comp := &fakeCompRankReader{
			fn: func(_ context.Context, pmID int64, pmType int64, charID string) (CompRankSnapshot, bool, error) {
				if pmID != 4611686018467484001 || pmType != 3 || charID != "char-2" {
					t.Errorf("unexpected comp rank lookup args: %d %d %s", pmID, pmType, charID)
				}
				return CompRankSnapshot{Points: 6100, DivisionName: "Adept III"}, true, nil
			},
		}
		svc := NewService(users, party, store, comp)

		created, err := svc.StartSession(clog.NewContext(context.Background()), "u-caller", StartSessionTarget{UserID: "u-mate1", CharacterID: "char-2"}, true)
		if err != nil {
			t.Fatalf("expected fireteam start to succeed, got %v", err)
		}
		if party.calls != 1 {
			t.Fatalf("expected 1 Bungie party call, got %d", party.calls)
		}
		if created.StartedBy == nil || created.StartedBy.ID != "u-caller" || created.StartedBy.Username != "Caller" {
			t.Fatalf("expected StartedBy caller, got %+v", created.StartedBy)
		}
		if created.CompTracking == nil || !created.CompTracking.Enabled || created.CompTracking.Start == nil || created.CompTracking.Start.Points != 6100 {
			t.Fatalf("expected starting comp rank attached, got %+v", created.CompTracking)
		}
	})

	t.Run("returns ErrAlreadyActive when matching character already has a pending session", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		party := &fakePartyReader{}
		store := &fakeSessionStore{
			activeDocs: []Session{
				{ID: "ses-existing", UserID: "u-caller", CharacterID: "char-1", Status: SessionStatusPending},
			},
		}
		svc := NewService(users, party, store, nil)

		_, err := svc.StartSession(clog.NewContext(context.Background()), "u-caller", StartSessionTarget{UserID: "u-caller", CharacterID: "char-1"}, false)
		if !errors.Is(err, ErrAlreadyActive) {
			t.Fatalf("expected ErrAlreadyActive, got %v", err)
		}
		if store.createCalls != 0 {
			t.Fatalf("expected 0 CreateSessions calls when already active, got %d", store.createCalls)
		}
	})
}

func TestFireteamServiceStartSessions(t *testing.T) {
	fixedNow := time.Date(2026, 5, 2, 18, 0, 0, 0, time.UTC)
	caller := Player{
		ID:                  "u-caller",
		MemberID:            "bnet-caller",
		PrimaryMembershipID: "4611686018467484863",
		DisplayName:         "Caller",
		Memberships:         []Membership{{ID: "4611686018467484863", Type: 3}},
	}
	mate1 := Player{
		ID:                  "u-mate1",
		MemberID:            "bnet-mate1",
		PrimaryMembershipID: "4611686018467484001",
		DisplayName:         "MateOne",
		Memberships:         []Membership{{ID: "4611686018467484001", Type: 3}},
	}
	mate2 := Player{
		ID:                  "u-mate2",
		MemberID:            "bnet-mate2",
		PrimaryMembershipID: "4611686018467484002",
		DisplayName:         "MateTwo",
		Memberships:         []Membership{{ID: "4611686018467484002", Type: 2}},
	}

	t.Run("3 members with 1 already active creates 2 with StartedBy caller and returns 1 skipped_active in 1 party call and 1 batched store read/write", func(t *testing.T) {
		existing := Session{
			ID:           "ses-existing-mate1",
			UserID:       "u-mate1",
			CharacterID:  "char-2",
			Status:       SessionStatusPending,
			StartedAt:    fixedNow.Add(-time.Hour),
			AggregateIDs: []string{"agg-1"},
		}
		users := &fakeUserReader{
			playersByID:  map[string]Player{"u-caller": caller},
			partyPlayers: []Player{mate1, mate2},
		}
		party := &fakePartyReader{
			membershipIDs: []string{caller.PrimaryMembershipID, mate1.PrimaryMembershipID, mate2.PrimaryMembershipID},
		}
		store := &fakeSessionStore{activeDocs: []Session{existing}}
		svc := &service{
			users:    users,
			party:    party,
			sessions: store,
			now:      func() time.Time { return fixedNow },
		}

		results, err := svc.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{
				{UserID: "u-caller", CharacterID: "char-1"},
				{UserID: "u-mate1", CharacterID: "char-2"},
				{UserID: "u-mate2", CharacterID: "char-3"},
				{UserID: "u-mate2", CharacterID: "char-3"},
			},
			false,
		)
		if err != nil {
			t.Fatalf("StartSessions failed: %v", err)
		}
		if party.calls != 1 {
			t.Fatalf("expected 1 Bungie party call, got %d", party.calls)
		}
		if users.partyLookupCalls != 1 {
			t.Fatalf("expected 1 batched user lookup, got %d", users.partyLookupCalls)
		}
		if store.activeDocsCalls != 1 {
			t.Fatalf("expected 1 batched active-session query, got %d", store.activeDocsCalls)
		}
		if store.createCalls != 1 || len(store.createdDocs) != 2 {
			t.Fatalf("expected 1 batched create for 2 sessions, got calls=%d count=%d", store.createCalls, len(store.createdDocs))
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 deduplicated results, got %d", len(results))
		}
		if results[0].Outcome != StartSessionOutcomeStarted || results[0].UserID != "u-caller" || results[0].Session.StartedBy == nil || results[0].Session.StartedBy.ID != "u-caller" {
			t.Fatalf("unexpected first result: %+v", results[0])
		}
		if results[1].Outcome != StartSessionOutcomeSkippedActive || results[1].Session.ID != "ses-existing-mate1" {
			t.Fatalf("unexpected second result: %+v", results[1])
		}
		if results[2].Outcome != StartSessionOutcomeStarted || results[2].UserID != "u-mate2" || results[2].Session.StartedBy == nil || results[2].Session.StartedBy.Username != "Caller" {
			t.Fatalf("unexpected third result: %+v", results[2])
		}
	})

	t.Run("self-only bulk start works offline with 0 Bungie party calls", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		party := &fakePartyReader{err: errors.New("should not call Bungie")}
		store := &fakeSessionStore{}
		svc := NewService(users, party, store, nil)

		results, err := svc.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{{UserID: "u-caller", CharacterID: "char-1"}},
			false,
		)
		if err != nil {
			t.Fatalf("expected self-only StartSessions to succeed offline, got %v", err)
		}
		if party.calls != 0 {
			t.Fatalf("expected 0 Bungie calls, got %d", party.calls)
		}
		if len(results) != 1 || results[0].Outcome != StartSessionOutcomeStarted {
			t.Fatalf("unexpected results: %+v", results)
		}
	})

	t.Run("trackComp fetches starting comp ranks concurrently for newly-created sessions only and tolerates partial failure", func(t *testing.T) {
		existing := Session{
			ID:          "ses-existing-mate1",
			UserID:      "u-mate1",
			CharacterID: "char-2",
			Status:      SessionStatusPending,
			StartedAt:   fixedNow.Add(-time.Hour),
		}
		users := &fakeUserReader{
			playersByID:  map[string]Player{"u-caller": caller},
			partyPlayers: []Player{mate1, mate2},
		}
		party := &fakePartyReader{
			membershipIDs: []string{caller.PrimaryMembershipID, mate1.PrimaryMembershipID, mate2.PrimaryMembershipID},
		}
		store := &fakeSessionStore{activeDocs: []Session{existing}}

		entered := make(chan string, 2)
		release := make(chan struct{})
		comp := &fakeCompRankReader{
			fn: func(_ context.Context, pmID int64, _ int64, charID string) (CompRankSnapshot, bool, error) {
				if charID == "char-2" {
					t.Errorf("comp rank must not be fetched for skipped_active member")
				}
				entered <- charID
				<-release
				if pmID == 4611686018467484002 {
					return CompRankSnapshot{}, false, errors.New("bungie hiccup")
				}
				return CompRankSnapshot{Points: 5820, DivisionName: "Platinum III"}, true, nil
			},
		}

		svc := NewService(users, party, store, comp)
		done := make(chan struct{})
		var (
			results []StartSessionResult
			err     error
		)
		go func() {
			defer close(done)
			results, err = svc.StartSessions(
				clog.NewContext(context.Background()),
				"u-caller",
				[]StartSessionTarget{
					{UserID: "u-caller", CharacterID: "char-1"},
					{UserID: "u-mate1", CharacterID: "char-2"},
					{UserID: "u-mate2", CharacterID: "char-3"},
				},
				true,
			)
		}()

		for range 2 {
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("expected starting comp rank fetches to run concurrently")
			}
		}
		close(release)
		<-done

		if err != nil {
			t.Fatalf("expected StartSessions with partial comp rank failure to succeed, got %v", err)
		}
		if comp.calls != 2 {
			t.Fatalf("expected exactly 2 comp rank calls (skipping active member), got %d", comp.calls)
		}
		if results[0].Session.CompTracking == nil || !results[0].Session.CompTracking.Enabled || results[0].Session.CompTracking.Start == nil || results[0].Session.CompTracking.Start.Points != 5820 {
			t.Fatalf("expected caller comp start snapshot, got %+v", results[0].Session.CompTracking)
		}
		if results[1].Outcome != StartSessionOutcomeSkippedActive {
			t.Fatalf("expected mate1 skipped_active, got %+v", results[1])
		}
		if results[2].Session.CompTracking == nil || !results[2].Session.CompTracking.Enabled || results[2].Session.CompTracking.Start != nil {
			t.Fatalf("expected mate2 compTracking enabled with nil start after best-effort failure, got %+v", results[2].Session.CompTracking)
		}
	})

	t.Run("all members already active makes 0 CreateSessions and 0 CompRank calls", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		party := &fakePartyReader{}
		store := &fakeSessionStore{
			activeDocs: []Session{
				{ID: "ses-existing", UserID: "u-caller", CharacterID: "char-1", Status: SessionStatusPending},
			},
		}
		comp := &fakeCompRankReader{}
		svc := NewService(users, party, store, comp)

		results, err := svc.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{{UserID: "u-caller", CharacterID: "char-1"}},
			true,
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.createCalls != 0 || comp.calls != 0 {
			t.Fatalf("expected 0 create and 0 comp calls when all skipped, got create=%d comp=%d", store.createCalls, comp.calls)
		}
		if len(results) != 1 || results[0].Outcome != StartSessionOutcomeSkippedActive {
			t.Fatalf("unexpected results: %+v", results)
		}
	})

	t.Run("returns ErrNotInFireteam when any target is not in caller party or caller is offline", func(t *testing.T) {
		users := &fakeUserReader{
			playersByID:  map[string]Player{"u-caller": caller},
			partyPlayers: []Player{mate1},
		}
		party := &fakePartyReader{membershipIDs: []string{caller.PrimaryMembershipID, mate1.PrimaryMembershipID}}
		store := &fakeSessionStore{}
		svc := NewService(users, party, store, nil)

		_, err := svc.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{
				{UserID: "u-caller", CharacterID: "char-1"},
				{UserID: "u-stranger", CharacterID: "char-9"},
			},
			false,
		)
		if !errors.Is(err, ErrNotInFireteam) {
			t.Fatalf("expected ErrNotInFireteam, got %v", err)
		}
		if store.activeDocsCalls != 0 || store.createCalls != 0 {
			t.Fatalf("expected 0 store calls when authorization fails, got active=%d create=%d", store.activeDocsCalls, store.createCalls)
		}

		partyOffline := &fakePartyReader{membershipIDs: nil}
		svcOffline := NewService(users, partyOffline, store, nil)
		_, err = svcOffline.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{{UserID: "u-mate1", CharacterID: "char-2"}},
			false,
		)
		if !errors.Is(err, ErrNotInFireteam) {
			t.Fatalf("expected ErrNotInFireteam when caller party is empty, got %v", err)
		}
	})

	t.Run("maps Bungie unavailable and timeout to ErrDestinyUnavailable while preserving caller cancellation", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		store := &fakeSessionStore{}

		svcDown := NewService(users, &fakePartyReader{err: ErrDestinyUnavailable}, store, nil)
		_, err := svcDown.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{{UserID: "u-mate1", CharacterID: "char-2"}},
			false,
		)
		if !errors.Is(err, ErrDestinyUnavailable) {
			t.Fatalf("expected ErrDestinyUnavailable, got %v", err)
		}

		partyTimeout := &fakePartyReader{
			fn: func(ctx context.Context, _ int64, _ int64) ([]string, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		svcTimeout := &service{
			users:        users,
			party:        partyTimeout,
			sessions:     store,
			partyTimeout: 20 * time.Millisecond,
		}
		_, err = svcTimeout.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{{UserID: "u-mate1", CharacterID: "char-2"}},
			false,
		)
		if !errors.Is(err, ErrDestinyUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected ErrDestinyUnavailable wrapping DeadlineExceeded, got %v", err)
		}

		canceledCtx, cancel := context.WithCancel(clog.NewContext(context.Background()))
		cancel()
		_, err = svcTimeout.StartSessions(
			canceledCtx,
			"u-caller",
			[]StartSessionTarget{{UserID: "u-mate1", CharacterID: "char-2"}},
			false,
		)
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrDestinyUnavailable) {
			t.Fatalf("expected caller cancellation to propagate context.Canceled, got %v", err)
		}
	})

	t.Run("validates empty members, >12 members cap, blank fields, and missing caller", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{}}
		store := &fakeSessionStore{}
		svc := NewService(users, &fakePartyReader{}, store, nil)

		if _, err := svc.StartSessions(clog.NewContext(context.Background()), "u-caller", nil, false); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected ErrInvalidRequest on empty targets, got %v", err)
		}
		tooMany := make([]StartSessionTarget, maxStartSessionTargets+1)
		for i := range tooMany {
			tooMany[i] = StartSessionTarget{UserID: "u-caller", CharacterID: fmt.Sprintf("char-%d", i)}
		}
		if _, err := svc.StartSessions(clog.NewContext(context.Background()), "u-caller", tooMany, false); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected ErrInvalidRequest when exceeding %d targets, got %v", maxStartSessionTargets, err)
		}
		if _, err := svc.StartSessions(clog.NewContext(context.Background()), "u-caller", []StartSessionTarget{{UserID: "u-caller", CharacterID: "  "}}, false); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("expected ErrInvalidRequest on blank characterId, got %v", err)
		}
		if _, err := svc.StartSessions(clog.NewContext(context.Background()), "missing", []StartSessionTarget{{UserID: "missing", CharacterID: "char-1"}}, false); !errors.Is(err, ErrUserNotFound) {
			t.Fatalf("expected ErrUserNotFound for missing caller, got %v", err)
		}
	})

	t.Run("canonicalizes alias actorId to caller.ID across StartSession and StartSessions", func(t *testing.T) {
		aliasID := caller.PrimaryMembershipID
		existing := Session{
			ID:          "ses-existing-canonical",
			UserID:      caller.ID,
			CharacterID: "char-active",
			Status:      SessionStatusPending,
			StartedAt:   fixedNow.Add(-time.Hour),
		}
		users := &fakeUserReader{
			playersByID: map[string]Player{
				aliasID:   caller,
				caller.ID: caller,
			},
		}
		party := &fakePartyReader{err: errors.New("should not call Bungie for self alias")}
		store := &fakeSessionStore{activeDocs: []Session{existing}}
		svc := &service{users: users, party: party, sessions: store, now: func() time.Time { return fixedNow }}

		if _, err := svc.StartSession(
			clog.NewContext(context.Background()),
			aliasID,
			StartSessionTarget{UserID: aliasID, CharacterID: "char-active"},
			false,
		); !errors.Is(err, ErrAlreadyActive) {
			t.Fatalf("expected ErrAlreadyActive when existing session is stored under canonical caller.ID, got %v", err)
		}

		created, err := svc.StartSession(
			clog.NewContext(context.Background()),
			aliasID,
			StartSessionTarget{UserID: aliasID, CharacterID: "char-new"},
			false,
		)
		if err != nil {
			t.Fatalf("unexpected error creating session via alias actorID: %v", err)
		}
		if created.UserID != caller.ID {
			t.Fatalf("expected created session UserID=%q, got %q", caller.ID, created.UserID)
		}

		results, err := svc.StartSessions(
			clog.NewContext(context.Background()),
			aliasID,
			[]StartSessionTarget{
				{UserID: aliasID, CharacterID: "char-active"},
				{UserID: caller.ID, CharacterID: "char-active"},
				{UserID: aliasID, CharacterID: "char-bulk"},
				{UserID: caller.ID, CharacterID: "char-bulk"},
			},
			false,
		)
		if err != nil {
			t.Fatalf("unexpected error in StartSessions via alias actorID: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected alias + canonical pairs deduplicated to 2 results, got %d: %+v", len(results), results)
		}
		if results[0].Outcome != StartSessionOutcomeSkippedActive || results[0].UserID != caller.ID || results[0].Session.ID != "ses-existing-canonical" {
			t.Fatalf("unexpected first canonical result: %+v", results[0])
		}
		if results[1].Outcome != StartSessionOutcomeStarted || results[1].UserID != caller.ID || results[1].Session.UserID != caller.ID {
			t.Fatalf("unexpected second canonical result: %+v", results[1])
		}
		if party.calls != 0 {
			t.Fatalf("expected 0 Bungie party calls for self alias requests, got %d", party.calls)
		}
	})

	t.Run("bounds starting comp rank fetch with timeout and degrades cleanly to Start: nil", func(t *testing.T) {
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		store := &fakeSessionStore{}
		hangingComp := &fakeCompRankReader{
			fn: func(ctx context.Context, _ int64, _ int64, _ string) (CompRankSnapshot, bool, error) {
				<-ctx.Done()
				return CompRankSnapshot{}, false, ctx.Err()
			},
		}
		svc := &service{
			users:        users,
			party:        &fakePartyReader{},
			sessions:     store,
			compRanks:    hangingComp,
			partyTimeout: 20 * time.Millisecond,
			now:          func() time.Time { return fixedNow },
		}

		ses, err := svc.StartSession(
			clog.NewContext(context.Background()),
			"u-caller",
			StartSessionTarget{UserID: "u-caller", CharacterID: "char-1"},
			true,
		)
		if err != nil {
			t.Fatalf("expected StartSession to degrade gracefully on comp rank timeout, got %v", err)
		}
		if ses.CompTracking == nil || !ses.CompTracking.Enabled || ses.CompTracking.Start != nil {
			t.Fatalf("expected CompTracking{Enabled: true, Start: nil}, got %+v", ses.CompTracking)
		}

		results, err := svc.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{{UserID: "u-caller", CharacterID: "char-2"}},
			true,
		)
		if err != nil {
			t.Fatalf("expected StartSessions to degrade gracefully on comp rank timeout, got %v", err)
		}
		if len(results) != 1 || results[0].Session.CompTracking == nil || !results[0].Session.CompTracking.Enabled || results[0].Session.CompTracking.Start != nil {
			t.Fatalf("expected bulk CompTracking{Enabled: true, Start: nil}, got %+v", results)
		}
	})

	t.Run("handles concurrent double-click race returned by CreateSessionsIfInactive", func(t *testing.T) {
		winningSession := Session{
			ID:          "ses-race-winner",
			UserID:      "u-caller",
			CharacterID: "char-1",
			Status:      SessionStatusPending,
			StartedAt:   fixedNow,
		}
		users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
		store := &fakeSessionStore{
			createFn: func(_ context.Context, _ []Session) ([]CreateSessionResult, error) {
				return []CreateSessionResult{{Session: winningSession, Created: false}}, nil
			},
		}
		svc := NewService(users, &fakePartyReader{}, store, nil)

		if _, err := svc.StartSession(
			clog.NewContext(context.Background()),
			"u-caller",
			StartSessionTarget{UserID: "u-caller", CharacterID: "char-1"},
			false,
		); !errors.Is(err, ErrAlreadyActive) {
			t.Fatalf("expected ErrAlreadyActive on StartSession race, got %v", err)
		}

		results, err := svc.StartSessions(
			clog.NewContext(context.Background()),
			"u-caller",
			[]StartSessionTarget{{UserID: "u-caller", CharacterID: "char-1"}},
			false,
		)
		if err != nil {
			t.Fatalf("unexpected error on StartSessions race: %v", err)
		}
		if len(results) != 1 || results[0].Outcome != StartSessionOutcomeSkippedActive || results[0].Session.ID != "ses-race-winner" {
			t.Fatalf("expected skipped_active with winning session on race, got %+v", results)
		}
	})
}

func TestGetFireteamSortsActiveSessionsNewestFirstWithIDTieBreaker(t *testing.T) {
	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	caller := Player{
		ID:                  "u-caller",
		PrimaryMembershipID: "4611686018467484863",
		DisplayName:         "Caller",
		Memberships:         []Membership{{ID: "4611686018467484863", Type: 3}},
	}
	users := &fakeUserReader{playersByID: map[string]Player{"u-caller": caller}}
	party := &fakePartyReader{membershipIDs: []string{caller.PrimaryMembershipID}}
	store := &fakeSessionStore{
		activeSummaries: []ActiveSession{
			{SessionID: "ses-older", UserID: "u-caller", CharacterID: "c1", StartedAt: t1},
			{SessionID: "ses-tie-b", UserID: "u-caller", CharacterID: "c2", StartedAt: t2},
			{SessionID: "ses-tie-a", UserID: "u-caller", CharacterID: "c3", StartedAt: t2},
		},
	}
	svc := NewService(users, party, store, nil)

	members, err := svc.GetFireteam(clog.NewContext(context.Background()), "u-caller")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(members) != 1 || len(members[0].ActiveSessions) != 3 {
		t.Fatalf("unexpected members: %+v", members)
	}
	gotIDs := []string{
		members[0].ActiveSessions[0].SessionID,
		members[0].ActiveSessions[1].SessionID,
		members[0].ActiveSessions[2].SessionID,
	}
	wantIDs := []string{"ses-tie-a", "ses-tie-b", "ses-older"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("expected active sessions sorted %v, got %v", wantIDs, gotIDs)
	}
}
