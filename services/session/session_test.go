package session

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

type memorySessionStore struct {
	docs        map[string]*api.Session
	seq         int
	getDocCalls atomic.Int32
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{
		docs: make(map[string]*api.Session),
	}
}

func (m *memorySessionStore) hasActive(_ context.Context, userID, characterID string) (bool, error) {
	for _, s := range m.docs {
		if s.UserID == userID && s.CharacterID == characterID && s.Status != nil && *s.Status == api.SessionPending {
			return true, nil
		}
	}
	return false, nil
}

func (m *memorySessionStore) createDoc(_ context.Context, ses *api.Session) (*api.Session, error) {
	m.seq++
	ses.ID = fmt.Sprintf("ses-%d", m.seq)
	cp := *ses
	m.docs[ses.ID] = &cp
	return &cp, nil
}

func (m *memorySessionStore) getActiveDoc(_ context.Context, userID, characterID string) (*api.Session, error) {
	for _, s := range m.docs {
		if s.UserID == userID && s.CharacterID == characterID && s.Status != nil && *s.Status == api.SessionPending {
			cp := *s
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("no active session found")
}

func (m *memorySessionStore) getDoc(_ context.Context, sessionID string) (*api.Session, error) {
	m.getDocCalls.Add(1)
	s, ok := m.docs[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *s
	if s.CompTracking != nil {
		ctCopy := *s.CompTracking
		cp.CompTracking = &ctCopy
	}
	return &cp, nil
}

func (m *memorySessionStore) listDocs(_ context.Context, _, _ *string, _ *api.SessionStatus) ([]api.Session, error) {
	out := make([]api.Session, 0, len(m.docs))
	for _, s := range m.docs {
		out = append(out, *s)
	}
	return out, nil
}

func (m *memorySessionStore) updateFields(_ context.Context, _ string, _ []firestore.Update) error {
	return nil
}

func (m *memorySessionStore) completeDoc(_ context.Context, sessionID string, completedAt time.Time) error {
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	s.CompletedAt = &completedAt
	s.Status = ptr.Of(api.SessionComplete)
	return nil
}

func (m *memorySessionStore) updateCompTrackingEnd(_ context.Context, sessionID string, end *api.CompRankSnapshot, pointDelta *int) error {
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	if s.CompTracking == nil {
		s.CompTracking = &api.CompTracking{Enabled: true}
	}
	s.CompTracking.End = end
	s.CompTracking.PointDelta = pointDelta
	return nil
}

func (m *memorySessionStore) setLastActivity(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}

func (m *memorySessionStore) deleteDoc(_ context.Context, sessionID string) error {
	delete(m.docs, sessionID)
	return nil
}

func (m *memorySessionStore) addAggregateIDs(_ context.Context, _ string, _ []string) error {
	return nil
}

type fakeSessionUserService struct {
	user.Service
	u *user.User
}

func (f *fakeSessionUserService) GetPrimaryMembership(_ context.Context, _ string) (int64, int64, error) {
	if f.u == nil {
		return 0, 0, user.NotFound
	}
	return f.u.PrimaryMembership()
}

type fakeSessionD2Service struct {
	destiny.Service
	snapshots []*api.CompRankSnapshot
	calls     atomic.Int32
}

func (f *fakeSessionD2Service) GetCompetitiveRank(_ context.Context, _ int64, _ int64, _ string) (*api.CompRankSnapshot, error) {
	idx := int(f.calls.Add(1)) - 1
	if idx < len(f.snapshots) {
		return f.snapshots[idx], nil
	}
	if len(f.snapshots) > 0 {
		return f.snapshots[len(f.snapshots)-1], nil
	}
	return nil, fmt.Errorf("no snapshot configured")
}

func TestSessionSorting(t *testing.T) {
	now := time.Now()
	sessions := []api.Session{
		{
			ID:        "1",
			StartedAt: now.Add(-2 * time.Hour),
			Status:    ptr.Of(api.SessionComplete),
		},
		{
			ID:        "2",
			StartedAt: now,
			Status:    ptr.Of(api.SessionPending),
		},
		{
			ID:        "3",
			StartedAt: now.Add(-1 * time.Hour),
			Status:    ptr.Of(api.SessionComplete),
		},
	}

	slices.SortFunc(sessions, func(a, b api.Session) int {
		return b.StartedAt.Compare(a.StartedAt)
	})

	if sessions[0].ID != "2" {
		t.Errorf("expected newest session first ('2'), got %s", sessions[0].ID)
	}
	if sessions[1].ID != "3" {
		t.Errorf("expected second session '3', got %s", sessions[1].ID)
	}
	if sessions[2].ID != "1" {
		t.Errorf("expected oldest session last ('1'), got %s", sessions[2].ID)
	}
}

func TestSessionWithSummary(t *testing.T) {
	summary := api.SessionSummary{
		TotalMatches: ptr.Of(5),
		Wins:         ptr.Of(3),
		Losses:       ptr.Of(2),
		WinRate:      ptr.Of(0.60),
		Kills:        ptr.Of(45),
		Deaths:       ptr.Of(25),
		Assists:      ptr.Of(15),
		KDRatio:      ptr.Of(1.80),
		KDARatio:     ptr.Of(2.10),
		ModesPlayed:  ptr.Of([]string{"Control", "Trials of Osiris"}),
		TopWeapons: ptr.Of([]api.SessionWeaponSummary{
			{
				Name:  ptr.Of("Igneous Hammer"),
				Icon:  ptr.Of("/img/igneous.png"),
				Kills: ptr.Of(20),
			},
		}),
	}

	session := api.Session{
		ID:           "test-session-1",
		StartedAt:    time.Now(),
		UserID:       "user-1",
		CharacterID:  "char-1",
		AggregateIDs: []string{"agg-1", "agg-2"},
		Summary:      &summary,
	}

	if session.ID != "test-session-1" || session.UserID != "user-1" || session.CharacterID != "char-1" || len(session.AggregateIDs) != 2 {
		t.Errorf("session fields mismatch: %+v", session)
	}
	if session.Summary == nil {
		t.Fatalf("expected session summary to be present")
	}
	if *session.Summary.TotalMatches != 5 {
		t.Errorf("expected total matches 5, got %d", *session.Summary.TotalMatches)
	}
	if *session.Summary.WinRate != 0.60 {
		t.Errorf("expected win rate 0.60, got %f", *session.Summary.WinRate)
	}
	if len(*session.Summary.TopWeapons) != 1 || *(*session.Summary.TopWeapons)[0].Name != "Igneous Hammer" {
		t.Errorf("expected top weapon 'Igneous Hammer'")
	}
}

func TestSessionService_CompTrackingStartCompleteAndIdempotentFinalize(t *testing.T) {
	store := newMemorySessionStore()
	uSvc := &fakeSessionUserService{
		u: &user.User{
			ID:                  "user-1",
			PrimaryMembershipID: "4611686018467484863",
			Memberships:         []user.Membership{{ID: "4611686018467484863", Type: 3}},
		},
	}
	d2Svc := &fakeSessionD2Service{
		snapshots: []*api.CompRankSnapshot{
			{
				Points:       5820,
				StepIndex:    ptr.Of(21),
				DivisionName: ptr.Of("Platinum III"),
				Icon:         ptr.Of("/icons/plat.png"),
				RecordedAt:   ptr.Of(time.Now().UTC()),
			},
			{
				Points:       6045,
				StepIndex:    ptr.Of(22),
				DivisionName: ptr.Of("Platinum II"),
				Icon:         ptr.Of("/icons/plat.png"),
				RecordedAt:   ptr.Of(time.Now().UTC()),
			},
		},
	}

	svc := &service{
		store:       store,
		userService: uSvc,
		d2Service:   d2Svc,
	}

	ctx := context.Background()
	audit := api.AuditField{ID: "user-1", Username: "Zone 6 Weegee"}

	// 1. Start session with trackComp = true
	started, err := svc.Start(ctx, "user-1", "char-1", audit, true)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if started.CompTracking == nil || !started.CompTracking.Enabled || started.CompTracking.Start == nil {
		t.Fatalf("expected CompTracking.Start populated, got %+v", started.CompTracking)
	}
	if started.CompTracking.Start.Points != 5820 || *started.CompTracking.Start.DivisionName != "Platinum III" {
		t.Fatalf("unexpected CompTracking.Start: %+v", started.CompTracking.Start)
	}

	// 1b. FinalizeCompTracking on a still-pending session refuses to seal compTracking.end prematurely
	if err := svc.FinalizeCompTracking(ctx, started.ID); err == nil {
		t.Fatal("expected FinalizeCompTracking to fail while session is still pending")
	}
	if d2Svc.calls.Load() != 1 {
		t.Fatalf("expected no additional GetCompetitiveRank calls when session is pending, got %d", d2Svc.calls.Load())
	}

	// 2. Complete session populates compTracking.end and compTracking.pointDelta with a single getDoc read
	store.getDocCalls.Store(0)
	if err := svc.Complete(ctx, started.ID); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if store.getDocCalls.Load() != 1 {
		t.Fatalf("expected Complete to read session doc once, got %d getDoc calls", store.getDocCalls.Load())
	}
	completed, err := svc.Get(ctx, started.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if completed.CompTracking == nil || completed.CompTracking.End == nil || completed.CompTracking.PointDelta == nil {
		t.Fatalf("expected CompTracking.End and PointDelta populated, got %+v", completed.CompTracking)
	}
	if completed.CompTracking.End.Points != 6045 {
		t.Fatalf("expected End.Points=6045, got %d", completed.CompTracking.End.Points)
	}
	if *completed.CompTracking.PointDelta != 225 {
		t.Fatalf("expected PointDelta=225, got %d", *completed.CompTracking.PointDelta)
	}
	if d2Svc.calls.Load() != 2 {
		t.Fatalf("expected 2 GetCompetitiveRank calls (start + end), got %d", d2Svc.calls.Load())
	}

	// 3. Calling FinalizeCompTracking again is idempotent (skipped, no extra Bungie call)
	if err := svc.FinalizeCompTracking(context.Background(), started.ID); err != nil {
		t.Fatalf("FinalizeCompTracking failed: %v", err)
	}
	if d2Svc.calls.Load() != 2 {
		t.Fatalf("expected no additional GetCompetitiveRank calls on idempotent finalize, got %d", d2Svc.calls.Load())
	}
}

func TestSessionService_StartWithoutCompTrackingSkipsBungieCall(t *testing.T) {
	store := newMemorySessionStore()
	d2Svc := &fakeSessionD2Service{}
	svc := &service{
		store:     store,
		d2Service: d2Svc,
	}

	started, err := svc.Start(context.Background(), "user-1", "char-1", api.AuditField{ID: "user-1", Username: "Guardian"}, false)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if started.CompTracking != nil {
		t.Fatalf("expected nil CompTracking when trackComp is omitted/false, got %+v", started.CompTracking)
	}
	if d2Svc.calls.Load() != 0 {
		t.Fatalf("expected 0 GetCompetitiveRank calls when trackComp is false, got %d", d2Svc.calls.Load())
	}

	if err := svc.FinalizeCompTracking(context.Background(), started.ID); err != nil {
		t.Fatalf("FinalizeCompTracking failed: %v", err)
	}
	if d2Svc.calls.Load() != 0 {
		t.Fatalf("expected 0 GetCompetitiveRank calls when finalizing session without compTracking, got %d", d2Svc.calls.Load())
	}
}

func TestSessionService_StartWarnsWhenCompServicesMissing(t *testing.T) {
	svc := &service{
		store: newMemorySessionStore(),
	}
	ctx := clog.NewContext(context.Background())
	started, err := svc.Start(ctx, "user-1", "char-1", api.AuditField{ID: "user-1", Username: "Guardian"}, true)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if started.CompTracking == nil || !started.CompTracking.Enabled || started.CompTracking.Start != nil {
		t.Fatalf("expected CompTracking enabled with nil Start, got %+v", started.CompTracking)
	}
	if !clog.BagFromContext(ctx).HasWarning() {
		t.Fatal("expected canonical FieldBag warning when competitive rank services are not configured")
	}
}

