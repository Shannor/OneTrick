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

type lastSnapshotRecord struct {
	snapshotID string
	hash       string
	recordedAt time.Time
}

type memorySessionStore struct {
	mu                       sync.Mutex
	docs                     map[string]*api.Session
	updatedAt                map[string]time.Time
	lastSnapshots            map[string]lastSnapshotRecord
	seq                      int
	getDocCalls              atomic.Int32
	writes                   atomic.Int32
	lastCompletedWindowCount atomic.Int32
}

func newMemorySessionStore() *memorySessionStore {
	return &memorySessionStore{
		docs:          make(map[string]*api.Session),
		updatedAt:     make(map[string]time.Time),
		lastSnapshots: make(map[string]lastSnapshotRecord),
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

func (m *memorySessionStore) getDocWithMeta(_ context.Context, sessionID string) (*tickCandidate, error) {
	m.getDocCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.docs[sessionID]
	if !ok {
		return nil, ErrNotFound
	}
	var upd *time.Time
	if ts, exists := m.updatedAt[sessionID]; exists {
		t := ts
		upd = &t
	}
	rec := m.lastSnapshots[sessionID]
	return &tickCandidate{
		session:                cloneSession(s),
		updatedAt:              upd,
		lastSnapshotID:         rec.snapshotID,
		lastSnapshotHash:       rec.hash,
		lastSnapshotRecordedAt: rec.recordedAt,
	}, nil
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

func (m *memorySessionStore) listCompletedInWindow(_ context.Context, since, until time.Time) ([]api.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]api.Session, 0)
	for _, s := range m.docs {
		if s.Status == nil || *s.Status != api.SessionComplete {
			continue
		}
		if s.CompletedAt == nil {
			continue
		}
		if s.CompletedAt.Before(since) || s.CompletedAt.After(until) {
			continue
		}
		out = append(out, cloneSession(s))
	}
	m.lastCompletedWindowCount.Store(int32(len(out)))
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
		rec := m.lastSnapshots[id]
		out = append(out, tickCandidate{
			session:                cloneSession(s),
			updatedAt:              upd,
			lastSnapshotID:         rec.snapshotID,
			lastSnapshotHash:       rec.hash,
			lastSnapshotRecordedAt: rec.recordedAt,
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

func (m *memorySessionStore) touchLastSeen(_ context.Context, sessionID string, seenAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	s, ok := m.docs[sessionID]
	if !ok {
		return ErrNotFound
	}
	s.LastSeenTimestamp = &seenAt
	m.updatedAt[sessionID] = seenAt
	return nil
}

func (m *memorySessionStore) setLastSnapshot(_ context.Context, sessionID, snapshotID, snapshotHash string, recordedAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	if _, ok := m.docs[sessionID]; !ok {
		return ErrNotFound
	}
	m.lastSnapshots[sessionID] = lastSnapshotRecord{
		snapshotID: snapshotID,
		hash:       snapshotHash,
		recordedAt: recordedAt,
	}
	return nil
}

func (m *memorySessionStore) deleteDoc(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	delete(m.docs, sessionID)
	delete(m.updatedAt, sessionID)
	delete(m.lastSnapshots, sessionID)
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
	perfCalls       atomic.Int32
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
	f.perfCalls.Add(1)
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
	checkInFn     func(userID, membershipID, characterID string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error)
	checkInCtxFn  func(ctx context.Context, userID, membershipID, characterID string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error)
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

func (f *fakeSessionSnapshotService) SaveSessionCheckIn(ctx context.Context, userID, membershipID, characterID string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
	f.saveCalls.Add(1)
	if err, ok := f.saveErrByChar[characterID]; ok && err != nil {
		return snapshot.SessionCheckInResult{}, err
	}
	if f.checkInCtxFn != nil {
		res, err := f.checkInCtxFn(ctx, userID, membershipID, characterID, opts)
		if err == nil && res.HistoryRecorded {
			f.writes.Add(1)
		}
		return res, err
	}
	if f.checkInFn != nil {
		res, err := f.checkInFn(userID, membershipID, characterID, opts)
		if err == nil && res.HistoryRecorded {
			f.writes.Add(1)
		}
		return res, err
	}
	f.writes.Add(1)
	return snapshot.SessionCheckInResult{
		SnapshotID:      "snap-" + characterID,
		SnapshotHash:    "hash-" + characterID,
		HistoryRecorded: true,
		RecordedAt:      time.Now(),
	}, nil
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
	nowFn      func() time.Time
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
		if link.SnapshotID != nil && *link.SnapshotID != "" && !slices.Contains(existing.SnapshotIds, *link.SnapshotID) {
			existing.SnapshotIds = append(existing.SnapshotIds, *link.SnapshotID)
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
	var snapshotIDs []string
	if link.SnapshotID != nil && *link.SnapshotID != "" {
		snapshotIDs = []string{*link.SnapshotID}
	}
	createdAt := time.Now()
	if f.nowFn != nil {
		createdAt = f.nowFn()
	}
	agg := &api.Aggregate{
		ID:              id,
		ActivityID:      activityHistory.InstanceID,
		ActivityDetails: activityHistory,
		CharacterIds:    []string{characterID},
		SessionIds:      sessionIDs,
		SnapshotIds:     snapshotIDs,
		SnapshotLinks:   map[string]api.SnapshotLink{characterID: link},
		Performance:     map[string]api.InstancePerformance{characterID: performance},
		CreatedAt:       createdAt,
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

func findIntAttr(attrs []slog.Attr, key string) (int64, bool) {
	var walk func([]slog.Attr) (int64, bool)
	walk = func(list []slog.Attr) (int64, bool) {
		for _, a := range list {
			if a.Key == key && a.Value.Kind() == slog.KindInt64 {
				return a.Value.Int64(), true
			}
			if a.Value.Kind() == slog.KindGroup {
				if v, ok := walk(a.Value.Group()); ok {
					return v, true
				}
			}
		}
		return 0, false
	}
	return walk(attrs)
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
	inWindowComplete := now.Add(-20 * time.Minute)
	historicalComplete := now.Add(-90 * time.Minute)

	store := newMemorySessionStore()
	store.docs["pending-empty"] = &api.Session{ID: "pending-empty", Status: new(api.SessionPending)}
	store.docs["complete-empty-recent"] = &api.Session{ID: "complete-empty-recent", Status: new(api.SessionComplete), CompletedAt: &recentComplete}
	store.docs["complete-empty-in-window"] = &api.Session{ID: "complete-empty-in-window", Status: new(api.SessionComplete), CompletedAt: &inWindowComplete}
	store.docs["complete-empty-historical"] = &api.Session{ID: "complete-empty-historical", Status: new(api.SessionComplete), CompletedAt: &historicalComplete}
	store.docs["complete-empty-nil-ts"] = &api.Session{ID: "complete-empty-nil-ts", Status: new(api.SessionComplete)}
	store.docs["complete-nonempty-in-window"] = &api.Session{ID: "complete-nonempty-in-window", Status: new(api.SessionComplete), CompletedAt: &inWindowComplete, AggregateIDs: []string{"agg-1"}}
	store.docs["complete-nonempty-historical"] = &api.Session{ID: "complete-nonempty-historical", Status: new(api.SessionComplete), CompletedAt: &historicalComplete, AggregateIDs: []string{"agg-2"}}

	svc := &service{
		store: store,
		nowFn: func() time.Time { return now },
	}

	cleanedDry, err := svc.CleanupEmptyCompleted(clog.NewContext(context.Background()), true)
	if err != nil {
		t.Fatalf("CleanupEmptyCompleted dryRun failed: %v", err)
	}
	if cleanedDry != 1 {
		t.Fatalf("expected 1 empty completed session in cleanup window in dryRun, got %d", cleanedDry)
	}
	if store.lastCompletedWindowCount.Load() != 2 {
		t.Fatalf("expected bounded query to read only 2 in-window sessions, got %d", store.lastCompletedWindowCount.Load())
	}
	if store.writes.Load() != 0 {
		t.Fatalf("expected 0 writes in dryRun, got %d", store.writes.Load())
	}

	cleaned, err := svc.CleanupEmptyCompleted(clog.NewContext(context.Background()), false)
	if err != nil {
		t.Fatalf("CleanupEmptyCompleted failed: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("expected 1 deleted session, got %d", cleaned)
	}
	if _, exists := store.docs["complete-empty-in-window"]; exists {
		t.Fatal("expected complete-empty-in-window to be deleted")
	}
	if _, exists := store.docs["complete-empty-recent"]; !exists {
		t.Fatal("expected complete-empty-recent inside 15m grace window to be preserved")
	}
	if _, exists := store.docs["complete-nonempty-in-window"]; !exists {
		t.Fatal("expected complete-nonempty-in-window to be preserved")
	}
	if _, exists := store.docs["complete-nonempty-historical"]; !exists {
		t.Fatal("expected complete-nonempty-historical outside lookback window to be untouched")
	}
	if _, exists := store.docs["complete-empty-historical"]; !exists {
		t.Fatal("expected complete-empty-historical outside 60m lookback window not to be scanned")
	}
	if _, exists := store.docs["complete-empty-nil-ts"]; !exists {
		t.Fatal("expected complete-empty-nil-ts without completedAt not to be scanned")
	}
	if _, exists := store.docs["pending-empty"]; !exists {
		t.Fatal("expected pending-empty to be preserved")
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
				"char-healthy": {
					PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(9), Deaths: statVal(3)},
					Weapons: map[string]api.WeaponInstanceMetrics{
						"1001": weaponMetrics(1001, 9, "Rose", "/img/rose.png"),
					},
				},
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

func TestRunTick_SnapshotHistoryThrottling(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	now := t0

	store := newMemorySessionStore()
	store.docs["ses-throttle"] = &api.Session{
		ID:          "ses-throttle",
		UserID:      "user-1",
		CharacterID: "char-1",
		StartedAt:   t0,
		Status:      new(api.SessionPending),
	}

	uSvc := &fakeSessionUserService{
		u: &user.User{
			ID:                  "user-1",
			PrimaryMembershipID: "12345",
			Memberships:         []user.Membership{{ID: "12345", Type: 3}},
		},
	}

	currentHash := "hash-loadout-a"
	currentSnapID := "snap-a"
	var seenOpts []snapshot.SessionCheckInOptions

	snapSvc := &fakeSessionSnapshotService{
		checkInFn: func(_, _, _ string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
			seenOpts = append(seenOpts, opts)
			if opts.LastSnapshotID != "" &&
				opts.LastSnapshotHash == currentHash &&
				!opts.LastSnapshotRecordedAt.IsZero() &&
				now.Sub(opts.LastSnapshotRecordedAt) < opts.HeartbeatInterval {
				return snapshot.SessionCheckInResult{
					SnapshotID:      opts.LastSnapshotID,
					SnapshotHash:    currentHash,
					HistoryRecorded: false,
					RecordedAt:      opts.LastSnapshotRecordedAt,
				}, nil
			}
			return snapshot.SessionCheckInResult{
				SnapshotID:      currentSnapID,
				SnapshotHash:    currentHash,
				HistoryRecorded: true,
				RecordedAt:      now,
			}, nil
		},
	}

	svc := &service{
		store:            store,
		aggregateService: newFakeSessionAggregateService(),
		userService:      uSvc,
		d2Service:        &fakeSessionD2Service{},
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	// Tick 1 (t=0m): initial session check-in records history and persists last-snapshot metadata.
	ctx1 := clog.NewContext(context.Background())
	resp1, err := svc.RunTick(ctx1, TickOptions{})
	if err != nil {
		t.Fatalf("Tick 1 failed: %v", err)
	}
	if resp1.LoadoutsSaved != 1 || snapSvc.writes.Load() != 1 || store.writes.Load() != 1 {
		t.Fatalf("Tick 1 expected 1 loadout save & 1 store write, got resp=%+v snapWrites=%d storeWrites=%d", resp1, snapSvc.writes.Load(), store.writes.Load())
	}
	if v, ok := findIntAttr(clog.BagFromContext(ctx1).Attrs(), "loadoutsThrottled"); !ok || v != 0 {
		t.Fatalf("Tick 1 expected loadoutsThrottled=0 in wide event, got ok=%v v=%d", ok, v)
	}
	if seenOpts[0].HeartbeatInterval != snapshotHeartbeatPeriod || seenOpts[0].LastSnapshotHash != "" {
		t.Fatalf("Tick 1 unexpected checkIn options: %+v", seenOpts[0])
	}
	if rec := store.lastSnapshots["ses-throttle"]; rec.snapshotID != "snap-a" || rec.hash != "hash-loadout-a" || !rec.recordedAt.Equal(t0) {
		t.Fatalf("Tick 1 expected persisted lastSnapshot metadata, got %+v", rec)
	}

	// Tick 2 (t=10m, unchanged loadout): skips history, skips session store writes, and logs loadoutsThrottled=1.
	now = t0.Add(10 * time.Minute)
	ctx2 := clog.NewContext(context.Background())
	resp2, err := svc.RunTick(ctx2, TickOptions{})
	if err != nil {
		t.Fatalf("Tick 2 failed: %v", err)
	}
	if resp2.LoadoutsSaved != 0 || snapSvc.writes.Load() != 1 || store.writes.Load() != 1 {
		t.Fatalf("Tick 2 expected throttled skip (0 new writes), got resp=%+v snapWrites=%d storeWrites=%d", resp2, snapSvc.writes.Load(), store.writes.Load())
	}
	if v, ok := findIntAttr(clog.BagFromContext(ctx2).Attrs(), "loadoutsThrottled"); !ok || v != 1 {
		t.Fatalf("Tick 2 expected loadoutsThrottled=1 in wide event, got ok=%v v=%d", ok, v)
	}
	if seenOpts[1].LastSnapshotID != "snap-a" || seenOpts[1].LastSnapshotHash != "hash-loadout-a" || !seenOpts[1].LastSnapshotRecordedAt.Equal(t0) {
		t.Fatalf("Tick 2 expected prior snapshot metadata passed in, got %+v", seenOpts[1])
	}

	// Tick 3 (t=15m, loadout hash changes): immediately records new history and updates metadata.
	now = t0.Add(15 * time.Minute)
	currentHash = "hash-loadout-b"
	currentSnapID = "snap-b"
	resp3, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{})
	if err != nil {
		t.Fatalf("Tick 3 failed: %v", err)
	}
	if resp3.LoadoutsSaved != 1 || snapSvc.writes.Load() != 2 || store.writes.Load() != 2 {
		t.Fatalf("Tick 3 expected immediate save on hash change, got resp=%+v snapWrites=%d storeWrites=%d", resp3, snapSvc.writes.Load(), store.writes.Load())
	}
	if rec := store.lastSnapshots["ses-throttle"]; rec.snapshotID != "snap-b" || rec.hash != "hash-loadout-b" || !rec.recordedAt.Equal(now) {
		t.Fatalf("Tick 3 expected updated lastSnapshot metadata, got %+v", rec)
	}

	// Tick 4 (t=44m58s, 29m58s after Tick 3): 28m cushion absorbs cron dispatch jitter so Tick 6 emits heartbeat.
	now = t0.Add(45*time.Minute - 2*time.Second)
	ctx4 := clog.NewContext(context.Background())
	resp4, err := svc.RunTick(ctx4, TickOptions{})
	if err != nil {
		t.Fatalf("Tick 4 failed: %v", err)
	}
	if resp4.LoadoutsSaved != 1 || snapSvc.writes.Load() != 3 || store.writes.Load() != 3 {
		t.Fatalf("Tick 4 expected heartbeat save with cron jitter cushion, got resp=%+v snapWrites=%d storeWrites=%d", resp4, snapSvc.writes.Load(), store.writes.Load())
	}
	if v, ok := findIntAttr(clog.BagFromContext(ctx4).Attrs(), "loadoutsThrottled"); !ok || v != 0 {
		t.Fatalf("Tick 4 expected loadoutsThrottled=0 on heartbeat tick, got ok=%v v=%d", ok, v)
	}
	if rec := store.lastSnapshots["ses-throttle"]; !rec.recordedAt.Equal(now) {
		t.Fatalf("Tick 4 expected updated recordedAt=%v, got %+v", now, rec)
	}
}

type unfilteredWindowSessionStore struct {
	*memorySessionStore
}

func (u *unfilteredWindowSessionStore) listCompletedInWindow(ctx context.Context, _, _ time.Time) ([]api.Session, error) {
	completed := api.SessionComplete
	return u.memorySessionStore.listDocs(ctx, nil, nil, &completed)
}

func TestCleanupEmptyCompleted_SymmetricGuard(t *testing.T) {
	now := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	tooNew := now.Add(-5 * time.Minute)
	inWindow := now.Add(-20 * time.Minute)
	tooOld := now.Add(-90 * time.Minute)

	base := newMemorySessionStore()
	base.docs["empty-nil"] = &api.Session{ID: "empty-nil", Status: new(api.SessionComplete)}
	base.docs["empty-too-new"] = &api.Session{ID: "empty-too-new", Status: new(api.SessionComplete), CompletedAt: &tooNew}
	base.docs["empty-too-old"] = &api.Session{ID: "empty-too-old", Status: new(api.SessionComplete), CompletedAt: &tooOld}
	base.docs["empty-in-window"] = &api.Session{ID: "empty-in-window", Status: new(api.SessionComplete), CompletedAt: &inWindow}

	svc := &service{
		store: &unfilteredWindowSessionStore{memorySessionStore: base},
		nowFn: func() time.Time { return now },
	}

	cleaned, err := svc.CleanupEmptyCompleted(clog.NewContext(context.Background()), false)
	if err != nil {
		t.Fatalf("CleanupEmptyCompleted failed: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("expected only 1 in-window session deleted, got %d", cleaned)
	}
	if _, exists := base.docs["empty-in-window"]; exists {
		t.Fatal("expected empty-in-window to be deleted")
	}
	for _, preserved := range []string{"empty-nil", "empty-too-new", "empty-too-old"} {
		if _, exists := base.docs[preserved]; !exists {
			t.Fatalf("expected %s to be preserved by symmetric service guard", preserved)
		}
	}
}

func TestRunTick_CompletedSessionRefreshesIncompletePGCR(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 4, 0, 5, 0, time.UTC)
	now := t0
	completedAt := t0.Add(-40 * time.Second)

	store := newMemorySessionStore()
	store.docs["ses-completed"] = &api.Session{
		ID:          "ses-completed",
		UserID:      "user-1",
		CharacterID: "char-1",
		StartedAt:   t0.Add(-45 * time.Minute),
		CompletedAt: &completedAt,
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
			"char-1": {
				{InstanceID: "act-17289048887", Activity: "Competitive", Mode: new("Zone Control"), Period: t0.Add(-15 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-17289048887": {
				// Tick 1: Bungie PGCR returned entry.Values before entry.Extended (extra/weapons) was ready.
				"char-1": {
					PlayerStats: api.PlayerStats{
						Standing: statVal(0),
						Kills:    statVal(20),
						Deaths:   statVal(8),
						Assists:  statVal(4),
					},
				},
			},
		},
	}

	snapSvc := &fakeSessionSnapshotService{
		findBestFitFn: func(_, characterID string, _ time.Time, weapons map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error) {
			if len(weapons) == 0 {
				return nil, &api.SnapshotLink{
					CharacterID:      characterID,
					ConfidenceLevel:  api.NoMatchConfidenceLevel,
					ConfidenceSource: api.SystemConfidenceSource,
					CreatedAt:        now,
				}, nil
			}
			snapID := "snap-expected"
			return &api.CharacterSnapshot{ID: snapID}, &api.SnapshotLink{
				CharacterID:      characterID,
				SnapshotID:       &snapID,
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			}, nil
		},
	}

	aggSvc := newFakeSessionAggregateService()
	aggSvc.nowFn = func() time.Time { return now }

	svc := &service{
		store:            store,
		aggregateService: aggSvc,
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	// Tick 1: persists match immediately with noMatch and logs warning.
	ctx1 := clog.NewContext(context.Background())
	resp1, err := svc.RunTick(ctx1, TickOptions{})
	if err != nil {
		t.Fatalf("Tick 1 failed: %v", err)
	}
	if resp1.SessionsProcessed != 1 || resp1.AggregatesAddedTotal != 1 || resp1.ErrorsTotal != 0 {
		t.Fatalf("Tick 1 unexpected response: %+v", resp1)
	}
	if !clog.BagFromContext(ctx1).HasWarning() {
		t.Fatal("Tick 1 expected incomplete_pgcr_extended warning")
	}
	assertAllWarningsAreStrings(t, clog.BagFromContext(ctx1).Attrs())

	aggAfterTick1 := aggSvc.byActivity["act-17289048887"]
	if aggAfterTick1 == nil {
		t.Fatal("Tick 1 expected aggregate to be created")
	}
	if aggAfterTick1.SnapshotLinks["char-1"].SnapshotID != nil || aggAfterTick1.SnapshotLinks["char-1"].ConfidenceLevel != api.NoMatchConfidenceLevel {
		t.Fatalf("Tick 1 expected noMatch without snapshot, got %+v", aggAfterTick1.SnapshotLinks["char-1"])
	}

	// Tick 2 (+5m, within 15m completed grace window): Bungie PGCR extended stats & weapons are now populated.
	now = t0.Add(5 * time.Minute)
	extraStats := map[string]api.UniqueStatValue{
		"precisionKills": {Basic: api.StatsValuePair{Value: new(float64(9))}},
	}
	d2Svc.perfsByAct["act-17289048887"] = map[string]api.InstancePerformance{
		"char-1": {
			PlayerStats: api.PlayerStats{
				Standing: statVal(0),
				Kills:    statVal(20),
				Deaths:   statVal(8),
				Assists:  statVal(4),
			},
			Extra: &extraStats,
			Weapons: map[string]api.WeaponInstanceMetrics{
				"2819552809": weaponMetrics(2819552809, 11, "Energy Pulse", "/img/pulse.png"),
				"1432682459": weaponMetrics(1432682459, 7, "Kinetic Shotgun", "/img/shotty.png"),
			},
		},
	}

	ctx2 := clog.NewContext(context.Background())
	resp2, err := svc.RunTick(ctx2, TickOptions{})
	if err != nil {
		t.Fatalf("Tick 2 failed: %v", err)
	}
	if resp2.SessionsProcessed != 1 || resp2.AggregatesAddedTotal != 0 || resp2.ErrorsTotal != 0 {
		t.Fatalf("Tick 2 unexpected response (expected refresh with 0 newly added aggregates): %+v", resp2)
	}
	if v, ok := findIntAttr(clog.BagFromContext(ctx2).Attrs(), "aggregatesRefreshed"); !ok || v != 1 {
		t.Fatalf("Tick 2 expected aggregatesRefreshed=1, got ok=%v v=%d", ok, v)
	}

	aggAfterTick2 := aggSvc.byActivity["act-17289048887"]
	link := aggAfterTick2.SnapshotLinks["char-1"]
	if link.SnapshotID == nil || *link.SnapshotID != "snap-expected" || link.ConfidenceLevel != api.HighConfidenceLevel {
		t.Fatalf("Tick 2 expected refreshed High confidence link to snap-expected, got %+v", link)
	}
	if !slices.Contains(aggAfterTick2.SnapshotIds, "snap-expected") {
		t.Fatalf("Tick 2 expected snapshotIds to contain snap-expected, got %v", aggAfterTick2.SnapshotIds)
	}
	if len(aggAfterTick2.Performance["char-1"].Weapons) != 2 || aggAfterTick2.Performance["char-1"].Extra == nil {
		t.Fatalf("Tick 2 expected refreshed weapons and extra stats, got %+v", aggAfterTick2.Performance["char-1"])
	}

	ses := store.docs["ses-completed"]
	if len(ses.AggregateIDs) != 1 {
		t.Fatalf("Tick 2 must not duplicate AggregateIDs on refresh, got %v", ses.AggregateIDs)
	}
	if ses.Summary == nil || *ses.Summary.TotalMatches != 1 || *ses.Summary.Kills != 20 {
		t.Fatalf("Tick 2 expected recomputed summary without double-counting, got %+v", ses.Summary)
	}
	if ses.Summary.TopWeapons == nil || len(*ses.Summary.TopWeapons) != 2 {
		t.Fatalf("Tick 2 expected TopWeapons populated after refresh, got %+v", ses.Summary.TopWeapons)
	}

	// Tick 3 (+10m): already refreshed and complete, so it must be skipped with zero PGCR calls or writes.
	now = t0.Add(10 * time.Minute)
	d2Svc.perfCalls.Store(0)
	writesBeforeTick3 := aggSvc.writes.Load()

	resp3, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{})
	if err != nil {
		t.Fatalf("Tick 3 failed: %v", err)
	}
	if resp3.SessionsSkipped != 1 || resp3.SessionsProcessed != 0 || d2Svc.perfCalls.Load() != 0 || aggSvc.writes.Load() != writesBeforeTick3 {
		t.Fatalf("Tick 3 expected clean skip with 0 PGCR calls/writes, got resp=%+v perfCalls=%d writes=%d->%d", resp3, d2Svc.perfCalls.Load(), writesBeforeTick3, aggSvc.writes.Load())
	}
}

func TestRunTick_ActiveSessionIncompletePGCRRetryAndWindowExpiry(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC)
	now := t0

	store := newMemorySessionStore()
	store.docs["ses-active"] = &api.Session{
		ID:          "ses-active",
		UserID:      "user-1",
		CharacterID: "char-1",
		StartedAt:   t0.Add(-20 * time.Minute),
		Status:      new(api.SessionPending),
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
			"char-1": {
				{InstanceID: "act-inc", Activity: "Control", Mode: new("Control"), Period: t0.Add(-5 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-inc": {
				"char-1": {
					PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(15), Deaths: statVal(5)},
				},
			},
		},
	}

	snapSvc := &fakeSessionSnapshotService{
		findBestFitFn: func(_, characterID string, _ time.Time, weapons map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error) {
			if len(weapons) == 0 {
				return nil, &api.SnapshotLink{
					CharacterID:      characterID,
					ConfidenceLevel:  api.NoMatchConfidenceLevel,
					ConfidenceSource: api.SystemConfidenceSource,
					CreatedAt:        now,
				}, nil
			}
			snapID := "snap-active"
			return &api.CharacterSnapshot{ID: snapID}, &api.SnapshotLink{
				CharacterID:      characterID,
				SnapshotID:       &snapID,
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			}, nil
		},
	}

	aggSvc := newFakeSessionAggregateService()
	aggSvc.nowFn = func() time.Time { return now }

	svc := &service{
		store:            store,
		aggregateService: aggSvc,
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  snapSvc,
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	// Tick 1 (t=0): incomplete PGCR saved, but LastSeenActivityID is NOT advanced yet.
	resp1, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
	if err != nil {
		t.Fatalf("Tick 1 failed: %v", err)
	}
	if resp1.SessionsProcessed != 1 || resp1.AggregatesAddedTotal != 1 {
		t.Fatalf("Tick 1 unexpected response: %+v", resp1)
	}
	if store.docs["ses-active"].LastSeenActivityID != nil {
		t.Fatalf("Tick 1 should hold LastSeenActivityID while PGCR is incomplete, got %v", *store.docs["ses-active"].LastSeenActivityID)
	}
	if ts := store.docs["ses-active"].LastSeenTimestamp; ts == nil || !ts.Equal(t0) {
		t.Fatalf("Tick 1 expected LastSeenTimestamp=%v even while holding LastSeenActivityID, got %v", t0, ts)
	}

	// Tick 2 (t=5m): Bungie STILL returns incomplete PGCR -> no redundant aggregate write, LastSeenActivityID still held.
	now = t0.Add(5 * time.Minute)
	aggWritesBeforeTick2 := aggSvc.writes.Load()
	resp2, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
	if err != nil {
		t.Fatalf("Tick 2 failed: %v", err)
	}
	if resp2.SessionsSkipped != 1 || resp2.SessionsProcessed != 0 || aggSvc.writes.Load() != aggWritesBeforeTick2 {
		t.Fatalf("Tick 2 expected skip with no aggregate rewrite, got resp=%+v aggWrites=%d->%d", resp2, aggWritesBeforeTick2, aggSvc.writes.Load())
	}
	if store.docs["ses-active"].LastSeenActivityID != nil {
		t.Fatalf("Tick 2 should continue holding LastSeenActivityID while still incomplete, got %v", *store.docs["ses-active"].LastSeenActivityID)
	}

	// Tick 3 (t=10m): Bungie returns complete PGCR -> refreshes aggregate AND advances LastSeenActivityID.
	now = t0.Add(10 * time.Minute)
	extraStats := map[string]api.UniqueStatValue{
		"precisionKills": {Basic: api.StatsValuePair{Value: new(float64(6))}},
	}
	d2Svc.perfsByAct["act-inc"] = map[string]api.InstancePerformance{
		"char-1": {
			PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(15), Deaths: statVal(5)},
			Extra:       &extraStats,
			Weapons: map[string]api.WeaponInstanceMetrics{
				"1001": weaponMetrics(1001, 15, "Rose", "/img/rose.png"),
			},
		},
	}

	resp3, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
	if err != nil {
		t.Fatalf("Tick 3 failed: %v", err)
	}
	if resp3.SessionsProcessed != 1 || resp3.AggregatesAddedTotal != 0 {
		t.Fatalf("Tick 3 unexpected response: %+v", resp3)
	}
	ses := store.docs["ses-active"]
	if ses.LastSeenActivityID == nil || *ses.LastSeenActivityID != "act-inc" {
		t.Fatalf("Tick 3 expected LastSeenActivityID to advance to 'act-inc', got %v", ses.LastSeenActivityID)
	}
	if len(ses.AggregateIDs) != 1 || ses.Summary == nil || ses.Summary.TopWeapons == nil || len(*ses.Summary.TopWeapons) != 1 {
		t.Fatalf("Tick 3 expected deduplicated AggregateIDs and populated TopWeapons, got aggs=%v summary=%+v", ses.AggregateIDs, ses.Summary)
	}
}

func TestRunTick_PendingRetryUpdatesLastSeenAndPreventsPrematureExpiry(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 5, 30, 0, 0, time.UTC)

	tests := []struct {
		name              string
		setupSession      func(store *memorySessionStore, aggSvc *fakeSessionAggregateService)
		wantHeldActivity  *string
		activities        []api.ActivityHistory
	}{
		{
			name: "inactive session near cutoff does not expire after partial PGCR",
			setupSession: func(store *memorySessionStore, _ *fakeSessionAggregateService) {
				store.docs["ses-near-cutoff"] = &api.Session{
					ID:          "ses-near-cutoff",
					UserID:      "user-1",
					CharacterID: "char-1",
					StartedAt:   t0.Add(-118 * time.Minute),
					Status:      new(api.SessionPending),
				}
			},
			wantHeldActivity: nil,
			activities: []api.ActivityHistory{
				{InstanceID: "act-inc", Activity: "Control", Mode: new("Control"), Period: t0.Add(-2 * time.Minute)},
			},
		},
		{
			name: "stale session near cutoff refreshes lastSeenTimestamp while holding cursor",
			setupSession: func(store *memorySessionStore, aggSvc *fakeSessionAggregateService) {
				prevSeen := t0.Add(-118 * time.Minute)
				store.docs["ses-near-cutoff"] = &api.Session{
					ID:                 "ses-near-cutoff",
					UserID:             "user-1",
					CharacterID:        "char-1",
					StartedAt:          t0.Add(-150 * time.Minute),
					LastSeenActivityID: new("act-prev"),
					LastSeenTimestamp:  &prevSeen,
					AggregateIDs:       []string{"agg-prev"},
					Status:             new(api.SessionPending),
				}
				store.updatedAt["ses-near-cutoff"] = prevSeen
				aggSvc.seed(api.Aggregate{
					ID:         "agg-prev",
					ActivityID: "act-prev",
					CreatedAt:  prevSeen,
					Performance: map[string]api.InstancePerformance{
						"char-1": {
							PlayerStats: api.PlayerStats{Kills: statVal(10), Deaths: statVal(5)},
							Extra:       &map[string]api.UniqueStatValue{},
						},
					},
				})
			},
			wantHeldActivity: new("act-prev"),
			activities: []api.ActivityHistory{
				{InstanceID: "act-inc", Activity: "Control", Mode: new("Control"), Period: t0.Add(-2 * time.Minute)},
				{InstanceID: "act-prev", Activity: "Control", Mode: new("Control"), Period: t0.Add(-120 * time.Minute)},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := t0
			store := newMemorySessionStore()
			aggSvc := newFakeSessionAggregateService()
			aggSvc.nowFn = func() time.Time { return now }
			tc.setupSession(store, aggSvc)

			uSvc := &fakeSessionUserService{
				u: &user.User{
					ID:                  "user-1",
					PrimaryMembershipID: "12345",
					Memberships:         []user.Membership{{ID: "12345", Type: 3}},
				},
			}
			d2Svc := &fakeSessionD2Service{
				activitiesByChr: map[string][]api.ActivityHistory{
					"char-1": tc.activities,
				},
				perfsByAct: map[string]map[string]api.InstancePerformance{
					"act-inc": {
						"char-1": {PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(12), Deaths: statVal(4)}},
					},
				},
			}
			snapSvc := &fakeSessionSnapshotService{
				findBestFitFn: func(_, characterID string, _ time.Time, _ map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error) {
					return nil, &api.SnapshotLink{
						CharacterID:      characterID,
						ConfidenceLevel:  api.NoMatchConfidenceLevel,
						ConfidenceSource: api.SystemConfidenceSource,
						CreatedAt:        now,
					}, nil
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

			// Tick 1 (t=0, 118m since start/lastSeen): partial PGCR saves aggregate and bumps LastSeenTimestamp, holding LastSeenActivityID.
			resp1, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
			if err != nil {
				t.Fatalf("Tick 1 failed: %v", err)
			}
			if resp1.SessionsProcessed != 1 || resp1.AggregatesAddedTotal != 1 {
				t.Fatalf("Tick 1 unexpected response: %+v", resp1)
			}
			ses := store.docs["ses-near-cutoff"]
			if ses.LastSeenTimestamp == nil || !ses.LastSeenTimestamp.Equal(t0) {
				t.Fatalf("Tick 1 expected LastSeenTimestamp=%v, got %v", t0, ses.LastSeenTimestamp)
			}
			if (tc.wantHeldActivity == nil) != (ses.LastSeenActivityID == nil) ||
				(tc.wantHeldActivity != nil && *ses.LastSeenActivityID != *tc.wantHeldActivity) {
				t.Fatalf("Tick 1 expected LastSeenActivityID=%v, got %v", tc.wantHeldActivity, ses.LastSeenActivityID)
			}

			// Tick 2 (t=5m, 123m since original start/lastSeen > 2h CutOffDuration): session must stay active rather than expiring.
			now = t0.Add(5 * time.Minute)
			resp2, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
			if err != nil {
				t.Fatalf("Tick 2 failed: %v", err)
			}
			if resp2.SessionsStaleEnded != 0 || resp2.SessionsInactiveDeleted != 0 || resp2.SessionsStaleDeleted != 0 {
				t.Fatalf("Tick 2 prematurely expired active session: %+v", resp2)
			}
			if ses.Status == nil || *ses.Status != api.SessionPending {
				t.Fatalf("Tick 2 expected session to remain pending, got %v", ses.Status)
			}
		})
	}
}

func TestRunTick_IncompletePGCRStopsRetryingAfterWindow(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 6, 0, 0, 0, time.UTC)
	now := t0

	store := newMemorySessionStore()
	store.docs["ses-expired-retry"] = &api.Session{
		ID:           "ses-expired-retry",
		UserID:       "user-1",
		CharacterID:  "char-1",
		StartedAt:    t0.Add(-40 * time.Minute),
		AggregateIDs: []string{"agg-old-inc"},
		Status:       new(api.SessionPending),
	}

	aggSvc := newFakeSessionAggregateService()
	aggSvc.seed(api.Aggregate{
		ID:         "agg-old-inc",
		ActivityID: "act-old-inc",
		CreatedAt:  t0.Add(-35 * time.Minute),
		SnapshotLinks: map[string]api.SnapshotLink{
			"char-1": {
				CharacterID:      "char-1",
				SessionID:        new("ses-expired-retry"),
				ConfidenceLevel:  api.NoMatchConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        t0.Add(-35 * time.Minute),
			},
		},
		Performance: map[string]api.InstancePerformance{
			"char-1": {PlayerStats: api.PlayerStats{Kills: statVal(10)}},
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
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-1": {
				{InstanceID: "act-old-inc", Activity: "Control", Period: t0.Add(-36 * time.Minute)},
			},
		},
	}

	svc := &service{
		store:            store,
		aggregateService: aggSvc,
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  &fakeSessionSnapshotService{},
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	resp, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true})
	if err != nil {
		t.Fatalf("RunTick failed: %v", err)
	}
	if resp.SessionsSkipped != 1 || d2Svc.perfCalls.Load() != 0 {
		t.Fatalf("expected expired retry window to skip without GetPerformances, got resp=%+v perfCalls=%d", resp, d2Svc.perfCalls.Load())
	}
	if got := store.docs["ses-expired-retry"].LastSeenActivityID; got == nil || *got != "act-old-inc" {
		t.Fatalf("expected LastSeenActivityID to advance once retry window expired, got %v", got)
	}

	// Verify guardrails: manual links and complete 0-kill matches (Extra != nil) do not trigger refresh.
	extra := map[string]api.UniqueStatValue{}
	manualAgg := &api.Aggregate{
		CreatedAt: now.Add(-5 * time.Minute),
		SnapshotLinks: map[string]api.SnapshotLink{
			"char-1": {
				SessionID:        new("ses-1"),
				ConfidenceSource: api.UserConfidenceSource,
				CreatedAt:        now.Add(-5 * time.Minute),
			},
		},
		Performance: map[string]api.InstancePerformance{
			"char-1": {PlayerStats: api.PlayerStats{Kills: statVal(10)}},
		},
	}
	if needsPerformanceRefresh(manualAgg, "char-1", now) {
		t.Fatal("expected manual confidence link not to trigger refresh")
	}

	zeroKillCompleteAgg := &api.Aggregate{
		CreatedAt: now.Add(-5 * time.Minute),
		SnapshotLinks: map[string]api.SnapshotLink{
			"char-1": {
				SessionID:        new("ses-1"),
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now.Add(-5 * time.Minute),
			},
		},
		Performance: map[string]api.InstancePerformance{
			"char-1": {
				PlayerStats: api.PlayerStats{Kills: statVal(0)},
				Extra:       &extra,
			},
		},
	}
	if needsPerformanceRefresh(zeroKillCompleteAgg, "char-1", now) {
		t.Fatal("expected complete 0-kill match with Extra != nil not to trigger refresh")
	}
}

func TestRunTick_DryRunIncompletePGCRParity(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 6, 30, 0, 0, time.UTC)

	store := newMemorySessionStore()
	store.docs["ses-dry"] = &api.Session{
		ID:           "ses-dry",
		UserID:       "user-1",
		CharacterID:  "char-1",
		StartedAt:    t0.Add(-20 * time.Minute),
		AggregateIDs: []string{"agg-inc"},
		Status:       new(api.SessionPending),
	}

	aggSvc := newFakeSessionAggregateService()
	aggSvc.seed(api.Aggregate{
		ID:         "agg-inc",
		ActivityID: "act-inc",
		CreatedAt:  t0.Add(-5 * time.Minute),
		SnapshotLinks: map[string]api.SnapshotLink{
			"char-1": {
				CharacterID:      "char-1",
				SessionID:        new("ses-dry"),
				ConfidenceLevel:  api.NoMatchConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        t0.Add(-5 * time.Minute),
			},
		},
		Performance: map[string]api.InstancePerformance{
			"char-1": {PlayerStats: api.PlayerStats{Kills: statVal(12), Deaths: statVal(4)}},
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
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-1": {
				{InstanceID: "act-inc", Activity: "Control", Period: t0.Add(-6 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-inc": {
				"char-1": {PlayerStats: api.PlayerStats{Kills: statVal(12), Deaths: statVal(4)}},
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
		nowFn:            func() time.Time { return t0 },
	}

	// Dry-run while PGCR is still incomplete: matches live tick (skipped, 0 refreshed, warning emitted, 0 DB writes).
	ctx1 := clog.NewContext(context.Background())
	resp1, err := svc.RunTick(ctx1, TickOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run Tick 1 failed: %v", err)
	}
	if resp1.SessionsSkipped != 1 || resp1.SessionsProcessed != 0 || resp1.AggregatesAddedTotal != 0 {
		t.Fatalf("dry-run Tick 1 expected skip on still-incomplete PGCR, got %+v", resp1)
	}
	if got, ok := findIntAttr(clog.BagFromContext(ctx1).Attrs(), "aggregatesRefreshed"); !ok || got != 0 {
		t.Fatalf("dry-run Tick 1 expected aggregatesRefreshed=0, got %d (found=%v)", got, ok)
	}
	if !clog.BagFromContext(ctx1).HasWarning() {
		t.Fatal("dry-run Tick 1 expected incomplete_pgcr_extended warning")
	}
	if store.writes.Load() != 0 || aggSvc.writes.Load() != 0 || snapSvc.writes.Load() != 0 {
		t.Fatalf("dry-run Tick 1 must perform zero writes, got store=%d agg=%d snap=%d", store.writes.Load(), aggSvc.writes.Load(), snapSvc.writes.Load())
	}

	// Dry-run once PGCR is complete: reports processed + refreshed with still zero DB writes.
	extra := map[string]api.UniqueStatValue{
		"precisionKills": {Basic: api.StatsValuePair{Value: new(float64(5))}},
	}
	d2Svc.perfsByAct["act-inc"] = map[string]api.InstancePerformance{
		"char-1": {
			PlayerStats: api.PlayerStats{Kills: statVal(12), Deaths: statVal(4)},
			Extra:       &extra,
			Weapons: map[string]api.WeaponInstanceMetrics{
				"1001": weaponMetrics(1001, 12, "Rose", "/img/rose.png"),
			},
		},
	}

	ctx2 := clog.NewContext(context.Background())
	resp2, err := svc.RunTick(ctx2, TickOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run Tick 2 failed: %v", err)
	}
	if resp2.SessionsProcessed != 1 || resp2.SessionsSkipped != 0 {
		t.Fatalf("dry-run Tick 2 expected processed on complete PGCR, got %+v", resp2)
	}
	if got, ok := findIntAttr(clog.BagFromContext(ctx2).Attrs(), "aggregatesRefreshed"); !ok || got != 1 {
		t.Fatalf("dry-run Tick 2 expected aggregatesRefreshed=1, got %d (found=%v)", got, ok)
	}
	if store.writes.Load() != 0 || aggSvc.writes.Load() != 0 || snapSvc.writes.Load() != 0 {
		t.Fatalf("dry-run Tick 2 must perform zero writes, got store=%d agg=%d snap=%d", store.writes.Load(), aggSvc.writes.Load(), snapSvc.writes.Load())
	}
}

func TestSessionService_CompleteFinalLoadoutCheckIn(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC)

	t.Run("records final check-in before completing and skips on idempotent second Complete", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-1"] = &api.Session{
			ID:          "ses-1",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
		}
		store.lastSnapshots["ses-1"] = lastSnapshotRecord{
			snapshotID: "snap-old",
			hash:       "hash-old",
			recordedAt: t0.Add(-6 * time.Minute),
		}

		var gotOpts snapshot.SessionCheckInOptions
		var sawCompletedDuringCheckIn bool
		snapSvc := &fakeSessionSnapshotService{
			checkInFn: func(_, _, _ string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				gotOpts = opts
				sawCompletedDuringCheckIn = store.docs["ses-1"].CompletedAt != nil
				return snapshot.SessionCheckInResult{
					SnapshotID:      "snap-final",
					SnapshotHash:    "hash-final",
					HistoryRecorded: true,
					RecordedAt:      t0,
				}, nil
			},
		}
		uSvc := &fakeSessionUserService{
			u: &user.User{
				ID:                  "user-1",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		}

		svc := &service{
			store:           store,
			userService:     uSvc,
			snapshotService: snapSvc,
			nowFn:           func() time.Time { return t0 },
		}

		if err := svc.Complete(clog.NewContext(context.Background()), "ses-1"); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		if store.getDocCalls.Load() != 1 {
			t.Fatalf("expected 1 session doc read in Complete, got %d", store.getDocCalls.Load())
		}
		if snapSvc.saveCalls.Load() != 1 {
			t.Fatalf("expected 1 SaveSessionCheckIn call, got %d", snapSvc.saveCalls.Load())
		}
		if sawCompletedDuringCheckIn {
			t.Fatal("expected final loadout check-in to finish before session is marked completed")
		}
		if gotOpts.LastSnapshotID != "snap-old" || gotOpts.LastSnapshotHash != "hash-old" || gotOpts.HeartbeatInterval != snapshotHeartbeatPeriod {
			t.Fatalf("unexpected check-in opts passed from Complete: %+v", gotOpts)
		}
		if rec := store.lastSnapshots["ses-1"]; rec.snapshotID != "snap-final" || rec.hash != "hash-final" {
			t.Fatalf("expected lastSnapshot metadata updated on session, got %+v", rec)
		}
		if store.docs["ses-1"].CompletedAt == nil || *store.docs["ses-1"].Status != api.SessionComplete {
			t.Fatalf("expected session marked completed, got %+v", store.docs["ses-1"])
		}

		// Idempotent second call must not re-run SaveSessionCheckIn.
		if err := svc.Complete(clog.NewContext(context.Background()), "ses-1"); err != nil {
			t.Fatalf("second Complete failed: %v", err)
		}
		if snapSvc.saveCalls.Load() != 1 {
			t.Fatalf("expected no additional check-in on already-completed session, got %d", snapSvc.saveCalls.Load())
		}
	})

	t.Run("throttled final check-in avoids unnecessary metadata write", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-2"] = &api.Session{
			ID:          "ses-2",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
		}
		snapSvc := &fakeSessionSnapshotService{
			checkInFn: func(_, _, _ string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				return snapshot.SessionCheckInResult{
					SnapshotID:      "snap-same",
					SnapshotHash:    "hash-same",
					HistoryRecorded: false,
				}, nil
			},
		}
		uSvc := &fakeSessionUserService{
			u: &user.User{
				ID:                  "user-1",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		}
		svc := &service{
			store:           store,
			userService:     uSvc,
			snapshotService: snapSvc,
			nowFn:           func() time.Time { return t0 },
		}

		if err := svc.Complete(clog.NewContext(context.Background()), "ses-2"); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		if store.writes.Load() != 1 {
			t.Fatalf("expected only completeDoc write when check-in is throttled, got %d writes", store.writes.Load())
		}
	})

	t.Run("best-effort resilience when check-in errors", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-3"] = &api.Session{
			ID:          "ses-3",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
		}
		snapSvc := &fakeSessionSnapshotService{
			saveErrByChar: map[string]error{"char-1": errors.New("bungie 503")},
		}
		uSvc := &fakeSessionUserService{
			u: &user.User{
				ID:                  "user-1",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		}
		svc := &service{
			store:           store,
			userService:     uSvc,
			snapshotService: snapSvc,
			nowFn:           func() time.Time { return t0 },
		}

		ctx := clog.NewContext(context.Background())
		if err := svc.Complete(ctx, "ses-3"); err != nil {
			t.Fatalf("Complete should still succeed on check-in failure, got: %v", err)
		}
		if store.docs["ses-3"].CompletedAt == nil || *store.docs["ses-3"].Status != api.SessionComplete {
			t.Fatalf("expected session still marked complete, got %+v", store.docs["ses-3"])
		}
		if !clog.BagFromContext(ctx).HasWarning() {
			t.Fatal("expected warning recorded when final check-in fails")
		}
		assertAllWarningsAreStrings(t, clog.BagFromContext(ctx).Attrs())
	})

	t.Run("bounded check-in timeout preserves parent context for completeDoc", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-4"] = &api.Session{
			ID:          "ses-4",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
		}

		var hasDeadline bool
		var checkInErr error
		snapSvc := &fakeSessionSnapshotService{
			checkInCtxFn: func(ctx context.Context, _, _, _ string, _ snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				_, hasDeadline = ctx.Deadline()
				<-ctx.Done()
				checkInErr = ctx.Err()
				return snapshot.SessionCheckInResult{}, checkInErr
			},
		}
		uSvc := &fakeSessionUserService{
			u: &user.User{
				ID:                  "user-1",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		}
		svc := &service{
			store:               store,
			userService:         uSvc,
			snapshotService:     snapSvc,
			nowFn:               func() time.Time { return t0 },
			finalCheckInTimeout: 25 * time.Millisecond,
		}

		parentCtx := clog.NewContext(context.Background())
		if err := svc.Complete(parentCtx, "ses-4"); err != nil {
			t.Fatalf("Complete should still succeed when final check-in times out, got: %v", err)
		}
		if !hasDeadline {
			t.Fatal("expected final check-in to receive a bounded context deadline")
		}
		if !errors.Is(checkInErr, context.DeadlineExceeded) {
			t.Fatalf("expected check-in to terminate with DeadlineExceeded, got %v", checkInErr)
		}
		if parentCtx.Err() != nil {
			t.Fatalf("expected parent context to remain uncanceled, got %v", parentCtx.Err())
		}
		if store.docs["ses-4"].CompletedAt == nil || *store.docs["ses-4"].Status != api.SessionComplete {
			t.Fatalf("expected session marked complete after check-in timeout, got %+v", store.docs["ses-4"])
		}
		if !clog.BagFromContext(parentCtx).HasWarning() {
			t.Fatal("expected warning recorded when final check-in times out")
		}
	})
}


