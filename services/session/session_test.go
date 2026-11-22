package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

type memorySessionStore struct {
	mu          sync.Mutex
	docs        map[string]*api.Session
	updatedAt   map[string]time.Time
	seq         int
	getDocCalls atomic.Int32
	writes      atomic.Int32
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{
		docs:      make(map[string]*api.Session),
		updatedAt: make(map[string]time.Time),
	}
}

func cloneSession(s *api.Session) api.Session {
	cp := *s
	if s.AggregateIDs != nil {
		cp.AggregateIDs = append([]string(nil), s.AggregateIDs...)
	}
	if s.CompTracking != nil {
		ctCopy := *s.CompTracking
		cp.CompTracking = &ctCopy
	}
	if s.Summary != nil {
		sumCopy := *s.Summary
		cp.Summary = &sumCopy
	}
	return cp
}

func (m *memorySessionStore) hasActive(_ context.Context, userID, characterID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.docs {
		if s.UserID == userID && s.CharacterID == characterID && s.Status != nil && *s.Status == api.SessionPending {
			return true, nil
		}
	}
	return false, nil
}

func (m *memorySessionStore) createDoc(_ context.Context, ses *api.Session) (*api.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	m.seq++
	ses.ID = fmt.Sprintf("ses-%d", m.seq)
	cp := cloneSession(ses)
	m.docs[ses.ID] = &cp
	out := cloneSession(&cp)
	return &out, nil
}

func (m *memorySessionStore) getActiveDoc(_ context.Context, userID, characterID string) (*api.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.docs {
		if s.UserID == userID && s.CharacterID == characterID && s.Status != nil && *s.Status == api.SessionPending {
			cp := cloneSession(s)
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("no active session found")
}

func (m *memorySessionStore) getDoc(_ context.Context, sessionID string) (*api.Session, error) {
	m.getDocCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.docs[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := cloneSession(s)
	return &cp, nil
}

func (m *memorySessionStore) listDocs(_ context.Context, userID, characterID *string, st *api.SessionStatus) ([]api.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.Session, 0, len(m.docs))
	for _, s := range m.docs {
		if userID != nil && *userID != "" && s.UserID != *userID {
			continue
		}
		if characterID != nil && *characterID != "" && s.CharacterID != *characterID {
			continue
		}
		if st != nil && *st != "" && (s.Status == nil || *s.Status != *st) {
			continue
		}
		out = append(out, cloneSession(s))
	}
	return out, nil
}

func (m *memorySessionStore) getTickCandidates(_ context.Context, completedSince time.Time) ([]tickCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.docs))
	for id := range m.docs {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]tickCandidate, 0, len(ids))
	for _, id := range ids {
		s := m.docs[id]
		isPending := s.Status != nil && *s.Status == api.SessionPending
		isRecentCompleted := s.CompletedAt != nil && !s.CompletedAt.Before(completedSince)
		if !isPending && !isRecentCompleted {
			continue
		}
		var upd *time.Time
		if ts, ok := m.updatedAt[id]; ok {
			t := ts
			upd = &t
		}
		out = append(out, tickCandidate{
			session:   cloneSession(s),
			updatedAt: upd,
		})
	}
	return out, nil
}

func (m *memorySessionStore) updateFields(_ context.Context, _ string, _ []firestore.Update) error {
	m.writes.Add(1)
	return nil
}

func (m *memorySessionStore) completeDoc(_ context.Context, sessionID string, completedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	s.CompletedAt = &completedAt
	s.Status = new(api.SessionComplete)
	return nil
}

func (m *memorySessionStore) endSessionDoc(_ context.Context, sessionID string, completedBy api.AuditField, completedAt time.Time, summary *api.SessionSummary) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	cb := completedBy
	s.CompletedBy = &cb
	s.Status = new(api.SessionComplete)
	s.CompletedAt = &completedAt
	if summary != nil {
		sumCopy := *summary
		s.Summary = &sumCopy
	}
	m.updatedAt[sessionID] = completedAt
	return nil
}

func (m *memorySessionStore) updateSummary(_ context.Context, sessionID string, summary *api.SessionSummary, updatedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	if summary != nil {
		sumCopy := *summary
		s.Summary = &sumCopy
	} else {
		s.Summary = nil
	}
	m.updatedAt[sessionID] = updatedAt
	return nil
}

func (m *memorySessionStore) updateCompTrackingEnd(_ context.Context, sessionID string, end *api.CompRankSnapshot, pointDelta *int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
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

func (m *memorySessionStore) setLastActivity(_ context.Context, sessionID, activityID string, seenAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	s.LastSeenActivityID = new(activityID)
	s.LastSeenTimestamp = &seenAt
	m.updatedAt[sessionID] = seenAt
	return nil
}

func (m *memorySessionStore) deleteDoc(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	delete(m.docs, sessionID)
	delete(m.updatedAt, sessionID)
	return nil
}

func (m *memorySessionStore) addAggregateIDs(_ context.Context, sessionID string, aggregateIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	existing := make(map[string]struct{}, len(s.AggregateIDs))
	for _, id := range s.AggregateIDs {
		existing[id] = struct{}{}
	}
	for _, id := range aggregateIDs {
		if _, seen := existing[id]; !seen {
			s.AggregateIDs = append(s.AggregateIDs, id)
			existing[id] = struct{}{}
		}
	}
	return nil
}

type fakeSessionUserService struct {
	user.Service
	u     *user.User
	users map[string]*user.User
}

func (f *fakeSessionUserService) GetPrimaryMembership(_ context.Context, userID string) (int64, int64, error) {
	if f.users != nil {
		if u, ok := f.users[userID]; ok && u != nil {
			return u.PrimaryMembership()
		}
		return 0, 0, user.NotFound
	}
	if f.u == nil {
		return 0, 0, user.NotFound
	}
	return f.u.PrimaryMembership()
}

type fakeSessionD2Service struct {
	destiny.Service
	snapshots       []*api.CompRankSnapshot
	calls           atomic.Int32
	activitiesByChr map[string][]api.ActivityHistory
	activityErrByCh map[string]error
	perfsByAct      map[string]map[string]api.InstancePerformance
	perfErrByAct    map[string]error
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

func (f *fakeSessionD2Service) GetAllPVPActivity(_ context.Context, _ string, _ int64, characterID string, _ int64, _ int64) ([]api.ActivityHistory, error) {
	if err, ok := f.activityErrByCh[characterID]; ok && err != nil {
		return nil, err
	}
	return f.activitiesByChr[characterID], nil
}

func (f *fakeSessionD2Service) GetPerformances(_ context.Context, activityID string, _ []string) (map[string]api.InstancePerformance, error) {
	if err, ok := f.perfErrByAct[activityID]; ok && err != nil {
		return nil, err
	}
	return f.perfsByAct[activityID], nil
}

type fakeSessionSnapshotService struct {
	snapshot.Service
	writes        atomic.Int32
	saveCalls     atomic.Int32
	saveErrByChar map[string]error
	findBestFitFn func(userID, characterID string, period time.Time, weapons map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error)
	enrichFn      func(snap *api.CharacterSnapshot, perf api.InstancePerformance) (*api.InstancePerformance, error)
}

func (f *fakeSessionSnapshotService) Save(_ context.Context, _, _, characterID string) (*api.CharacterSnapshot, error) {
	f.saveCalls.Add(1)
	if err, ok := f.saveErrByChar[characterID]; ok && err != nil {
		return nil, err
	}
	f.writes.Add(1)
	return &api.CharacterSnapshot{ID: "snap-" + characterID}, nil
}

func (f *fakeSessionSnapshotService) FindBestFit(_ context.Context, userID, characterID string, activityPeriod time.Time, weapons map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error) {
	if f.findBestFitFn != nil {
		return f.findBestFitFn(userID, characterID, activityPeriod, weapons)
	}
	snapID := "snap-" + characterID
	return &api.CharacterSnapshot{ID: snapID}, &api.SnapshotLink{
		CharacterID:     characterID,
		SnapshotID:      &snapID,
		ConfidenceLevel: api.HighConfidenceLevel,
	}, nil
}

func (f *fakeSessionSnapshotService) EnrichInstancePerformance(snap *api.CharacterSnapshot, performance api.InstancePerformance) (*api.InstancePerformance, error) {
	if f.enrichFn != nil {
		return f.enrichFn(snap, performance)
	}
	cp := performance
	return &cp, nil
}

type fakeSessionAggregateService struct {
	aggregate.Service
	mu         sync.Mutex
	writes     atomic.Int32
	byID       map[string]*api.Aggregate
	byActivity map[string]*api.Aggregate
	seq        int
}

func newFakeSessionAggregateService() *fakeSessionAggregateService {
	return &fakeSessionAggregateService{
		byID:       make(map[string]*api.Aggregate),
		byActivity: make(map[string]*api.Aggregate),
	}
}

func (f *fakeSessionAggregateService) seed(agg api.Aggregate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := agg
	f.byID[agg.ID] = &cp
	f.byActivity[agg.ActivityID] = &cp
}

func (f *fakeSessionAggregateService) GetAggregates(_ context.Context, IDs []string) ([]api.Aggregate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]api.Aggregate, 0, len(IDs))
	for _, id := range IDs {
		if agg, ok := f.byID[id]; ok {
			out = append(out, *agg)
		}
	}
	return out, nil
}

func (f *fakeSessionAggregateService) GetAggregatesByActivity(_ context.Context, IDs []string) ([]api.Aggregate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]api.Aggregate, 0, len(IDs))
	for _, id := range IDs {
		if agg, ok := f.byActivity[id]; ok {
			out = append(out, *agg)
		}
	}
	return out, nil
}

func (f *fakeSessionAggregateService) UpsertSessionActivity(_ context.Context, characterID string, activityHistory api.ActivityHistory, link api.SnapshotLink, performance api.InstancePerformance) (*api.Aggregate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes.Add(1)
	if existing, ok := f.byActivity[activityHistory.InstanceID]; ok {
		if existing.SnapshotLinks == nil {
			existing.SnapshotLinks = make(map[string]api.SnapshotLink)
		}
		if existing.Performance == nil {
			existing.Performance = make(map[string]api.InstancePerformance)
		}
		existing.SnapshotLinks[characterID] = link
		existing.Performance[characterID] = performance
		if link.SessionID != nil && !slices.Contains(existing.SessionIds, *link.SessionID) {
			existing.SessionIds = append(existing.SessionIds, *link.SessionID)
		}
		cp := *existing
		return &cp, nil
	}

	f.seq++
	id := fmt.Sprintf("agg-%d", f.seq)
	var sessionIDs []string
	if link.SessionID != nil {
		sessionIDs = []string{*link.SessionID}
	}
	agg := &api.Aggregate{
		ID:              id,
		ActivityID:      activityHistory.InstanceID,
		ActivityDetails: activityHistory,
		CharacterIds:    []string{characterID},
		SessionIds:      sessionIDs,
		SnapshotLinks:   map[string]api.SnapshotLink{characterID: link},
		Performance:     map[string]api.InstancePerformance{characterID: performance},
	}
	f.byID[id] = agg
	f.byActivity[activityHistory.InstanceID] = agg
	cp := *agg
	return &cp, nil
}

type fakeSessionManifestService struct {
	destiny.ManifestService
	items      map[string]destiny.ItemDefinition
	activities map[string]destiny.ActivityDefinition
	modes      map[string]destiny.ActivityModeDefinition
}

func (f *fakeSessionManifestService) GetItemsByIDs(_ context.Context, IDs []int64) (map[string]destiny.ItemDefinition, error) {
	out := make(map[string]destiny.ItemDefinition)
	for _, id := range IDs {
		key := strconv.FormatInt(id, 10)
		if def, ok := f.items[key]; ok {
			out[key] = def
		}
	}
	return out, nil
}

func (f *fakeSessionManifestService) GetActivitiesByIDs(_ context.Context, IDs []int64) (map[string]destiny.ActivityDefinition, error) {
	out := make(map[string]destiny.ActivityDefinition)
	for _, id := range IDs {
		key := strconv.FormatInt(id, 10)
		if def, ok := f.activities[key]; ok {
			out[key] = def
		}
	}
	return out, nil
}

func (f *fakeSessionManifestService) GetActivityModesByIDs(_ context.Context, IDs []int64) (map[string]destiny.ActivityModeDefinition, error) {
	out := make(map[string]destiny.ActivityModeDefinition)
	for _, id := range IDs {
		key := strconv.FormatInt(id, 10)
		if def, ok := f.modes[key]; ok {
			out[key] = def
		}
	}
	return out, nil
}

func statVal(v float64) *api.StatsValuePair {
	return &api.StatsValuePair{Value: new(v)}
}

func weaponMetrics(refID int64, kills float64, fallbackName, fallbackIcon string) api.WeaponInstanceMetrics {
	stats := map[string]api.UniqueStatValue{
		"uniqueWeaponKills": {
			Name:  new("uniqueWeaponKills"),
			Basic: api.StatsValuePair{Value: new(kills)},
		},
	}
	var disp *api.Display
	if fallbackName != "" || fallbackIcon != "" {
		disp = &api.Display{Name: fallbackName}
		if fallbackIcon != "" {
			disp.Icon = new(fallbackIcon)
		}
	}
	return api.WeaponInstanceMetrics{
		ReferenceID: new(refID),
		Stats:       &stats,
		Display:     disp,
	}
}

func assertAllWarningsAreStrings(t *testing.T, attrs []slog.Attr) {
	t.Helper()
	var walk func(list []slog.Attr)
	walk = func(list []slog.Attr) {
		for _, a := range list {
			if a.Key == "warning" && a.Value.Kind() != slog.KindString {
				t.Errorf("expected warning attribute to be a plain string, got kind=%v val=%v", a.Value.Kind(), a.Value.Any())
			}
			if a.Value.Kind() == slog.KindGroup {
				walk(a.Value.Group())
			}
		}
	}
	walk(attrs)
}

func TestSessionSorting(t *testing.T) {
	now := time.Now()
	sessions := []api.Session{
		{
			ID:        "1",
			StartedAt: now.Add(-2 * time.Hour),
			Status:    new(api.SessionComplete),
		},
		{
			ID:        "2",
			StartedAt: now,
			Status:    new(api.SessionPending),
		},
		{
			ID:        "3",
			StartedAt: now.Add(-1 * time.Hour),
			Status:    new(api.SessionComplete),
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
		TotalMatches: new(5),
		Wins:         new(3),
		Losses:       new(2),
		WinRate:      new(0.60),
		Kills:        new(45),
		Deaths:       new(25),
		Assists:      new(15),
		KDRatio:      new(1.80),
		KDARatio:     new(2.10),
		ModesPlayed:  new([]string{"Control", "Trials of Osiris"}),
		TopWeapons: new([]api.SessionWeaponSummary{
			{
				Name:  new("Igneous Hammer"),
				Icon:  new("/img/igneous.png"),
				Kills: new(20),
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
				StepIndex:    new(21),
				DivisionName: new("Platinum III"),
				Icon:         new("/icons/plat.png"),
				RecordedAt:   new(time.Now().UTC()),
			},
			{
				Points:       6045,
				StepIndex:    new(22),
				DivisionName: new("Platinum II"),
				Icon:         new("/icons/plat.png"),
				RecordedAt:   new(time.Now().UTC()),
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

	if err := svc.FinalizeCompTracking(ctx, started.ID); err == nil {
		t.Fatal("expected FinalizeCompTracking to fail while session is still pending")
	}
	if d2Svc.calls.Load() != 1 {
		t.Fatalf("expected no additional GetCompetitiveRank calls when session is pending, got %d", d2Svc.calls.Load())
	}

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

func TestSessionService_RunTickScaffolding(t *testing.T) {
	svc := &service{
		store: newMemorySessionStore(),
	}
	ctx := clog.NewContext(context.Background())

	res, err := svc.RunTick(ctx, TickOptions{DryRun: true, SkipSave: true})
	if err != nil {
		t.Fatalf("RunTick failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil SessionTickResponse")
	}
	if !res.DryRun {
		t.Fatalf("expected DryRun=true in response, got %+v", res)
	}
	if res.DurationMs < 0 {
		t.Fatalf("expected non-negative DurationMs, got %d", res.DurationMs)
	}
}

func TestSessionLifecycleHelpers(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	t.Run("ShouldFinalizeComp", func(t *testing.T) {
		valid := api.Session{
			ID:           "ses-1",
			AggregateIDs: []string{"agg-1"},
			CompTracking: &api.CompTracking{Enabled: true},
		}
		if !ShouldFinalizeComp(valid) {
			t.Fatal("expected valid comp session to require finalization")
		}

		noAggs := valid
		noAggs.AggregateIDs = nil
		if ShouldFinalizeComp(noAggs) {
			t.Fatal("expected session with no aggregates to skip finalization")
		}

		alreadyEnded := valid
		alreadyEnded.CompTracking = &api.CompTracking{Enabled: true, End: &api.CompRankSnapshot{Points: 5000}}
		if ShouldFinalizeComp(alreadyEnded) {
			t.Fatal("expected already-finalized session to skip finalization")
		}

		disabled := valid
		disabled.CompTracking = &api.CompTracking{Enabled: false}
		if ShouldFinalizeComp(disabled) {
			t.Fatal("expected disabled comp session to skip finalization")
		}

		emptyID := valid
		emptyID.ID = ""
		if ShouldFinalizeComp(emptyID) {
			t.Fatal("expected session without ID to skip finalization")
		}
	})

	t.Run("IsStaleSession", func(t *testing.T) {
		fresh := now.Add(-1*time.Hour - 59*time.Minute)
		stale := now.Add(-2 * time.Hour)

		if IsStaleSession(api.Session{LastSeenTimestamp: &fresh}, &stale, now) {
			t.Fatal("expected fresh LastSeenTimestamp to take precedence over stale updatedAt")
		}
		if !IsStaleSession(api.Session{LastSeenTimestamp: &stale}, &fresh, now) {
			t.Fatal("expected stale LastSeenTimestamp to be stale")
		}
		if !IsStaleSession(api.Session{}, &stale, now) {
			t.Fatal("expected fallback to stale updatedAt when LastSeenTimestamp is nil")
		}
		if IsStaleSession(api.Session{}, &fresh, now) {
			t.Fatal("expected fresh updatedAt not to be stale")
		}
		if IsStaleSession(api.Session{}, nil, now) {
			t.Fatal("expected session with neither LastSeenTimestamp nor updatedAt not to be stale")
		}
	})

	t.Run("IsInactiveSession", func(t *testing.T) {
		if IsInactiveSession(api.Session{StartedAt: now.Add(-90 * time.Minute)}, now) {
			t.Fatal("expected 90m-old session not to be inactive")
		}
		if !IsInactiveSession(api.Session{StartedAt: now.Add(-2 * time.Hour)}, now) {
			t.Fatal("expected 2h-old session to be inactive")
		}
	})

	t.Run("filterTickActivities", func(t *testing.T) {
		activities := []api.ActivityHistory{
			{InstanceID: "pub-1", Activity: "Control", Location: "Javelin-4", IsPrivate: new(false)},
			{InstanceID: "priv-drop", Activity: "Private Rumble", Location: "Endless Vale", IsPrivate: new(true)},
			{InstanceID: "priv-ib", Activity: "Iron Banner: Control", Location: "Burnout", IsPrivate: new(true)},
			{InstanceID: "priv-tribute", Activity: "Crucible", Location: "Tribute Arena", IsPrivate: new(true)},
			{InstanceID: "pub-nil", Activity: "Trials of Osiris", Location: "Altar of Flame"},
		}

		filtered := filterTickActivities(activities)
		got := make([]string, 0, len(filtered))
		for _, a := range filtered {
			got = append(got, a.InstanceID)
		}
		want := []string{"pub-1", "priv-ib", "priv-tribute", "pub-nil"}
		if !slices.Equal(got, want) {
			t.Fatalf("filtered IDs mismatch: got %v, want %v", got, want)
		}
	})
}

func TestComputeSessionSummary(t *testing.T) {
	aggSvc := newFakeSessionAggregateService()
	manifestSvc := &fakeSessionManifestService{
		items: map[string]destiny.ItemDefinition{
			"1001": {Hash: 1001, DisplayProperties: destiny.ItemDisplayProperties{Name: "Rose", Icon: "/img/rose.png"}},
			"1002": {Hash: 1002, DisplayProperties: destiny.ItemDisplayProperties{Name: "Matador 64", Icon: "/img/matador.png"}},
			"1003": {Hash: 1003, DisplayProperties: destiny.ItemDisplayProperties{Name: "Igneous Hammer", Icon: "https://cdn.example.com/igneous.png"}},
			"1004": {Hash: 1004, DisplayProperties: destiny.ItemDisplayProperties{Name: "Cataphract GL3", Icon: "/img/cataphract.png"}},
			"1005": {Hash: 1005, DisplayProperties: destiny.ItemDisplayProperties{Name: "Conditional Finality", Icon: "/img/cf.png"}},
		},
		activities: map[string]destiny.ActivityDefinition{
			"9001": {Hash: 9001, DirectActivityModeHash: 5001},
		},
		modes: map[string]destiny.ActivityModeDefinition{
			"5001": {Hash: 5001, DisplayProperties: destiny.ModeDisplayProperties{Name: "Trials of Osiris"}},
		},
	}

	svc := &service{
		aggregateService: aggSvc,
		manifestService:  manifestSvc,
	}

	t.Run("empty session returns zeroed summary", func(t *testing.T) {
		sum, err := svc.ComputeSessionSummary(context.Background(), api.Session{ID: "ses-empty"})
		if err != nil {
			t.Fatalf("ComputeSessionSummary failed: %v", err)
		}
		if *sum.TotalMatches != 0 || *sum.WinRate != 0 || *sum.KDRatio != 0 || *sum.KDARatio != 0 {
			t.Fatalf("unexpected zeroed stats: %+v", sum)
		}
		if sum.ModesPlayed == nil || len(*sum.ModesPlayed) != 0 {
			t.Fatalf("expected non-nil empty ModesPlayed, got %v", sum.ModesPlayed)
		}
		if sum.TopWeapons == nil || len(*sum.TopWeapons) != 0 {
			t.Fatalf("expected non-nil empty TopWeapons, got %v", sum.TopWeapons)
		}
	})

	t.Run("multi-match aggregation with mode fallback and top-5 weapons", func(t *testing.T) {
		aggSvc.seed(api.Aggregate{
			ID:         "agg-1",
			ActivityID: "act-1",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-1",
				Mode:       new("Control"),
			},
			Performance: map[string]api.InstancePerformance{
				"char-1": {
					PlayerStats: api.PlayerStats{
						Standing: statVal(0), // win
						Kills:    statVal(10),
						Deaths:   statVal(4),
						Assists:  statVal(6),
					},
					Weapons: map[string]api.WeaponInstanceMetrics{
						"1001": weaponMetrics(1001, 8, "Fallback Rose", "/fallback.png"),
						"1002": weaponMetrics(1002, 5, "", ""),
						"1006": weaponMetrics(1006, 12, "Fallback Hand Cannon", "/img/fb.png"),
						"1007": weaponMetrics(1007, 1, "", ""),
					},
				},
			},
		})
		aggSvc.seed(api.Aggregate{
			ID:         "agg-2",
			ActivityID: "act-2",
			ActivityDetails: api.ActivityHistory{
				InstanceID:   "act-2",
				ActivityHash: 9001, // triggers manifest mode fallback -> Trials of Osiris
			},
			Performance: map[string]api.InstancePerformance{
				"char-1": {
					PlayerStats: api.PlayerStats{
						Standing: statVal(1), // loss
						Kills:    statVal(5),
						Deaths:   statVal(6),
						Assists:  statVal(4),
					},
					Weapons: map[string]api.WeaponInstanceMetrics{
						"1001": weaponMetrics(1001, 4, "", ""),
						"1003": weaponMetrics(1003, 9, "", ""),
						"1004": weaponMetrics(1004, 6, "", ""),
						"1005": weaponMetrics(1005, 3, "", ""),
					},
				},
			},
		})

		sum, err := svc.ComputeSessionSummary(context.Background(), api.Session{
			ID:           "ses-1",
			CharacterID:  "char-1",
			AggregateIDs: []string{"agg-1", "agg-2"},
		})
		if err != nil {
			t.Fatalf("ComputeSessionSummary failed: %v", err)
		}

		if *sum.TotalMatches != 2 || *sum.Wins != 1 || *sum.Losses != 1 || *sum.WinRate != 0.5 {
			t.Fatalf("unexpected match record: total=%d wins=%d losses=%d wr=%f", *sum.TotalMatches, *sum.Wins, *sum.Losses, *sum.WinRate)
		}
		// kills=15, deaths=10, assists=10 -> KD=1.5, KDA=(15+5)/10=2.0
		if *sum.Kills != 15 || *sum.Deaths != 10 || *sum.Assists != 10 || *sum.KDRatio != 1.5 || *sum.KDARatio != 2.0 {
			t.Fatalf("unexpected combat stats: K=%d D=%d A=%d KD=%f KDA=%f", *sum.Kills, *sum.Deaths, *sum.Assists, *sum.KDRatio, *sum.KDARatio)
		}
		if !slices.Equal(*sum.ModesPlayed, []string{"Control", "Trials of Osiris"}) {
			t.Fatalf("unexpected modesPlayed: %v", *sum.ModesPlayed)
		}

		top := *sum.TopWeapons
		if len(top) != 5 {
			t.Fatalf("expected topWeapons capped at 5, got %d: %+v", len(top), top)
		}
		// 1001 (Rose): 12 kills, 1006 (Fallback Hand Cannon): 12 kills -> alphabetical tie-break puts Fallback Hand Cannon before Rose
		if *top[0].Name != "Fallback Hand Cannon" || *top[0].Kills != 12 || *top[0].Icon != "https://www.bungie.net/img/fb.png" {
			t.Fatalf("unexpected top[0]: %+v", top[0])
		}
		if *top[1].Name != "Rose" || *top[1].Kills != 12 || *top[1].Icon != "https://www.bungie.net/img/rose.png" {
			t.Fatalf("unexpected top[1]: %+v", top[1])
		}
		if *top[2].Name != "Igneous Hammer" || *top[2].Kills != 9 || *top[2].Icon != "https://cdn.example.com/igneous.png" {
			t.Fatalf("unexpected top[2]: %+v", top[2])
		}
		if *top[3].Name != "Cataphract GL3" || *top[3].Kills != 6 {
			t.Fatalf("unexpected top[3]: %+v", top[3])
		}
		if *top[4].Name != "Matador 64" || *top[4].Kills != 5 {
			t.Fatalf("unexpected top[4]: %+v", top[4])
		}
	})

	t.Run("zero deaths guards division by zero and unknown weapon fallback", func(t *testing.T) {
		aggSvc.seed(api.Aggregate{
			ID:         "agg-zero-d",
			ActivityID: "act-zero-d",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-zero-d",
				Mode:       new("Rumble"),
			},
			Performance: map[string]api.InstancePerformance{
				"char-1": {
					PlayerStats: api.PlayerStats{
						Standing: statVal(0),
						Kills:    statVal(7),
						Deaths:   statVal(0),
						Assists:  statVal(2),
					},
					Weapons: map[string]api.WeaponInstanceMetrics{
						"9999": weaponMetrics(9999, 7, "", ""),
					},
				},
			},
		})
		sum, err := svc.ComputeSessionSummary(context.Background(), api.Session{
			ID:           "ses-zero-d",
			CharacterID:  "char-1",
			AggregateIDs: []string{"agg-zero-d"},
		})
		if err != nil {
			t.Fatalf("ComputeSessionSummary failed: %v", err)
		}
		if *sum.KDRatio != 7.0 || *sum.KDARatio != 8.0 {
			t.Fatalf("expected KD=7.0 KDA=8.0 with 0 deaths, got KD=%f KDA=%f", *sum.KDRatio, *sum.KDARatio)
		}
		if len(*sum.TopWeapons) != 1 || *(*sum.TopWeapons)[0].Name != "Unknown Weapon" {
			t.Fatalf("expected Unknown Weapon fallback, got %+v", sum.TopWeapons)
		}
	})
}

func TestCleanupEmptyCompleted(t *testing.T) {
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	recentComplete := now.Add(-5 * time.Minute)
	oldComplete := now.Add(-20 * time.Minute)

	store := newMemorySessionStore()
	store.docs["pending-empty"] = &api.Session{ID: "pending-empty", Status: new(api.SessionPending)}
	store.docs["complete-empty-recent"] = &api.Session{ID: "complete-empty-recent", Status: new(api.SessionComplete), CompletedAt: &recentComplete}
	store.docs["complete-empty-old"] = &api.Session{ID: "complete-empty-old", Status: new(api.SessionComplete), CompletedAt: &oldComplete}
	store.docs["complete-empty-nil-ts"] = &api.Session{ID: "complete-empty-nil-ts", Status: new(api.SessionComplete)}
	store.docs["complete-nonempty"] = &api.Session{ID: "complete-nonempty", Status: new(api.SessionComplete), CompletedAt: &oldComplete, AggregateIDs: []string{"agg-1"}}

	svc := &service{
		store: store,
		nowFn: func() time.Time { return now },
	}

	cleanedDry, err := svc.CleanupEmptyCompleted(clog.NewContext(context.Background()), true)
	if err != nil {
		t.Fatalf("CleanupEmptyCompleted dryRun failed: %v", err)
	}
	if cleanedDry != 2 {
		t.Fatalf("expected 2 empty completed sessions outside grace window in dryRun, got %d", cleanedDry)
	}
	if store.writes.Load() != 0 {
		t.Fatalf("expected 0 writes in dryRun, got %d", store.writes.Load())
	}

	cleaned, err := svc.CleanupEmptyCompleted(clog.NewContext(context.Background()), false)
	if err != nil {
		t.Fatalf("CleanupEmptyCompleted failed: %v", err)
	}
	if cleaned != 2 {
		t.Fatalf("expected 2 deleted sessions, got %d", cleaned)
	}
	if _, exists := store.docs["complete-empty-old"]; exists {
		t.Fatal("expected complete-empty-old to be deleted")
	}
	if _, exists := store.docs["complete-empty-nil-ts"]; exists {
		t.Fatal("expected complete-empty-nil-ts to be deleted")
	}
	if _, exists := store.docs["complete-empty-recent"]; !exists {
		t.Fatal("expected complete-empty-recent inside 15m grace window to be preserved")
	}
	if _, exists := store.docs["pending-empty"]; !exists {
		t.Fatal("expected pending-empty to be preserved")
	}
	if _, exists := store.docs["complete-nonempty"]; !exists {
		t.Fatal("expected complete-nonempty to be preserved")
	}
}

func TestRunTick_ActiveSessionHappyPathAndSkipSave(t *testing.T) {
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	startedAt := now.Add(-30 * time.Minute)

	store := newMemorySessionStore()
	store.docs["ses-active"] = &api.Session{
		ID:          "ses-active",
		UserID:      "user-1",
		CharacterID: "char-1",
		StartedAt:   startedAt,
		Status:      new(api.SessionPending),
	}

	uSvc := &fakeSessionUserService{
		u: &user.User{
			ID:                  "user-1",
			PrimaryMembershipID: "12345",
			Memberships:         []user.Membership{{ID: "12345", Type: 3}},
		},
	}

	aggSvc := newFakeSessionAggregateService()
	aggSvc.seed(api.Aggregate{
		ID:         "agg-existing",
		ActivityID: "act-already-linked",
		SnapshotLinks: map[string]api.SnapshotLink{
			"char-1": {SessionID: new("ses-active")},
		},
	})

	d2Svc := &fakeSessionD2Service{
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-1": {
				{InstanceID: "act-priv", Activity: "Private Rumble", Location: "Burnout", IsPrivate: new(true), Period: now.Add(-1 * time.Minute)},
				{InstanceID: "act-new", Activity: "Control", Mode: new("Control"), Location: "Javelin-4", IsPrivate: new(false), Period: now.Add(-5 * time.Minute)},
				{InstanceID: "act-already-linked", Activity: "Control", Mode: new("Control"), Period: now.Add(-10 * time.Minute)},
				{InstanceID: "act-too-old", Activity: "Control", Mode: new("Control"), Period: startedAt.Add(-20 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-new": {
				"char-1": {
					PlayerStats: api.PlayerStats{
						Standing: statVal(0),
						Kills:    statVal(12),
						Deaths:   statVal(4),
						Assists:  statVal(2),
					},
					Weapons: map[string]api.WeaponInstanceMetrics{
						"1001": weaponMetrics(1001, 12, "Rose", "/img/rose.png"),
					},
				},
			},
		},
	}

	snapSvc := &fakeSessionSnapshotService{}
	svc := &service{
		store:            store,
		aggregateService: aggSvc,
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	resp, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{})
	if err != nil {
		t.Fatalf("RunTick failed: %v", err)
	}
	if resp.TotalSessions != 1 || resp.SessionsProcessed != 1 || resp.LoadoutsSaved != 1 || resp.ActivitiesFoundTotal != 2 || resp.AggregatesAddedTotal != 1 || resp.ErrorsTotal != 0 {
		t.Fatalf("unexpected RunTick response: %+v", resp)
	}

	updated := store.docs["ses-active"]
	if updated.LastSeenActivityID == nil || *updated.LastSeenActivityID != "act-new" {
		t.Fatalf("expected LastSeenActivityID='act-new', got %v", updated.LastSeenActivityID)
	}
	if updated.LastSeenTimestamp == nil || !updated.LastSeenTimestamp.Equal(now) {
		t.Fatalf("expected LastSeenTimestamp=%v, got %v", now, updated.LastSeenTimestamp)
	}
	if len(updated.AggregateIDs) != 1 {
		t.Fatalf("expected 1 linked aggregate ID, got %v", updated.AggregateIDs)
	}
	if updated.Summary == nil || *updated.Summary.TotalMatches != 1 || *updated.Summary.Kills != 12 {
		t.Fatalf("expected persisted session summary, got %+v", updated.Summary)
	}

	// Second tick with SkipSave: true and a newer activity should skip snapshot.Save.
	d2Svc.activitiesByChr["char-1"] = append([]api.ActivityHistory{
		{InstanceID: "act-new-2", Activity: "Control", Mode: new("Control"), Period: now.Add(-1 * time.Minute)},
	}, d2Svc.activitiesByChr["char-1"]...)
	d2Svc.perfsByAct["act-new-2"] = d2Svc.perfsByAct["act-new"]
	snapSvc.saveCalls.Store(0)

	resp2, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
	if err != nil {
		t.Fatalf("RunTick(SkipSave=true) failed: %v", err)
	}
	if snapSvc.saveCalls.Load() != 0 || resp2.LoadoutsSaved != 0 {
		t.Fatalf("expected 0 loadout saves when SkipSave=true, got calls=%d resp=%d", snapSvc.saveCalls.Load(), resp2.LoadoutsSaved)
	}
	if resp2.SessionsProcessed != 1 || resp2.AggregatesAddedTotal != 1 {
		t.Fatalf("expected activity to still link with SkipSave=true, got %+v", resp2)
	}
}

func TestRunTick_DryRunPerformsZeroWrites(t *testing.T) {
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	staleTime := now.Add(-3 * time.Hour)
	recentComplete := now.Add(-5 * time.Minute)
	oldComplete := now.Add(-1 * time.Hour)

	store := newMemorySessionStore()
	store.docs["ses-active"] = &api.Session{
		ID:          "ses-active",
		UserID:      "user-1",
		CharacterID: "char-active",
		StartedAt:   now.Add(-20 * time.Minute),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-stale-ended"] = &api.Session{
		ID:                "ses-stale-ended",
		UserID:            "user-1",
		CharacterID:       "char-stale-1",
		StartedAt:         now.Add(-4 * time.Hour),
		LastSeenTimestamp: &staleTime,
		AggregateIDs:      []string{"agg-old"},
		CompTracking:      &api.CompTracking{Enabled: true, Start: &api.CompRankSnapshot{Points: 5000}},
		Status:            new(api.SessionPending),
	}
	store.docs["ses-stale-deleted"] = &api.Session{
		ID:                "ses-stale-deleted",
		UserID:            "user-1",
		CharacterID:       "char-stale-2",
		StartedAt:         now.Add(-4 * time.Hour),
		LastSeenTimestamp: &staleTime,
		Status:            new(api.SessionPending),
	}
	store.docs["ses-inactive"] = &api.Session{
		ID:          "ses-inactive",
		UserID:      "user-1",
		CharacterID: "char-inactive",
		StartedAt:   now.Add(-3 * time.Hour),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-recent-complete"] = &api.Session{
		ID:           "ses-recent-complete",
		UserID:       "user-1",
		CharacterID:  "char-comp",
		StartedAt:    now.Add(-1 * time.Hour),
		CompletedAt:  &recentComplete,
		AggregateIDs: []string{"agg-comp"},
		CompTracking: &api.CompTracking{Enabled: true, Start: &api.CompRankSnapshot{Points: 5200}},
		Status:       new(api.SessionComplete),
	}
	store.docs["ses-old-empty-complete"] = &api.Session{
		ID:          "ses-old-empty-complete",
		UserID:      "user-1",
		CharacterID: "char-old",
		StartedAt:   now.Add(-2 * time.Hour),
		CompletedAt: &oldComplete,
		Status:      new(api.SessionComplete),
	}

	uSvc := &fakeSessionUserService{
		u: &user.User{
			ID:                  "user-1",
			PrimaryMembershipID: "12345",
			Memberships:         []user.Membership{{ID: "12345", Type: 3}},
		},
	}
	d2Svc := &fakeSessionD2Service{
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-active": {
				{InstanceID: "act-dry-1", Activity: "Control", Period: now.Add(-5 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-dry-1": {
				"char-active": {PlayerStats: api.PlayerStats{Kills: statVal(10)}},
			},
		},
	}
	snapSvc := &fakeSessionSnapshotService{}
	aggSvc := newFakeSessionAggregateService()

	svc := &service{
		store:            store,
		aggregateService: aggSvc,
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	resp, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{DryRun: true})
	if err != nil {
		t.Fatalf("RunTick(DryRun=true) failed: %v", err)
	}

	if store.writes.Load() != 0 || snapSvc.writes.Load() != 0 || aggSvc.writes.Load() != 0 {
		t.Fatalf("expected 0 writes in DryRun, got store=%d snap=%d agg=%d", store.writes.Load(), snapSvc.writes.Load(), aggSvc.writes.Load())
	}
	if !resp.DryRun {
		t.Fatal("expected resp.DryRun == true")
	}
	if resp.TotalSessions != 5 {
		t.Fatalf("expected 5 tick candidates (excluding old complete), got %d", resp.TotalSessions)
	}
	if resp.SessionsProcessed != 1 || resp.SessionsStaleEnded != 1 || resp.SessionsStaleDeleted != 1 || resp.SessionsInactiveDeleted != 1 || resp.SessionsSkipped != 1 {
		t.Fatalf("unexpected session outcome counts: %+v", resp)
	}
	if resp.ActivitiesFoundTotal != 1 || resp.AggregatesAddedTotal != 1 || resp.EmptySessionsCleaned != 1 {
		t.Fatalf("unexpected activity/cleanup counts: %+v", resp)
	}
	if resp.LoadoutsSaved != 0 || resp.CompFinalizedCount != 0 || resp.ErrorsTotal != 0 {
		t.Fatalf("expected 0 loadouts/comp/errors in dryRun, got %+v", resp)
	}
}

func TestRunTick_RecentlyCompletedLateActivityAndInProcessCompFinalize(t *testing.T) {
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	completedAt := now.Add(-5 * time.Minute)

	store := newMemorySessionStore()
	store.docs["ses-comp"] = &api.Session{
		ID:                 "ses-comp",
		UserID:             "user-1",
		CharacterID:        "char-1",
		StartedAt:          now.Add(-45 * time.Minute),
		CompletedAt:        &completedAt,
		LastSeenActivityID: new("act-prev"),
		Status:             new(api.SessionComplete),
		CompTracking: &api.CompTracking{
			Enabled: true,
			Start:   &api.CompRankSnapshot{Points: 5800},
		},
	}

	uSvc := &fakeSessionUserService{
		u: &user.User{
			ID:                  "user-1",
			PrimaryMembershipID: "12345",
			Memberships:         []user.Membership{{ID: "12345", Type: 3}},
		},
	}
	d2Svc := &fakeSessionD2Service{
		snapshots: []*api.CompRankSnapshot{
			{Points: 6050, DivisionName: new("Platinum II")},
		},
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-1": {
				{InstanceID: "act-late", Activity: "Competitive", Mode: new("Competitive"), Period: now.Add(-6 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-late": {
				"char-1": {
					PlayerStats: api.PlayerStats{
						Standing: statVal(0),
						Kills:    statVal(14),
						Deaths:   statVal(7),
						Assists:  statVal(4),
					},
				},
			},
		},
	}

	snapSvc := &fakeSessionSnapshotService{}
	svc := &service{
		store:            store,
		aggregateService: newFakeSessionAggregateService(),
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	resp, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{})
	if err != nil {
		t.Fatalf("RunTick failed: %v", err)
	}
	if resp.SessionsProcessed != 1 || resp.AggregatesAddedTotal != 1 || resp.CompFinalizedCount != 1 || resp.LoadoutsSaved != 0 || resp.ErrorsTotal != 0 {
		t.Fatalf("unexpected RunTick response: %+v", resp)
	}
	if snapSvc.saveCalls.Load() != 0 {
		t.Fatalf("expected 0 snapshot Save calls for already-completed session, got %d", snapSvc.saveCalls.Load())
	}

	ses := store.docs["ses-comp"]
	if ses.LastSeenActivityID == nil || *ses.LastSeenActivityID != "act-prev" {
		t.Fatalf("expected completed session LastSeenActivityID to remain 'act-prev', got %v", ses.LastSeenActivityID)
	}
	if ses.Summary == nil || *ses.Summary.TotalMatches != 1 {
		t.Fatalf("expected recomputed summary on completed session, got %+v", ses.Summary)
	}
	if ses.CompTracking == nil || ses.CompTracking.End == nil || ses.CompTracking.End.Points != 6050 {
		t.Fatalf("expected in-process comp finalization end=6050, got %+v", ses.CompTracking)
	}
	if ses.CompTracking.PointDelta == nil || *ses.CompTracking.PointDelta != 250 {
		t.Fatalf("expected PointDelta=250, got %+v", ses.CompTracking.PointDelta)
	}
}

func TestRunTick_StaleAndInactiveLifecycle(t *testing.T) {
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	staleTime := now.Add(-3 * time.Hour)

	store := newMemorySessionStore()
	store.docs["ses-1-stale-end"] = &api.Session{
		ID:                "ses-1-stale-end",
		UserID:            "user-1",
		CharacterID:       "char-1",
		StartedAt:         now.Add(-4 * time.Hour),
		LastSeenTimestamp: &staleTime,
		AggregateIDs:      []string{"agg-1"},
		Status:            new(api.SessionPending),
		CompTracking: &api.CompTracking{
			Enabled: true,
			Start:   &api.CompRankSnapshot{Points: 4000},
		},
	}
	store.docs["ses-2-stale-del"] = &api.Session{
		ID:                "ses-2-stale-del",
		UserID:            "user-1",
		CharacterID:       "char-2",
		StartedAt:         now.Add(-4 * time.Hour),
		LastSeenTimestamp: &staleTime,
		Status:            new(api.SessionPending),
	}
	store.docs["ses-3-inactive-del"] = &api.Session{
		ID:          "ses-3-inactive-del",
		UserID:      "user-1",
		CharacterID: "char-3",
		StartedAt:   now.Add(-3 * time.Hour),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-4-inactive-with-aggs"] = &api.Session{
		ID:           "ses-4-inactive-with-aggs",
		UserID:       "user-1",
		CharacterID:  "char-3b",
		StartedAt:    now.Add(-3 * time.Hour),
		AggregateIDs: []string{"agg-1"},
		Status:       new(api.SessionPending),
		CompTracking: &api.CompTracking{
			Enabled: true,
			Start:   &api.CompRankSnapshot{Points: 4500},
		},
	}
	store.docs["ses-5-seen-skip"] = &api.Session{
		ID:                 "ses-5-seen-skip",
		UserID:             "user-1",
		CharacterID:        "char-4",
		StartedAt:          now.Add(-30 * time.Minute),
		LastSeenActivityID: new("act-same"),
		Status:             new(api.SessionPending),
	}

	aggSvc := newFakeSessionAggregateService()
	aggSvc.seed(api.Aggregate{
		ID:         "agg-1",
		ActivityID: "act-1",
		ActivityDetails: api.ActivityHistory{
			InstanceID: "act-1",
			Mode:       new("Competitive"),
		},
		Performance: map[string]api.InstancePerformance{
			"char-1": {PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(8), Deaths: statVal(4)}},
		},
	})

	uSvc := &fakeSessionUserService{
		u: &user.User{
			ID:                  "user-1",
			PrimaryMembershipID: "12345",
			Memberships:         []user.Membership{{ID: "12345", Type: 3}},
		},
	}
	d2Svc := &fakeSessionD2Service{
		snapshots: []*api.CompRankSnapshot{{Points: 4120}, {Points: 4600}},
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-4": {
				{InstanceID: "act-same", Activity: "Control", Period: now.Add(-10 * time.Minute)},
			},
		},
		activityErrByCh: map[string]error{
			// Expiring sessions must still expire cleanly even if Bungie activity/loadout calls would error.
			"char-2": errors.New("no activities found"),
			"char-3": errors.New("no activities found"),
		},
	}
	snapSvc := &fakeSessionSnapshotService{
		saveErrByChar: map[string]error{
			"char-1": errors.New("bungie 503"),
			"char-3": errors.New("bungie 503"),
		},
	}

	svc := &service{
		store:            store,
		aggregateService: aggSvc,
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	resp, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{})
	if err != nil {
		t.Fatalf("RunTick failed: %v", err)
	}
	if resp.SessionsStaleEnded != 2 || resp.SessionsStaleDeleted != 1 || resp.SessionsInactiveDeleted != 1 || resp.SessionsSkipped != 1 || resp.CompFinalizedCount != 2 || resp.LoadoutsSaved != 1 || resp.ErrorsTotal != 0 {
		t.Fatalf("unexpected RunTick counts: %+v", resp)
	}
	if snapSvc.saveCalls.Load() != 1 {
		t.Fatalf("expected Save called only for active non-expiring session (1), got %d", snapSvc.saveCalls.Load())
	}

	ended := store.docs["ses-1-stale-end"]
	if ended == nil || ended.Status == nil || *ended.Status != api.SessionComplete {
		t.Fatalf("expected ses-1-stale-end to be completed, got %+v", ended)
	}
	if ended.CompletedBy == nil || ended.CompletedBy.ID != "system" {
		t.Fatalf("expected CompletedBy system, got %+v", ended.CompletedBy)
	}
	if ended.Summary == nil || *ended.Summary.TotalMatches != 1 {
		t.Fatalf("expected summary computed on auto-end, got %+v", ended.Summary)
	}
	if ended.CompTracking == nil || ended.CompTracking.End == nil || *ended.CompTracking.PointDelta != 120 {
		t.Fatalf("expected comp finalized on auto-end with delta=120, got %+v", ended.CompTracking)
	}
	endedInactive := store.docs["ses-4-inactive-with-aggs"]
	if endedInactive == nil || endedInactive.Status == nil || *endedInactive.Status != api.SessionComplete {
		t.Fatalf("expected inactive session with aggregates to be completed rather than deleted, got %+v", endedInactive)
	}
	if endedInactive.CompTracking == nil || endedInactive.CompTracking.End == nil || *endedInactive.CompTracking.PointDelta != 100 {
		t.Fatalf("expected comp finalized on ended inactive session with delta=100, got %+v", endedInactive.CompTracking)
	}
	if _, exists := store.docs["ses-2-stale-del"]; exists {
		t.Fatal("expected stale session with 0 aggregates to be deleted")
	}
	if _, exists := store.docs["ses-3-inactive-del"]; exists {
		t.Fatal("expected inactive session with 0 aggregates to be deleted")
	}
}

func TestRunTick_MissingPerformanceWarningAndPartialFailureResilience(t *testing.T) {
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)

	store := newMemorySessionStore()
	store.docs["ses-1-missing-perf"] = &api.Session{
		ID:          "ses-1-missing-perf",
		UserID:      "user-ok",
		CharacterID: "char-missing-perf",
		StartedAt:   now.Add(-20 * time.Minute),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-2-bad-user"] = &api.Session{
		ID:          "ses-2-bad-user",
		UserID:      "user-missing",
		CharacterID: "char-bad-user",
		StartedAt:   now.Add(-20 * time.Minute),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-3-save-fail"] = &api.Session{
		ID:          "ses-3-save-fail",
		UserID:      "user-ok",
		CharacterID: "char-save-fail",
		StartedAt:   now.Add(-20 * time.Minute),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-4-healthy"] = &api.Session{
		ID:          "ses-4-healthy",
		UserID:      "user-ok",
		CharacterID: "char-healthy",
		StartedAt:   now.Add(-20 * time.Minute),
		Status:      new(api.SessionPending),
	}

	uSvc := &fakeSessionUserService{
		users: map[string]*user.User{
			"user-ok": {
				ID:                  "user-ok",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		},
	}
	d2Svc := &fakeSessionD2Service{
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-missing-perf": {
				{InstanceID: "act-no-char", Activity: "Control", Period: now.Add(-5 * time.Minute)},
			},
			"char-healthy": {
				{InstanceID: "act-healthy", Activity: "Control", Mode: new("Control"), Period: now.Add(-5 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-no-char": {
				"other-character": {PlayerStats: api.PlayerStats{Kills: statVal(5)}},
			},
			"act-healthy": {
				"char-healthy": {PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(9), Deaths: statVal(3)}},
			},
		},
	}
	snapSvc := &fakeSessionSnapshotService{
		saveErrByChar: map[string]error{
			"char-save-fail": errors.New("bungie timeout"),
		},
	}

	svc := &service{
		store:            store,
		aggregateService: newFakeSessionAggregateService(),
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	ctx := clog.NewContext(context.Background())
	resp, err := svc.RunTick(ctx, TickOptions{})
	if err != nil {
		t.Fatalf("RunTick should return response despite per-session errors, got err: %v", err)
	}

	if resp.TotalSessions != 4 {
		t.Fatalf("expected TotalSessions=4, got %d", resp.TotalSessions)
	}
	if resp.SessionsSkipped != 1 || resp.SessionsErrored != 1 || resp.LoadoutsFailed != 1 || resp.LoadoutsSaved != 2 || resp.SessionsProcessed != 1 || resp.AggregatesAddedTotal != 1 || resp.ErrorsTotal != 2 {
		t.Fatalf("unexpected RunTick counts: %+v", resp)
	}

	bag := clog.BagFromContext(ctx)
	if !bag.HasWarning() {
		t.Fatal("expected canonical FieldBag warning to propagate from missing performance entry")
	}
	assertAllWarningsAreStrings(t, bag.Attrs())
}

