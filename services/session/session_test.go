package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"os"
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
	mu                           sync.Mutex
	docs                         map[string]*api.Session
	updatedAt                    map[string]time.Time
	lastSnapshots                map[string]lastSnapshotRecord
	candidateOrder               []string
	seq                          int
	getDocCalls                  atomic.Int32
	getActiveByUserIDsCalls      atomic.Int32
	getActiveByUserIDsErr        error
	writes                       atomic.Int32
	lastCompletedWindowCount     atomic.Int32
	lastSnapshotWriteHasDeadline atomic.Bool
	compEndWriteHasDeadline      atomic.Bool
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

func (m *memorySessionStore) HasActive(_ context.Context, userID, characterID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.docs {
		if s.UserID == userID && s.CharacterID == characterID && s.Status != nil && *s.Status == api.SessionPending {
			return true, nil
		}
	}
	return false, nil
}

func (m *memorySessionStore) CreateDoc(_ context.Context, ses *api.Session) (*api.Session, error) {
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

func (m *memorySessionStore) GetActiveDoc(_ context.Context, userID, characterID string) (*api.Session, error) {
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

func (m *memorySessionStore) GetActiveByUserIDs(_ context.Context, userIDs []string) ([]ActiveSession, error) {
	m.getActiveByUserIDsCalls.Add(1)
	if m.getActiveByUserIDsErr != nil {
		return nil, m.getActiveByUserIDsErr
	}
	wanted := make(map[string]struct{}, len(userIDs))
	for _, id := range userIDs {
		wanted[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ActiveSession, 0)
	for _, s := range m.docs {
		if _, ok := wanted[s.UserID]; !ok {
			continue
		}
		if s.Status == nil || *s.Status != api.SessionPending {
			continue
		}
		out = append(out, ActiveSession{
			SessionID:   s.ID,
			UserID:      s.UserID,
			CharacterID: s.CharacterID,
			StartedAt:   s.StartedAt,
			TrackComp:   s.CompTracking != nil && s.CompTracking.Enabled,
		})
	}
	return out, nil
}

func (m *memorySessionStore) GetDoc(_ context.Context, sessionID string) (*api.Session, error) {
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

func (m *memorySessionStore) GetDocWithMeta(_ context.Context, sessionID string) (*TickCandidate, error) {
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
	return &TickCandidate{
		Session:                cloneSession(s),
		UpdatedAt:              upd,
		LastSnapshotID:         rec.snapshotID,
		LastSnapshotHash:       rec.hash,
		LastSnapshotRecordedAt: rec.recordedAt,
	}, nil
}

func (m *memorySessionStore) ListDocs(_ context.Context, userID, characterID *string, st *api.SessionStatus) ([]api.Session, error) {
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

func (m *memorySessionStore) ListCompletedInWindow(_ context.Context, since, until time.Time) ([]api.Session, error) {
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

func (m *memorySessionStore) GetTickCandidates(_ context.Context, completedSince time.Time) ([]TickCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	if len(m.candidateOrder) > 0 {
		ids = make([]string, 0, len(m.candidateOrder))
		for _, id := range m.candidateOrder {
			if _, ok := m.docs[id]; ok {
				ids = append(ids, id)
			}
		}
	} else {
		ids = make([]string, 0, len(m.docs))
		for id := range m.docs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
	}

	out := make([]TickCandidate, 0, len(ids))
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
		out = append(out, TickCandidate{
			Session:                cloneSession(s),
			UpdatedAt:              upd,
			LastSnapshotID:         rec.snapshotID,
			LastSnapshotHash:       rec.hash,
			LastSnapshotRecordedAt: rec.recordedAt,
		})
	}
	return out, nil
}

func (m *memorySessionStore) UpdateFields(_ context.Context, _ string, _ []firestore.Update) error {
	m.writes.Add(1)
	return nil
}

func (m *memorySessionStore) CompleteDoc(_ context.Context, sessionID string, completedAt time.Time) error {
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

func (m *memorySessionStore) EndSessionDoc(_ context.Context, sessionID string, completedBy api.AuditField, completedAt time.Time, summary *api.SessionSummary) error {
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

func (m *memorySessionStore) UpdateSummary(_ context.Context, sessionID string, summary *api.SessionSummary, updatedAt time.Time) error {
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

func (m *memorySessionStore) UpdateCompTrackingEnd(ctx context.Context, sessionID string, end *api.CompRankSnapshot, pointDelta *int) error {
	_, hasDeadline := ctx.Deadline()
	m.compEndWriteHasDeadline.Store(hasDeadline)
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

func (m *memorySessionStore) SetLastActivity(_ context.Context, sessionID, activityID string, seenAt time.Time) error {
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

func (m *memorySessionStore) TouchLastSeen(_ context.Context, sessionID string, seenAt time.Time) error {
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

func (m *memorySessionStore) SetLastSnapshot(ctx context.Context, sessionID, snapshotID, snapshotHash string, recordedAt time.Time) error {
	_, hasDeadline := ctx.Deadline()
	m.lastSnapshotWriteHasDeadline.Store(hasDeadline)
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

func (m *memorySessionStore) DeleteDoc(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes.Add(1)
	delete(m.docs, sessionID)
	delete(m.updatedAt, sessionID)
	delete(m.lastSnapshots, sessionID)
	return nil
}

func (m *memorySessionStore) AddAggregateIDs(_ context.Context, sessionID string, aggregateIDs []string) error {
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
	u                       *user.User
	users                   map[string]*user.User
	getUserErr              error
	findByCharErr           error
	getPrimaryMembershipErr error
	getPrimaryMembershipFn  func(ctx context.Context, userID string) (int64, int64, error)
	membershipCalls         atomic.Int32
	userCalls               atomic.Int32
	findByCharCalls         atomic.Int32
}

func (f *fakeSessionUserService) FindByCharacterIDs(_ context.Context, characterIDs []string) ([]user.User, error) {
	f.findByCharCalls.Add(1)
	if f.findByCharErr != nil {
		return nil, f.findByCharErr
	}
	wanted := make(map[string]struct{}, len(characterIDs))
	for _, cid := range characterIDs {
		if cid != "" {
			wanted[cid] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return []user.User{}, nil
	}

	seen := make(map[string]struct{})
	out := make([]user.User, 0)
	checkUser := func(u *user.User) {
		if u == nil {
			return
		}
		if _, dup := seen[u.ID]; dup && u.ID != "" {
			return
		}
		matched := false
		for _, cid := range u.CharacterIDs {
			if _, ok := wanted[cid]; ok {
				matched = true
				break
			}
		}
		if !matched {
			for _, ch := range u.Characters {
				if _, ok := wanted[ch.Id]; ok {
					matched = true
					break
				}
			}
		}
		if matched {
			if u.ID != "" {
				seen[u.ID] = struct{}{}
			}
			out = append(out, *u)
		}
	}
	if f.users != nil {
		for _, u := range f.users {
			checkUser(u)
		}
	} else {
		checkUser(f.u)
	}
	return out, nil
}

func (f *fakeSessionUserService) GetUser(_ context.Context, userID string) (*user.User, error) {
	f.userCalls.Add(1)
	if f.getUserErr != nil {
		return nil, f.getUserErr
	}
	if f.getPrimaryMembershipErr != nil {
		return nil, f.getPrimaryMembershipErr
	}
	if f.users != nil {
		if u, ok := f.users[userID]; ok && u != nil {
			return u, nil
		}
		return nil, user.NotFound
	}
	if f.u == nil {
		return nil, user.NotFound
	}
	return f.u, nil
}

func (f *fakeSessionUserService) GetPrimaryMembership(ctx context.Context, userID string) (int64, int64, error) {
	f.membershipCalls.Add(1)
	if f.getPrimaryMembershipFn != nil {
		return f.getPrimaryMembershipFn(ctx, userID)
	}
	if f.getPrimaryMembershipErr != nil {
		return 0, 0, f.getPrimaryMembershipErr
	}
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
	mu                sync.Mutex
	snapshots         []*api.CompRankSnapshot
	calls             atomic.Int32
	perfCalls         atomic.Int32
	perfCallsByAct    map[string]int
	perfCharIDsByAct  map[string][]string
	getCompRankFn     func(ctx context.Context, pmID, membershipType int64, characterID string) (*api.CompRankSnapshot, error)
	getActivityFn     func(ctx context.Context, membershipID string, membershipType int64, characterID string) ([]api.ActivityHistory, error)
	getPerformancesFn func(ctx context.Context, activityID string, characterIDs []string) (map[string]api.InstancePerformance, error)
	activitiesByChr   map[string][]api.ActivityHistory
	activityErrByCh   map[string]error
	perfsByAct        map[string]map[string]api.InstancePerformance
	perfErrByAct      map[string]error
}

func (f *fakeSessionD2Service) GetCompetitiveRank(ctx context.Context, pmID int64, membershipType int64, characterID string) (*api.CompRankSnapshot, error) {
	idx := int(f.calls.Add(1)) - 1
	if f.getCompRankFn != nil {
		return f.getCompRankFn(ctx, pmID, membershipType, characterID)
	}
	if idx < len(f.snapshots) {
		return f.snapshots[idx], nil
	}
	if len(f.snapshots) > 0 {
		return f.snapshots[len(f.snapshots)-1], nil
	}
	return nil, fmt.Errorf("no snapshot configured")
}

func (f *fakeSessionD2Service) GetAllPVPActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, _ int64, _ int64) ([]api.ActivityHistory, error) {
	if f.getActivityFn != nil {
		return f.getActivityFn(ctx, membershipID, membershipType, characterID)
	}
	if err, ok := f.activityErrByCh[characterID]; ok && err != nil {
		return nil, err
	}
	return f.activitiesByChr[characterID], nil
}

func (f *fakeSessionD2Service) GetPerformances(ctx context.Context, activityID string, characterIDs []string) (map[string]api.InstancePerformance, error) {
	f.perfCalls.Add(1)
	f.mu.Lock()
	if f.perfCallsByAct == nil {
		f.perfCallsByAct = make(map[string]int)
	}
	f.perfCallsByAct[activityID]++
	if f.perfCharIDsByAct == nil {
		f.perfCharIDsByAct = make(map[string][]string)
	}
	f.perfCharIDsByAct[activityID] = append([]string(nil), characterIDs...)
	f.mu.Unlock()

	if f.getPerformancesFn != nil {
		return f.getPerformancesFn(ctx, activityID, characterIDs)
	}
	if err, ok := f.perfErrByAct[activityID]; ok && err != nil {
		return nil, err
	}
	all := f.perfsByAct[activityID]
	if len(characterIDs) == 0 {
		return maps.Clone(all), nil
	}
	filtered := make(map[string]api.InstancePerformance, len(characterIDs))
	for _, cid := range characterIDs {
		if perf, ok := all[cid]; ok {
			filtered[cid] = perf
		}
	}
	return filtered, nil
}

type fakeSessionSnapshotService struct {
	snapshot.Service
	mu            sync.Mutex
	writes        atomic.Int32
	saveCalls     atomic.Int32
	getByIDsCalls atomic.Int32
	capturedIDs   []string
	byID          map[string]api.CharacterSnapshot
	getByIDsErr   error
	saveErrByChar map[string]error
	checkInFn     func(userID, membershipID, characterID string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error)
	checkInCtxFn  func(ctx context.Context, userID, membershipID, characterID string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error)
	findBestFitFn func(userID, characterID string, period time.Time, weapons map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error)
	enrichFn      func(snap *api.CharacterSnapshot, perf api.InstancePerformance) (*api.InstancePerformance, error)
}

func (f *fakeSessionSnapshotService) GetByIDs(_ context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error) {
	f.getByIDsCalls.Add(1)
	f.mu.Lock()
	f.capturedIDs = append([]string(nil), snapshotIDs...)
	f.mu.Unlock()
	if f.getByIDsErr != nil {
		return nil, f.getByIDsErr
	}
	out := make([]api.CharacterSnapshot, 0, len(snapshotIDs))
	for _, id := range snapshotIDs {
		if f.byID != nil {
			if snap, ok := f.byID[id]; ok {
				out = append(out, snap)
				continue
			}
		}
		out = append(out, api.CharacterSnapshot{ID: id})
	}
	return out, nil
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
	mu            sync.Mutex
	writes        atomic.Int32
	getCalls      atomic.Int32
	stampCalls    atomic.Int32
	stampErrByAgg map[string]error
	byID          map[string]*api.Aggregate
	byActivity    map[string]*api.Aggregate
	seq           int
	nowFn         func() time.Time
}

func newFakeSessionAggregateService() *fakeSessionAggregateService {
	return &fakeSessionAggregateService{
		byID:       make(map[string]*api.Aggregate),
		byActivity: make(map[string]*api.Aggregate),
	}
}

func cloneAggregate(agg *api.Aggregate) api.Aggregate {
	cp := *agg
	cp.CharacterIds = slices.Clone(agg.CharacterIds)
	cp.SessionIds = slices.Clone(agg.SessionIds)
	cp.SnapshotIds = slices.Clone(agg.SnapshotIds)
	cp.SnapshotLinks = maps.Clone(agg.SnapshotLinks)
	cp.Performance = maps.Clone(agg.Performance)
	return cp
}

func (f *fakeSessionAggregateService) seed(agg api.Aggregate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := cloneAggregate(&agg)
	f.byID[agg.ID] = &cp
	f.byActivity[agg.ActivityID] = &cp
}

func (f *fakeSessionAggregateService) GetAggregates(_ context.Context, IDs []string) ([]api.Aggregate, error) {
	f.getCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]api.Aggregate, 0, len(IDs))
	for _, id := range IDs {
		if agg, ok := f.byID[id]; ok {
			out = append(out, cloneAggregate(agg))
		}
	}
	return out, nil
}

func (f *fakeSessionAggregateService) GetAllAggregates(_ context.Context) ([]api.Aggregate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]api.Aggregate, 0, len(f.byID))
	for _, agg := range f.byID {
		out = append(out, cloneAggregate(agg))
	}
	return out, nil
}

func (f *fakeSessionAggregateService) StampSnapshotLinkParticipants(_ context.Context, aggregateID string, participants map[string]aggregate.SnapshotLinkParticipant) error {
	f.stampCalls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.stampErrByAgg[aggregateID]; ok && err != nil {
		return err
	}
	agg, ok := f.byID[aggregateID]
	if !ok {
		return aggregate.NotFound
	}
	f.writes.Add(1)
	updatedLinks := make(map[string]api.SnapshotLink, len(agg.SnapshotLinks))
	for k, v := range agg.SnapshotLinks {
		updatedLinks[k] = v
	}
	for charID, patch := range participants {
		link, exists := updatedLinks[charID]
		if !exists {
			continue
		}
		if patch.UserID != "" {
			link.UserID = new(patch.UserID)
		}
		if patch.DisplayName != "" {
			link.DisplayName = new(patch.DisplayName)
		}
		if patch.CharacterClass != "" {
			link.CharacterClass = new(patch.CharacterClass)
		}
		if patch.EmblemURL != "" {
			link.EmblemURL = new(patch.EmblemURL)
		}
		updatedLinks[charID] = link
	}
	agg.SnapshotLinks = updatedLinks
	return nil
}

func (f *fakeSessionAggregateService) GetAggregatesByActivity(_ context.Context, IDs []string) ([]api.Aggregate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]api.Aggregate, 0, len(IDs))
	for _, id := range IDs {
		if agg, ok := f.byActivity[id]; ok {
			out = append(out, cloneAggregate(agg))
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
		if characterID != "" && !slices.Contains(existing.CharacterIds, characterID) {
			existing.CharacterIds = append(existing.CharacterIds, characterID)
		}
		cp := cloneAggregate(existing)
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
	cp := cloneAggregate(agg)
	return &cp, nil
}

func (f *fakeSessionAggregateService) RemoveSession(_ context.Context, sessionID string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	siblings := make([]string, 0)
	seen := make(map[string]struct{})
	for id, agg := range f.byID {
		if !slices.Contains(agg.SessionIds, sessionID) {
			continue
		}
		f.writes.Add(1)
		filteredSessions := make([]string, 0, len(agg.SessionIds))
		for _, sid := range agg.SessionIds {
			if sid != sessionID {
				filteredSessions = append(filteredSessions, sid)
			}
		}
		agg.SessionIds = filteredSessions

		for cid, link := range agg.SnapshotLinks {
			if link.SessionID != nil && *link.SessionID == sessionID {
				link.SessionID = nil
				agg.SnapshotLinks[cid] = link
			}
		}

		hasSnapshot := len(agg.SnapshotIds) > 0
		if !hasSnapshot {
			for _, link := range agg.SnapshotLinks {
				if link.SnapshotID != nil && *link.SnapshotID != "" {
					hasSnapshot = true
					break
				}
			}
		}
		if len(agg.SessionIds) == 0 && !hasSnapshot {
			delete(f.byID, id)
			delete(f.byActivity, agg.ActivityID)
			continue
		}
		for _, sid := range agg.SessionIds {
			if sid == "" || sid == sessionID {
				continue
			}
			if _, ok := seen[sid]; !ok {
				seen[sid] = struct{}{}
				siblings = append(siblings, sid)
			}
		}
	}
	slices.Sort(siblings)
	return siblings, nil
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

func findStringAttr(attrs []slog.Attr, key string) (string, bool) {
	var walk func([]slog.Attr) (string, bool)
	walk = func(list []slog.Attr) (string, bool) {
		for _, a := range list {
			if a.Key == key && a.Value.Kind() == slog.KindString {
				return a.Value.String(), true
			}
			if a.Value.Kind() == slog.KindGroup {
				if v, ok := walk(a.Value.Group()); ok {
					return v, true
				}
			}
		}
		return "", false
	}
	return walk(attrs)
}

func findBoolAttr(attrs []slog.Attr, key string) (bool, bool) {
	var walk func([]slog.Attr) (bool, bool)
	walk = func(list []slog.Attr) (bool, bool) {
		for _, a := range list {
			if a.Key == key && a.Value.Kind() == slog.KindBool {
				return a.Value.Bool(), true
			}
			if a.Value.Kind() == slog.KindGroup {
				if v, ok := walk(a.Value.Group()); ok {
					return v, true
				}
			}
		}
		return false, false
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

	finalizeDone := make(chan context.Context, 1)
	svc := &service{
		store:       store,
		userService: uSvc,
		d2Service:   d2Svc,
		onFinalizeDone: func(bgCtx context.Context, _ error) {
			finalizeDone <- bgCtx
		},
	}

	ctx := clog.NewContext(context.Background())
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
	returned, err := svc.Complete(ctx, started.ID)
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if returned == nil || returned.CompletedAt == nil || returned.Status == nil || *returned.Status != api.SessionComplete {
		t.Fatalf("expected Complete to return completed session immediately, got %+v", returned)
	}
	if store.getDocCalls.Load() != 1 {
		t.Fatalf("expected Complete to read session doc once, got %d getDoc calls", store.getDocCalls.Load())
	}

	select {
	case bgCtx := <-finalizeDone:
		if status, ok := findStringAttr(clog.BagFromContext(bgCtx).Attrs(), "compFinalizeStatus"); !ok || status != "succeeded" {
			t.Fatalf("expected compFinalizeStatus=succeeded on background log, got %q (found=%v)", status, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background comp finalization")
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

func (u *unfilteredWindowSessionStore) ListCompletedInWindow(ctx context.Context, _, _ time.Time) ([]api.Session, error) {
	completed := api.SessionComplete
	return u.memorySessionStore.ListDocs(ctx, nil, nil, &completed)
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

func TestGetActiveByUserIDs(t *testing.T) {
	t1 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)

	findIntAttr := func(attrs []slog.Attr, key string) (int64, bool) {
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

	t.Run("returns empty slice without querying store when input has no valid user IDs", func(t *testing.T) {
		store := newMemorySessionStore()
		svc := &service{store: store}

		ctx := clog.NewContext(context.Background())
		got, err := svc.GetActiveByUserIDs(ctx, []string{"", "   "})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("expected 0 sessions, got %d", len(got))
		}
		if store.getActiveByUserIDsCalls.Load() != 0 {
			t.Fatalf("expected 0 store calls for empty input, got %d", store.getActiveByUserIDsCalls.Load())
		}
		attrs := clog.BagFromContext(ctx).Attrs()
		if v, ok := findIntAttr(attrs, "requestedUserCount"); !ok || v != 0 {
			t.Fatalf("expected requestedUserCount=0 in clog, got ok=%v v=%d", ok, v)
		}
		if v, ok := findIntAttr(attrs, "returnedCount"); !ok || v != 0 {
			t.Fatalf("expected returnedCount=0 in clog, got ok=%v v=%d", ok, v)
		}
	})

	t.Run("returns empty slice safely when store is nil", func(t *testing.T) {
		svc := &service{}
		ctx := clog.NewContext(context.Background())
		got, err := svc.GetActiveByUserIDs(ctx, []string{"u-1"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("expected non-nil empty slice, got %#v", got)
		}
	})

	t.Run("batches pending sessions for requested users, maps trackComp, excludes completed and other users, and sorts newest first with SessionID tie-breaker", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-old"] = &api.Session{
			ID:           "ses-old",
			UserID:       "u-1",
			CharacterID:  "char-1a",
			StartedAt:    t1,
			Status:       new(api.SessionPending),
			CompTracking: &api.CompTracking{Enabled: false},
		}
		store.docs["ses-new"] = &api.Session{
			ID:           "ses-new",
			UserID:       "u-1",
			CharacterID:  "char-1b",
			StartedAt:    t3,
			Status:       new(api.SessionPending),
			CompTracking: &api.CompTracking{Enabled: true},
		}
		store.docs["ses-tie-b"] = &api.Session{
			ID:          "ses-tie-b",
			UserID:      "u-2",
			CharacterID: "char-2b-tie",
			StartedAt:   t2,
			Status:      new(api.SessionPending),
		}
		store.docs["ses-tie-a"] = &api.Session{
			ID:          "ses-tie-a",
			UserID:      "u-2",
			CharacterID: "char-2a-tie",
			StartedAt:   t2,
			Status:      new(api.SessionPending),
		}
		store.docs["ses-complete"] = &api.Session{
			ID:          "ses-complete",
			UserID:      "u-2",
			CharacterID: "char-2b",
			StartedAt:   t3,
			Status:      new(api.SessionComplete),
		}
		store.docs["ses-other"] = &api.Session{
			ID:          "ses-other",
			UserID:      "u-99",
			CharacterID: "char-99",
			StartedAt:   t3,
			Status:      new(api.SessionPending),
		}

		svc := &service{store: store}
		ctx := clog.NewContext(context.Background())
		got, err := svc.GetActiveByUserIDs(ctx, []string{" u-1 ", "u-2", "u-1", "u-3", ""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.getActiveByUserIDsCalls.Load() != 1 {
			t.Fatalf("expected 1 batched store call, got %d", store.getActiveByUserIDsCalls.Load())
		}
		if len(got) != 4 {
			t.Fatalf("expected 4 pending sessions, got %d: %+v", len(got), got)
		}
		if got[0].SessionID != "ses-new" || !got[0].TrackComp || got[0].CharacterID != "char-1b" {
			t.Fatalf("unexpected first session: %+v", got[0])
		}
		if got[1].SessionID != "ses-tie-a" || got[1].TrackComp || got[1].UserID != "u-2" {
			t.Fatalf("expected ses-tie-a before ses-tie-b on identical StartedAt, got %+v", got[1])
		}
		if got[2].SessionID != "ses-tie-b" || got[2].TrackComp || got[2].UserID != "u-2" {
			t.Fatalf("unexpected third session: %+v", got[2])
		}
		if got[3].SessionID != "ses-old" || got[3].TrackComp || got[3].CharacterID != "char-1a" {
			t.Fatalf("unexpected fourth session: %+v", got[3])
		}
		attrs := clog.BagFromContext(ctx).Attrs()
		if v, ok := findIntAttr(attrs, "requestedUserCount"); !ok || v != 3 {
			t.Fatalf("expected requestedUserCount=3 in clog, got ok=%v v=%d", ok, v)
		}
		if v, ok := findIntAttr(attrs, "returnedCount"); !ok || v != 4 {
			t.Fatalf("expected returnedCount=4 in clog, got ok=%v v=%d", ok, v)
		}
	})

	t.Run("propagates store error", func(t *testing.T) {
		store := newMemorySessionStore()
		store.getActiveByUserIDsErr = errors.New("firestore unavailable")
		svc := &service{store: store}

		_, err := svc.GetActiveByUserIDs(clog.NewContext(context.Background()), []string{"u-1"})
		if err == nil {
			t.Fatal("expected error from store")
		}
	})
}

func TestFirestoreEmulatorGetActiveByUserIDs(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("skipping Firestore emulator integration test; set FIRESTORE_EMULATOR_HOST to run")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, "gruntt-destiny-test")
	if err != nil {
		t.Fatalf("failed to create Firestore emulator client: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewFirestoreStore(db)
	svc := &service{store: store}

	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	user1 := "emu-fireteam-u1-" + runID
	user2 := "emu-fireteam-u2-" + runID
	userChunk2 := "emu-fireteam-chunk2-" + runID
	userOther := "emu-fireteam-other-" + runID

	t1 := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 5, 1, 13, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
	t4 := time.Date(2026, 5, 1, 15, 0, 0, 0, time.UTC)

	seedDocs := []struct {
		id  string
		doc map[string]any
	}{
		{
			id: "emu-ses-comp-enabled-" + runID,
			doc: map[string]any{
				"id":          "emu-ses-comp-enabled-" + runID,
				"userId":      user1,
				"characterId": "char-1b",
				"startedAt":   t3,
				"status":      api.SessionPending,
				"compTracking": map[string]any{
					"enabled": true,
				},
			},
		},
		{
			id: "emu-ses-comp-disabled-" + runID,
			doc: map[string]any{
				"id":          "emu-ses-comp-disabled-" + runID,
				"userId":      user1,
				"characterId": "char-1a",
				"startedAt":   t1,
				"status":      api.SessionPending,
				"compTracking": map[string]any{
					"enabled": false,
				},
			},
		},
		{
			// Deliberately omit inner "id" and "compTracking" to verify doc.Ref.ID fallback under .Select(...).
			id: "emu-ses-no-id-no-comp-" + runID,
			doc: map[string]any{
				"userId":      user2,
				"characterId": "char-2a",
				"startedAt":   t2,
				"status":      api.SessionPending,
			},
		},
		{
			// Lives in the second >30 userId chunk and has the newest StartedAt to verify cross-chunk sorting.
			id: "emu-ses-chunk2-newest-" + runID,
			doc: map[string]any{
				"id":          "emu-ses-chunk2-newest-" + runID,
				"userId":      userChunk2,
				"characterId": "char-chunk2",
				"startedAt":   t4,
				"status":      api.SessionPending,
				"compTracking": map[string]any{
					"enabled": true,
				},
			},
		},
		{
			id: "emu-ses-complete-" + runID,
			doc: map[string]any{
				"id":          "emu-ses-complete-" + runID,
				"userId":      user2,
				"characterId": "char-2b",
				"startedAt":   t3,
				"status":      api.SessionComplete,
			},
		},
		{
			id: "emu-ses-other-" + runID,
			doc: map[string]any{
				"id":          "emu-ses-other-" + runID,
				"userId":      userOther,
				"characterId": "char-99",
				"startedAt":   t3,
				"status":      api.SessionPending,
			},
		},
	}

	for _, s := range seedDocs {
		if _, err := db.Collection(collection).Doc(s.id).Set(ctx, s.doc); err != nil {
			t.Fatalf("failed to seed session doc %s: %v", s.id, err)
		}
	}
	t.Cleanup(func() {
		for _, s := range seedDocs {
			_, _ = db.Collection(collection).Doc(s.id).Delete(ctx)
		}
	})

	queryUserIDs := make([]string, 0, 35)
	queryUserIDs = append(queryUserIDs, user1, user2, user1)
	for i := 0; i < 30; i++ {
		queryUserIDs = append(queryUserIDs, fmt.Sprintf("emu-fireteam-pad-%s-%d", runID, i))
	}
	queryUserIDs = append(queryUserIDs, userChunk2)

	got, err := svc.GetActiveByUserIDs(clog.NewContext(ctx), queryUserIDs)
	if err != nil {
		t.Fatalf("GetActiveByUserIDs failed: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 active sessions across chunks, got %d: %+v", len(got), got)
	}
	if got[0].SessionID != "emu-ses-chunk2-newest-"+runID || !got[0].TrackComp || got[0].UserID != userChunk2 || !got[0].StartedAt.Equal(t4) {
		t.Fatalf("expected chunk-2 session sorted newest first, got %+v", got[0])
	}
	if got[1].SessionID != "emu-ses-comp-enabled-"+runID || !got[1].TrackComp || got[1].CharacterID != "char-1b" || !got[1].StartedAt.Equal(t3) {
		t.Fatalf("unexpected second active session: %+v", got[1])
	}
	if got[2].SessionID != "emu-ses-no-id-no-comp-"+runID || got[2].TrackComp || got[2].UserID != user2 || !got[2].StartedAt.Equal(t2) {
		t.Fatalf("expected doc.Ref.ID fallback and omitted compTracking on third session, got %+v", got[2])
	}
	if got[3].SessionID != "emu-ses-comp-disabled-"+runID || got[3].TrackComp || got[3].CharacterID != "char-1a" || !got[3].StartedAt.Equal(t1) {
		t.Fatalf("unexpected fourth active session: %+v", got[3])
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
		name             string
		setupSession     func(store *memorySessionStore, aggSvc *fakeSessionAggregateService)
		wantHeldActivity *string
		activities       []api.ActivityHistory
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

func waitFinalizeDone(t *testing.T, ch <-chan context.Context) context.Context {
	t.Helper()
	select {
	case bgCtx := <-ch:
		return bgCtx
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for complete-session-finalize-bg")
		return nil
	}
}

func TestSessionService_CompleteFinalLoadoutCheckIn(t *testing.T) {
	t0 := time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC)

	t.Run("completes immediately, runs check-in and comp concurrently with one membership read, and skips check-in on idempotent retry", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-1"] = &api.Session{
			ID:          "ses-1",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 5000},
			},
		}
		store.lastSnapshots["ses-1"] = lastSnapshotRecord{
			snapshotID: "snap-old",
			hash:       "hash-old",
			recordedAt: t0.Add(-6 * time.Minute),
		}

		releaseBungie := make(chan struct{})
		checkInEntered := make(chan struct{}, 1)
		compEntered := make(chan struct{}, 1)
		finalizeDone := make(chan context.Context, 2)

		var (
			mu         sync.Mutex
			gotOpts    snapshot.SessionCheckInOptions
			sawCompSet bool
		)
		snapSvc := &fakeSessionSnapshotService{
			checkInCtxFn: func(ctx context.Context, _, _, _ string, opts snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				mu.Lock()
				gotOpts = opts
				mu.Unlock()
				store.mu.Lock()
				sawComp := store.docs["ses-1"].CompletedAt != nil
				store.mu.Unlock()
				mu.Lock()
				sawCompSet = sawComp
				mu.Unlock()

				checkInEntered <- struct{}{}
				select {
				case <-releaseBungie:
				case <-ctx.Done():
					return snapshot.SessionCheckInResult{}, ctx.Err()
				}
				return snapshot.SessionCheckInResult{
					SnapshotID:      "snap-final",
					SnapshotHash:    "hash-final",
					HistoryRecorded: true,
					RecordedAt:      t0,
				}, nil
			},
		}
		d2Svc := &fakeSessionD2Service{
			getCompRankFn: func(ctx context.Context, _, _ int64, _ string) (*api.CompRankSnapshot, error) {
				compEntered <- struct{}{}
				select {
				case <-releaseBungie:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return &api.CompRankSnapshot{Points: 5150}, nil
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
			d2Service:       d2Svc,
			nowFn:           func() time.Time { return t0 },
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		parentCtx, cancelParent := context.WithCancel(clog.NewContext(context.Background()))
		completed, err := svc.Complete(parentCtx, "ses-1")
		if err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		// Cancel parent HTTP request context immediately to verify detached clog.Go keeps running.
		cancelParent()

		if completed == nil || completed.CompletedAt == nil || !completed.CompletedAt.Equal(t0) {
			t.Fatalf("expected Complete to return completedAt=%v immediately, got %+v", t0, completed)
		}
		if completed.Status == nil || *completed.Status != api.SessionComplete {
			t.Fatalf("expected status=complete immediately, got %+v", completed.Status)
		}
		if completed.AggregateIDs == nil {
			t.Fatal("expected non-nil AggregateIDs slice on returned session")
		}
		if store.getDocCalls.Load() != 1 {
			t.Fatalf("expected 1 session doc read in Complete, got %d", store.getDocCalls.Load())
		}
		if async, ok := findBoolAttr(clog.BagFromContext(parentCtx).Attrs(), "finalizationAsync"); !ok || !async {
			t.Fatalf("expected finalizationAsync=true on request log, got %v (found=%v)", async, ok)
		}

		// Wait until BOTH Bungie calls are concurrently in flight before releasing.
		for _, ch := range []chan struct{}{checkInEntered, compEntered} {
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
				t.Fatal("expected check-in and comp finalization to execute concurrently")
			}
		}
		close(releaseBungie)

		bgCtx := waitFinalizeDone(t, finalizeDone)

		if uSvc.membershipCalls.Load() != 1 {
			t.Fatalf("expected 1 GetPrimaryMembership call shared across check-in and comp, got %d", uSvc.membershipCalls.Load())
		}
		if snapSvc.saveCalls.Load() != 1 {
			t.Fatalf("expected 1 SaveSessionCheckIn call, got %d", snapSvc.saveCalls.Load())
		}
		if d2Svc.calls.Load() != 1 {
			t.Fatalf("expected 1 GetCompetitiveRank call, got %d", d2Svc.calls.Load())
		}
		mu.Lock()
		optsCopy := gotOpts
		completedBeforeCheckIn := sawCompSet
		mu.Unlock()
		if !completedBeforeCheckIn {
			t.Fatal("expected session to be marked completed before detached check-in runs")
		}
		if optsCopy.LastSnapshotID != "snap-old" || optsCopy.LastSnapshotHash != "hash-old" || optsCopy.HeartbeatInterval != snapshotHeartbeatPeriod || optsCopy.MembershipType != 3 {
			t.Fatalf("unexpected check-in opts passed from Complete: %+v", optsCopy)
		}
		if !store.lastSnapshotWriteHasDeadline.Load() {
			t.Fatal("expected SetLastSnapshot write context to have a bounded deadline")
		}
		if !store.compEndWriteHasDeadline.Load() {
			t.Fatal("expected UpdateCompTrackingEnd write context to have a bounded deadline")
		}

		store.mu.Lock()
		rec := store.lastSnapshots["ses-1"]
		persisted := cloneSession(store.docs["ses-1"])
		store.mu.Unlock()
		if rec.snapshotID != "snap-final" || rec.hash != "hash-final" {
			t.Fatalf("expected lastSnapshot metadata updated on session, got %+v", rec)
		}
		if persisted.CompTracking == nil || persisted.CompTracking.End == nil || persisted.CompTracking.End.Points != 5150 {
			t.Fatalf("expected CompTracking.End=5150 persisted, got %+v", persisted.CompTracking)
		}
		if persisted.CompTracking.PointDelta == nil || *persisted.CompTracking.PointDelta != 150 {
			t.Fatalf("expected PointDelta=150 persisted, got %+v", persisted.CompTracking)
		}

		bgAttrs := clog.BagFromContext(bgCtx).Attrs()
		if status, ok := findStringAttr(bgAttrs, "finalLoadoutStatus"); !ok || status != "saved" {
			t.Fatalf("expected finalLoadoutStatus=saved, got %q (found=%v)", status, ok)
		}
		if status, ok := findStringAttr(bgAttrs, "compFinalizeStatus"); !ok || status != "succeeded" {
			t.Fatalf("expected compFinalizeStatus=succeeded, got %q (found=%v)", status, ok)
		}

		// Idempotent second call must not re-run SaveSessionCheckIn or GetCompetitiveRank.
		secondCtx := clog.NewContext(context.Background())
		secondSes, err := svc.Complete(secondCtx, "ses-1")
		if err != nil {
			t.Fatalf("second Complete failed: %v", err)
		}
		if secondSes.Status == nil || *secondSes.Status != api.SessionComplete {
			t.Fatalf("expected completed session on idempotent second Complete, got %+v", secondSes)
		}
		if async, ok := findBoolAttr(clog.BagFromContext(secondCtx).Attrs(), "finalizationAsync"); !ok || async {
			t.Fatalf("expected finalizationAsync=false on already-finalized session, got %v (found=%v)", async, ok)
		}
		if snapSvc.saveCalls.Load() != 1 || d2Svc.calls.Load() != 1 || uSvc.membershipCalls.Load() != 1 {
			t.Fatalf("expected no additional external calls on idempotent Complete, got save=%d comp=%d membership=%d",
				snapSvc.saveCalls.Load(), d2Svc.calls.Load(), uSvc.membershipCalls.Load())
		}
	})

	t.Run("throttled final check-in avoids unnecessary metadata write and logs throttled status", func(t *testing.T) {
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
		finalizeDone := make(chan context.Context, 1)
		svc := &service{
			store:           store,
			userService:     uSvc,
			snapshotService: snapSvc,
			nowFn:           func() time.Time { return t0 },
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		if _, err := svc.Complete(clog.NewContext(context.Background()), "ses-2"); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		bgCtx := waitFinalizeDone(t, finalizeDone)

		if store.writes.Load() != 1 {
			t.Fatalf("expected only completeDoc write when check-in is throttled, got %d writes", store.writes.Load())
		}
		bgAttrs := clog.BagFromContext(bgCtx).Attrs()
		if status, ok := findStringAttr(bgAttrs, "finalLoadoutStatus"); !ok || status != "throttled" {
			t.Fatalf("expected finalLoadoutStatus=throttled, got %q (found=%v)", status, ok)
		}
		if throttled, ok := findBoolAttr(bgAttrs, "finalLoadoutThrottled"); !ok || !throttled {
			t.Fatalf("expected finalLoadoutThrottled=true, got %v (found=%v)", throttled, ok)
		}
	})

	t.Run("best-effort resilience when check-in and comp rank error", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-3"] = &api.Session{
			ID:          "ses-3",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 4000},
			},
		}
		snapSvc := &fakeSessionSnapshotService{
			saveErrByChar: map[string]error{"char-1": errors.New("bungie 503")},
		}
		d2Svc := &fakeSessionD2Service{
			getCompRankFn: func(_ context.Context, _, _ int64, _ string) (*api.CompRankSnapshot, error) {
				return nil, errors.New("bungie comp 503")
			},
		}
		uSvc := &fakeSessionUserService{
			u: &user.User{
				ID:                  "user-1",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		}
		finalizeDone := make(chan context.Context, 1)
		svc := &service{
			store:           store,
			userService:     uSvc,
			snapshotService: snapSvc,
			d2Service:       d2Svc,
			nowFn:           func() time.Time { return t0 },
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		ctx := clog.NewContext(context.Background())
		ses, err := svc.Complete(ctx, "ses-3")
		if err != nil {
			t.Fatalf("Complete should still succeed on Bungie failures, got: %v", err)
		}
		if ses.CompletedAt == nil || *ses.Status != api.SessionComplete {
			t.Fatalf("expected session marked complete, got %+v", ses)
		}

		bgCtx := waitFinalizeDone(t, finalizeDone)
		bgBag := clog.BagFromContext(bgCtx)
		if !bgBag.HasWarning() {
			t.Fatal("expected warning recorded on background log when check-in and comp finalize fail")
		}
		assertAllWarningsAreStrings(t, bgBag.Attrs())
		if status, ok := findStringAttr(bgBag.Attrs(), "finalLoadoutStatus"); !ok || status != "failed" {
			t.Fatalf("expected finalLoadoutStatus=failed, got %q (found=%v)", status, ok)
		}
		if status, ok := findStringAttr(bgBag.Attrs(), "compFinalizeStatus"); !ok || status != "fallback_run_tick" {
			t.Fatalf("expected compFinalizeStatus=fallback_run_tick, got %q (found=%v)", status, ok)
		}
	})

	t.Run("bounded check-in timeout does not block Complete or comp finalization", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-4"] = &api.Session{
			ID:          "ses-4",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 4500},
			},
		}

		var (
			mu          sync.Mutex
			hasDeadline bool
			checkInErr  error
		)
		snapSvc := &fakeSessionSnapshotService{
			checkInCtxFn: func(ctx context.Context, _, _, _ string, _ snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				_, dl := ctx.Deadline()
				<-ctx.Done()
				mu.Lock()
				hasDeadline = dl
				checkInErr = ctx.Err()
				mu.Unlock()
				return snapshot.SessionCheckInResult{}, ctx.Err()
			},
		}
		d2Svc := &fakeSessionD2Service{
			snapshots: []*api.CompRankSnapshot{{Points: 4625}},
		}
		uSvc := &fakeSessionUserService{
			u: &user.User{
				ID:                  "user-1",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		}
		finalizeDone := make(chan context.Context, 1)
		svc := &service{
			store:               store,
			userService:         uSvc,
			snapshotService:     snapSvc,
			d2Service:           d2Svc,
			nowFn:               func() time.Time { return t0 },
			finalCheckInTimeout: 25 * time.Millisecond,
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		parentCtx := clog.NewContext(context.Background())
		if _, err := svc.Complete(parentCtx, "ses-4"); err != nil {
			t.Fatalf("Complete should still succeed when final check-in times out, got: %v", err)
		}
		bgCtx := waitFinalizeDone(t, finalizeDone)

		mu.Lock()
		gotDeadline := hasDeadline
		gotErr := checkInErr
		mu.Unlock()
		if !gotDeadline {
			t.Fatal("expected final check-in to receive a bounded context deadline")
		}
		if !errors.Is(gotErr, context.DeadlineExceeded) {
			t.Fatalf("expected check-in to terminate with DeadlineExceeded, got %v", gotErr)
		}
		if parentCtx.Err() != nil {
			t.Fatalf("expected parent context to remain uncanceled, got %v", parentCtx.Err())
		}

		store.mu.Lock()
		persisted := cloneSession(store.docs["ses-4"])
		store.mu.Unlock()
		if persisted.CompTracking == nil || persisted.CompTracking.End == nil || persisted.CompTracking.End.Points != 4625 {
			t.Fatalf("expected comp finalization to still persist End=4625 despite check-in timeout, got %+v", persisted.CompTracking)
		}
		bgBag := clog.BagFromContext(bgCtx)
		if !bgBag.HasWarning() {
			t.Fatal("expected warning recorded when final check-in times out")
		}
		assertAllWarningsAreStrings(t, bgBag.Attrs())
		if status, ok := findStringAttr(bgBag.Attrs(), "finalLoadoutStatus"); !ok || status != "failed" {
			t.Fatalf("expected finalLoadoutStatus=failed, got %q (found=%v)", status, ok)
		}
		if status, ok := findStringAttr(bgBag.Attrs(), "compFinalizeStatus"); !ok || status != "succeeded" {
			t.Fatalf("expected compFinalizeStatus=succeeded, got %q (found=%v)", status, ok)
		}
	})

	t.Run("bounded comp timeout falls back to RunTick without blocking loadout check-in", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-5"] = &api.Session{
			ID:           "ses-5",
			UserID:       "user-1",
			CharacterID:  "char-1",
			StartedAt:    t0.Add(-10 * time.Minute),
			AggregateIDs: []string{"agg-1"},
			Status:       new(api.SessionPending),
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 4500},
			},
		}

		var (
			mu          sync.Mutex
			hasDeadline bool
			compErr     error
		)
		snapSvc := &fakeSessionSnapshotService{
			checkInFn: func(_, _, _ string, _ snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				return snapshot.SessionCheckInResult{
					SnapshotID:      "snap-5",
					SnapshotHash:    "hash-5",
					HistoryRecorded: true,
					RecordedAt:      t0,
				}, nil
			},
		}
		d2Svc := &fakeSessionD2Service{
			getCompRankFn: func(ctx context.Context, _, _ int64, _ string) (*api.CompRankSnapshot, error) {
				_, dl := ctx.Deadline()
				<-ctx.Done()
				mu.Lock()
				hasDeadline = dl
				compErr = ctx.Err()
				mu.Unlock()
				return nil, ctx.Err()
			},
		}
		uSvc := &fakeSessionUserService{
			u: &user.User{
				ID:                  "user-1",
				PrimaryMembershipID: "12345",
				Memberships:         []user.Membership{{ID: "12345", Type: 3}},
			},
		}
		finalizeDone := make(chan context.Context, 1)
		svc := &service{
			store:            store,
			userService:      uSvc,
			snapshotService:  snapSvc,
			d2Service:        d2Svc,
			nowFn:            func() time.Time { return t0 },
			finalCompTimeout: 25 * time.Millisecond,
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		if _, err := svc.Complete(clog.NewContext(context.Background()), "ses-5"); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		bgCtx := waitFinalizeDone(t, finalizeDone)

		mu.Lock()
		gotDeadline := hasDeadline
		gotErr := compErr
		mu.Unlock()
		if !gotDeadline {
			t.Fatal("expected comp finalization to receive a bounded context deadline")
		}
		if !errors.Is(gotErr, context.DeadlineExceeded) {
			t.Fatalf("expected comp finalization to terminate with DeadlineExceeded, got %v", gotErr)
		}

		store.mu.Lock()
		rec := store.lastSnapshots["ses-5"]
		persisted := cloneSession(store.docs["ses-5"])
		store.mu.Unlock()
		if rec.snapshotID != "snap-5" {
			t.Fatalf("expected final loadout check-in to persist despite comp timeout, got %+v", rec)
		}
		if persisted.CompTracking.End != nil {
			t.Fatalf("expected CompTracking.End to remain nil for RunTick retry, got %+v", persisted.CompTracking.End)
		}
		if !ShouldFinalizeComp(persisted) {
			t.Fatal("expected timed-out session to remain eligible for RunTick comp finalization")
		}

		bgBag := clog.BagFromContext(bgCtx)
		if !bgBag.HasWarning() {
			t.Fatal("expected warning recorded when comp finalization times out")
		}
		assertAllWarningsAreStrings(t, bgBag.Attrs())
		if status, ok := findStringAttr(bgBag.Attrs(), "finalLoadoutStatus"); !ok || status != "saved" {
			t.Fatalf("expected finalLoadoutStatus=saved, got %q (found=%v)", status, ok)
		}
		if status, ok := findStringAttr(bgBag.Attrs(), "compFinalizeStatus"); !ok || status != "fallback_run_tick" {
			t.Fatalf("expected compFinalizeStatus=fallback_run_tick, got %q (found=%v)", status, ok)
		}
	})

	t.Run("membership resolution failure skips Bungie calls and records fallback statuses", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-6"] = &api.Session{
			ID:          "ses-6",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 4500},
			},
		}
		snapSvc := &fakeSessionSnapshotService{}
		d2Svc := &fakeSessionD2Service{}
		uSvc := &fakeSessionUserService{
			getPrimaryMembershipErr: errors.New("firestore users unavailable"),
		}
		finalizeDone := make(chan context.Context, 1)
		svc := &service{
			store:           store,
			userService:     uSvc,
			snapshotService: snapSvc,
			d2Service:       d2Svc,
			nowFn:           func() time.Time { return t0 },
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		if _, err := svc.Complete(clog.NewContext(context.Background()), "ses-6"); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		bgCtx := waitFinalizeDone(t, finalizeDone)

		if uSvc.membershipCalls.Load() != 1 {
			t.Fatalf("expected 1 GetPrimaryMembership call, got %d", uSvc.membershipCalls.Load())
		}
		if snapSvc.saveCalls.Load() != 0 || d2Svc.calls.Load() != 0 {
			t.Fatalf("expected 0 Bungie calls on membership lookup failure, got save=%d comp=%d", snapSvc.saveCalls.Load(), d2Svc.calls.Load())
		}
		bgBag := clog.BagFromContext(bgCtx)
		if !bgBag.HasWarning() {
			t.Fatal("expected warning recorded on membership lookup failure")
		}
		assertAllWarningsAreStrings(t, bgBag.Attrs())
		if status, ok := findStringAttr(bgBag.Attrs(), "finalLoadoutStatus"); !ok || status != "failed" {
			t.Fatalf("expected finalLoadoutStatus=failed, got %q (found=%v)", status, ok)
		}
		if status, ok := findStringAttr(bgBag.Attrs(), "compFinalizeStatus"); !ok || status != "fallback_run_tick" {
			t.Fatalf("expected compFinalizeStatus=fallback_run_tick, got %q (found=%v)", status, ok)
		}
	})

	t.Run("deduplicates overlapping background finalizations and allows retry after completion", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-7"] = &api.Session{
			ID:          "ses-7",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			Status:      new(api.SessionPending),
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 5000},
			},
		}

		firstEntered := make(chan struct{})
		releaseFirst := make(chan struct{})
		var attempt atomic.Int32
		d2Svc := &fakeSessionD2Service{
			getCompRankFn: func(_ context.Context, _, _ int64, _ string) (*api.CompRankSnapshot, error) {
				if attempt.Add(1) == 1 {
					close(firstEntered)
					<-releaseFirst
					return nil, errors.New("temporary bungie failure")
				}
				return &api.CompRankSnapshot{Points: 5200}, nil
			},
		}
		snapSvc := &fakeSessionSnapshotService{
			checkInFn: func(_, _, _ string, _ snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				return snapshot.SessionCheckInResult{
					SnapshotID:      "snap-7",
					SnapshotHash:    "hash-7",
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
		finalizeDone := make(chan context.Context, 2)
		svc := &service{
			store:                 store,
			userService:           uSvc,
			snapshotService:       snapSvc,
			d2Service:             d2Svc,
			nowFn:                 func() time.Time { return t0 },
			inFlightFinalizations: &sync.Map{},
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		if _, err := svc.Complete(clog.NewContext(context.Background()), "ses-7"); err != nil {
			t.Fatalf("first Complete failed: %v", err)
		}
		select {
		case <-firstEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for first background finalization to start")
		}

		// Rapid second Complete while first finalization is still in flight must be deduplicated.
		dupCtx := clog.NewContext(context.Background())
		if _, err := svc.Complete(dupCtx, "ses-7"); err != nil {
			t.Fatalf("second Complete failed: %v", err)
		}
		dupAttrs := clog.BagFromContext(dupCtx).Attrs()
		if dedup, ok := findBoolAttr(dupAttrs, "finalizationDeduplicated"); !ok || !dedup {
			t.Fatalf("expected finalizationDeduplicated=true on overlapping Complete, got %v (found=%v)", dedup, ok)
		}
		if async, ok := findBoolAttr(dupAttrs, "finalizationAsync"); !ok || async {
			t.Fatalf("expected finalizationAsync=false on overlapping Complete, got %v (found=%v)", async, ok)
		}
		if d2Svc.calls.Load() != 1 {
			t.Fatalf("expected overlapping Complete not to start a second comp rank call, got %d", d2Svc.calls.Load())
		}

		// Let the first attempt fail so CompTracking.End stays nil and lock is released.
		close(releaseFirst)
		waitFinalizeDone(t, finalizeDone)

		// A subsequent Complete should now acquire the slot and finish comp finalization.
		retryCtx := clog.NewContext(context.Background())
		if _, err := svc.Complete(retryCtx, "ses-7"); err != nil {
			t.Fatalf("retry Complete failed: %v", err)
		}
		if async, ok := findBoolAttr(clog.BagFromContext(retryCtx).Attrs(), "finalizationAsync"); !ok || !async {
			t.Fatalf("expected finalizationAsync=true on retry after in-flight release, got %v (found=%v)", async, ok)
		}
		waitFinalizeDone(t, finalizeDone)

		if d2Svc.calls.Load() != 2 {
			t.Fatalf("expected 2 total comp rank calls across initial + retry, got %d", d2Svc.calls.Load())
		}
		store.mu.Lock()
		persisted := cloneSession(store.docs["ses-7"])
		store.mu.Unlock()
		if persisted.CompTracking == nil || persisted.CompTracking.End == nil || persisted.CompTracking.End.Points != 5200 {
			t.Fatalf("expected CompTracking.End=5200 after retry, got %+v", persisted.CompTracking)
		}
	})

	t.Run("comp-only finalization uses comp timeout for membership lookup", func(t *testing.T) {
		store := newMemorySessionStore()
		completedAt := t0.Add(-1 * time.Minute)
		store.docs["ses-8"] = &api.Session{
			ID:          "ses-8",
			UserID:      "user-1",
			CharacterID: "char-1",
			StartedAt:   t0.Add(-10 * time.Minute),
			CompletedAt: &completedAt,
			Status:      new(api.SessionComplete),
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 5000},
			},
		}

		var (
			mu          sync.Mutex
			hasDeadline bool
			remaining   time.Duration
		)
		uSvc := &fakeSessionUserService{
			getPrimaryMembershipFn: func(ctx context.Context, _ string) (int64, int64, error) {
				dl, ok := ctx.Deadline()
				mu.Lock()
				hasDeadline = ok
				if ok {
					remaining = time.Until(dl)
				}
				mu.Unlock()
				return 12345, 3, nil
			},
		}
		d2Svc := &fakeSessionD2Service{
			snapshots: []*api.CompRankSnapshot{{Points: 5100}},
		}
		finalizeDone := make(chan context.Context, 1)
		svc := &service{
			store:               store,
			userService:         uSvc,
			d2Service:           d2Svc,
			nowFn:               func() time.Time { return t0 },
			finalCheckInTimeout: 10 * time.Millisecond,
			finalCompTimeout:    250 * time.Millisecond,
			onFinalizeDone: func(bgCtx context.Context, _ error) {
				finalizeDone <- bgCtx
			},
		}

		if _, err := svc.Complete(clog.NewContext(context.Background()), "ses-8"); err != nil {
			t.Fatalf("Complete failed: %v", err)
		}
		waitFinalizeDone(t, finalizeDone)

		mu.Lock()
		gotDeadline := hasDeadline
		gotRemaining := remaining
		mu.Unlock()
		if !gotDeadline {
			t.Fatal("expected membership lookup context to have a deadline")
		}
		if gotRemaining <= 50*time.Millisecond {
			t.Fatalf("expected comp-only membership lookup to use finalCompTimeout (~250ms), got remaining=%v", gotRemaining)
		}
	})
}

func TestRunTick_FireteamSharedActivityDeduplicatesPGCRAndStampsParticipantMetadata(t *testing.T) {
	now := time.Date(2026, 6, 1, 18, 0, 0, 0, time.UTC)
	matchTime := now.Add(-5 * time.Minute)
	soloTime := now.Add(-10 * time.Minute)

	store := newMemorySessionStore()
	store.docs["ses-1"] = &api.Session{
		ID:          "ses-1",
		UserID:      "user-1",
		CharacterID: "char-1",
		StartedAt:   now.Add(-30 * time.Minute),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-2"] = &api.Session{
		ID:          "ses-2",
		UserID:      "user-2",
		CharacterID: "char-2",
		StartedAt:   now.Add(-30 * time.Minute),
		Status:      new(api.SessionPending),
	}
	store.docs["ses-3"] = &api.Session{
		ID:          "ses-3",
		UserID:      "user-3",
		CharacterID: "char-3",
		StartedAt:   now.Add(-30 * time.Minute),
		Status:      new(api.SessionPending),
	}

	uSvc := &fakeSessionUserService{
		users: map[string]*user.User{
			"user-1": {
				ID:                  "user-1",
				DisplayName:         "Weegee",
				PrimaryMembershipID: "10001",
				Memberships:         []user.Membership{{ID: "10001", Type: 3}},
				Characters: []api.Character{
					{Id: "char-1", Class: "Hunter", EmblemURL: "https://www.bungie.net/emblem-1.jpg"},
				},
			},
			"user-2": {
				ID:                  "user-2",
				DisplayName:         "Maverick",
				PrimaryMembershipID: "10002",
				Memberships:         []user.Membership{{ID: "10002", Type: 3}},
				Characters: []api.Character{
					{Id: "char-2", Class: "Titan", EmblemURL: "https://www.bungie.net/emblem-2.jpg"},
				},
			},
			"user-3": {
				ID:                  "user-3",
				DisplayName:         "Nova",
				PrimaryMembershipID: "10003",
				Memberships:         []user.Membership{{ID: "10003", Type: 2}},
				Characters: []api.Character{
					{Id: "char-3", Class: "Warlock", EmblemURL: "https://www.bungie.net/emblem-3.jpg"},
				},
			},
		},
	}

	sharedActivity := api.ActivityHistory{
		InstanceID: "act-shared",
		Activity:   "Trials of Osiris",
		Mode:       new("Trials of Osiris"),
		Period:     matchTime,
	}
	soloActivity := api.ActivityHistory{
		InstanceID: "act-solo-1",
		Activity:   "Control",
		Mode:       new("Control"),
		Period:     soloTime,
	}

	makePerf := func(kills float64) api.InstancePerformance {
		return api.InstancePerformance{
			PlayerStats: api.PlayerStats{
				Standing:   statVal(0),
				Kills:      statVal(kills),
				Deaths:     statVal(4),
				Team:       statVal(17),
				FireTeamID: statVal(424242),
			},
			Weapons: map[string]api.WeaponInstanceMetrics{
				"1001": weaponMetrics(1001, kills, "Rose", "/img/rose.png"),
			},
		}
	}

	d2Svc := &fakeSessionD2Service{
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-1": {sharedActivity, soloActivity},
			"char-2": {sharedActivity},
			"char-3": {sharedActivity},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-shared": {
				"char-1": makePerf(12),
				"char-2": makePerf(9),
				"char-3": makePerf(7),
			},
			"act-solo-1": {
				"char-1": makePerf(15),
			},
		},
	}
	aggSvc := newFakeSessionAggregateService()
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

	ctx := clog.NewContext(context.Background())
	resp, err := svc.RunTick(ctx, TickOptions{SkipSave: true})
	if err != nil {
		t.Fatalf("RunTick failed: %v", err)
	}

	if resp.SessionsProcessed != 3 || resp.ErrorsTotal != 0 {
		t.Fatalf("expected 3 sessions processed with 0 errors, got %+v", resp)
	}
	if d2Svc.perfCallsByAct["act-shared"] != 1 {
		t.Fatalf("expected act-shared PGCR to be fetched once across 3 fireteam sessions, got %d", d2Svc.perfCallsByAct["act-shared"])
	}
	if d2Svc.perfCallsByAct["act-solo-1"] != 1 || d2Svc.perfCalls.Load() != 2 {
		t.Fatalf("expected 1 fetch for act-solo-1 and 2 total PGCR fetches, got solo=%d total=%d", d2Svc.perfCallsByAct["act-solo-1"], d2Svc.perfCalls.Load())
	}
	for _, wantChar := range []string{"char-1", "char-2", "char-3"} {
		if !slices.Contains(d2Svc.perfCharIDsByAct["act-shared"], wantChar) {
			t.Fatalf("expected GetPerformances for act-shared to request %s, got %v", wantChar, d2Svc.perfCharIDsByAct["act-shared"])
		}
	}
	if uSvc.userCalls.Load() != 3 || uSvc.membershipCalls.Load() != 0 {
		t.Fatalf("expected 3 GetUser calls (1 per user) and 0 GetPrimaryMembership calls, got userCalls=%d membershipCalls=%d",
			uSvc.userCalls.Load(), uSvc.membershipCalls.Load())
	}

	if hits, ok := findIntAttr(clog.BagFromContext(ctx).Attrs(), "pgcrCacheHits"); !ok || hits != 2 {
		t.Fatalf("expected pgcrCacheHits=2 in canonical log, got %d (found=%v)", hits, ok)
	}

	sharedAgg, ok := aggSvc.byActivity["act-shared"]
	if !ok {
		t.Fatal("expected aggregate to be written for act-shared")
	}
	if len(sharedAgg.SnapshotLinks) != 3 {
		t.Fatalf("expected 3 SnapshotLinks on shared aggregate, got %d", len(sharedAgg.SnapshotLinks))
	}

	type wantMeta struct {
		sessionID   string
		userID      string
		displayName string
		class       string
		emblemURL   string
	}
	expected := map[string]wantMeta{
		"char-1": {sessionID: "ses-1", userID: "user-1", displayName: "Weegee", class: "Hunter", emblemURL: "https://www.bungie.net/emblem-1.jpg"},
		"char-2": {sessionID: "ses-2", userID: "user-2", displayName: "Maverick", class: "Titan", emblemURL: "https://www.bungie.net/emblem-2.jpg"},
		"char-3": {sessionID: "ses-3", userID: "user-3", displayName: "Nova", class: "Warlock", emblemURL: "https://www.bungie.net/emblem-3.jpg"},
	}

	for charID, want := range expected {
		link, exists := sharedAgg.SnapshotLinks[charID]
		if !exists {
			t.Fatalf("missing SnapshotLink for %s", charID)
		}
		if link.SessionID == nil || *link.SessionID != want.sessionID {
			t.Errorf("%s: expected SessionID=%q, got %v", charID, want.sessionID, link.SessionID)
		}
		if link.UserID == nil || *link.UserID != want.userID {
			t.Errorf("%s: expected UserID=%q, got %v", charID, want.userID, link.UserID)
		}
		if link.DisplayName == nil || *link.DisplayName != want.displayName {
			t.Errorf("%s: expected DisplayName=%q, got %v", charID, want.displayName, link.DisplayName)
		}
		if link.CharacterClass == nil || *link.CharacterClass != want.class {
			t.Errorf("%s: expected CharacterClass=%q, got %v", charID, want.class, link.CharacterClass)
		}
		if link.EmblemURL == nil || *link.EmblemURL != want.emblemURL {
			t.Errorf("%s: expected EmblemURL=%q, got %v", charID, want.emblemURL, link.EmblemURL)
		}
	}
}

func TestRunTick_ParticipantMetadataOmitsEmptyFieldsAndReusesUserReadWithinTick(t *testing.T) {
	now := time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)
	completedAt := now.Add(-5 * time.Minute)

	store := newMemorySessionStore()
	store.docs["ses-a"] = &api.Session{
		ID:          "ses-a",
		UserID:      "user-swap",
		CharacterID: "char-hunter",
		StartedAt:   now.Add(-30 * time.Minute),
		CompletedAt: &completedAt,
		Status:      new(api.SessionComplete),
	}
	store.docs["ses-b"] = &api.Session{
		ID:          "ses-b",
		UserID:      "user-swap",
		CharacterID: "char-unlisted",
		StartedAt:   now.Add(-15 * time.Minute),
		Status:      new(api.SessionPending),
	}

	uSvc := &fakeSessionUserService{
		users: map[string]*user.User{
			"user-swap": {
				ID:                  "user-swap",
				DisplayName:         "Switcher",
				PrimaryMembershipID: "20001",
				Memberships:         []user.Membership{{ID: "20001", Type: 3}},
				Characters: []api.Character{
					{Id: "char-hunter", Class: "Hunter", EmblemURL: "https://www.bungie.net/hunter.jpg"},
				},
			},
		},
	}

	d2Svc := &fakeSessionD2Service{
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-hunter": {
				{InstanceID: "act-a", Activity: "Control", Mode: new("Control"), Period: now.Add(-10 * time.Minute)},
			},
			"char-unlisted": {
				{InstanceID: "act-b", Activity: "Control", Mode: new("Control"), Period: now.Add(-2 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-a": {
				"char-hunter": {
					PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(10), Deaths: statVal(5)},
					Weapons:     map[string]api.WeaponInstanceMetrics{"1001": weaponMetrics(1001, 10, "Rose", "/img/rose.png")},
				},
			},
			"act-b": {
				"char-unlisted": {
					PlayerStats: api.PlayerStats{Standing: statVal(1), Kills: statVal(8), Deaths: statVal(8)},
					Weapons:     map[string]api.WeaponInstanceMetrics{"1001": weaponMetrics(1001, 8, "Rose", "/img/rose.png")},
				},
			},
		},
	}

	aggSvc := newFakeSessionAggregateService()
	svc := &service{
		store:            store,
		aggregateService: aggSvc,
		userService:      uSvc,
		d2Service:        d2Svc,
		snapshotService:  &fakeSessionSnapshotService{},
		manifestService:  &fakeSessionManifestService{},
		nowFn:            func() time.Time { return now },
	}

	if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true}); err != nil {
		t.Fatalf("RunTick failed: %v", err)
	}

	if uSvc.userCalls.Load() != 1 {
		t.Fatalf("expected GetUser to be memoized to 1 call for same user across 2 sessions in one tick, got %d", uSvc.userCalls.Load())
	}

	linkA := aggSvc.byActivity["act-a"].SnapshotLinks["char-hunter"]
	if linkA.UserID == nil || *linkA.UserID != "user-swap" ||
		linkA.DisplayName == nil || *linkA.DisplayName != "Switcher" ||
		linkA.CharacterClass == nil || *linkA.CharacterClass != "Hunter" ||
		linkA.EmblemURL == nil || *linkA.EmblemURL != "https://www.bungie.net/hunter.jpg" {
		t.Fatalf("unexpected stamped metadata for char-hunter: %+v", linkA)
	}

	linkB := aggSvc.byActivity["act-b"].SnapshotLinks["char-unlisted"]
	if linkB.UserID == nil || *linkB.UserID != "user-swap" || linkB.DisplayName == nil || *linkB.DisplayName != "Switcher" {
		t.Fatalf("expected userId and displayName stamped for char-unlisted, got %+v", linkB)
	}
	if linkB.CharacterClass != nil || linkB.EmblemURL != nil {
		t.Fatalf("expected nil CharacterClass and EmblemURL when character missing from user doc, got class=%v emblem=%v", linkB.CharacterClass, linkB.EmblemURL)
	}
}

func TestRunTick_PGCRCacheScopedPerTick(t *testing.T) {
	now := time.Date(2026, 6, 1, 20, 0, 0, 0, time.UTC)

	store := newMemorySessionStore()
	store.docs["ses-retry"] = &api.Session{
		ID:          "ses-retry",
		UserID:      "user-1",
		CharacterID: "char-1",
		StartedAt:   now.Add(-20 * time.Minute),
		Status:      new(api.SessionPending),
	}

	uSvc := &fakeSessionUserService{
		u: &user.User{
			ID:                  "user-1",
			DisplayName:         "Weegee",
			PrimaryMembershipID: "10001",
			Memberships:         []user.Membership{{ID: "10001", Type: 3}},
			Characters: []api.Character{
				{Id: "char-1", Class: "Hunter", EmblemURL: "https://www.bungie.net/emblem-1.jpg"},
			},
		},
	}

	d2Svc := &fakeSessionD2Service{
		activitiesByChr: map[string][]api.ActivityHistory{
			"char-1": {
				{InstanceID: "act-retry", Activity: "Control", Mode: new("Control"), Period: now.Add(-5 * time.Minute)},
			},
		},
		perfsByAct: map[string]map[string]api.InstancePerformance{
			"act-retry": {
				"char-1": {PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(11), Deaths: statVal(5)}},
			},
		},
	}

	aggSvc := newFakeSessionAggregateService()
	aggSvc.nowFn = func() time.Time { return now }
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
			snapID := "snap-1"
			return &api.CharacterSnapshot{ID: snapID}, &api.SnapshotLink{
				CharacterID:      characterID,
				SnapshotID:       &snapID,
				ConfidenceLevel:  api.HighConfidenceLevel,
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

	if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true}); err != nil {
		t.Fatalf("Tick 1 failed: %v", err)
	}
	if d2Svc.perfCalls.Load() != 1 {
		t.Fatalf("expected 1 PGCR call on Tick 1, got %d", d2Svc.perfCalls.Load())
	}

	// Simulate Bungie populating extended weapon stats before Tick 2.
	d2Svc.perfsByAct["act-retry"]["char-1"] = api.InstancePerformance{
		PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(11), Deaths: statVal(5)},
		Weapons:     map[string]api.WeaponInstanceMetrics{"1001": weaponMetrics(1001, 11, "Rose", "/img/rose.png")},
	}
	now = now.Add(5 * time.Minute)

	if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true}); err != nil {
		t.Fatalf("Tick 2 failed: %v", err)
	}
	if d2Svc.perfCalls.Load() != 2 {
		t.Fatalf("expected Tick 2 to fetch fresh PGCR (not reuse Tick 1 cache), got %d calls", d2Svc.perfCalls.Load())
	}
	refreshedLink := aggSvc.byActivity["act-retry"].SnapshotLinks["char-1"]
	if refreshedLink.SnapshotID == nil || *refreshedLink.SnapshotID != "snap-1" {
		t.Fatalf("expected refreshed SnapshotID=snap-1, got %+v", refreshedLink)
	}
	if refreshedLink.UserID == nil || *refreshedLink.UserID != "user-1" ||
		refreshedLink.DisplayName == nil || *refreshedLink.DisplayName != "Weegee" ||
		refreshedLink.CharacterClass == nil || *refreshedLink.CharacterClass != "Hunter" ||
		refreshedLink.EmblemURL == nil || *refreshedLink.EmblemURL != "https://www.bungie.net/emblem-1.jpg" {
		t.Fatalf("expected refreshed link to retain stamped participant metadata, got %+v", refreshedLink)
	}
}

func TestRunTick_CandidateCharacterIDsExcludesExpiredAndGetUserGuardsAndAliases(t *testing.T) {
	now := time.Date(2026, 6, 1, 21, 0, 0, 0, time.UTC)
	staleLastSeen := now.Add(-CutOffDuration - 10*time.Minute)
	completedAt := now.Add(-5 * time.Minute)

	t.Run("excludes stale and inactive candidates from GetPerformances filter", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-active"] = &api.Session{
			ID:          "ses-active",
			UserID:      "user-1",
			CharacterID: "char-active",
			StartedAt:   now.Add(-20 * time.Minute),
			Status:      new(api.SessionPending),
		}
		store.docs["ses-completed-recent"] = &api.Session{
			ID:          "ses-completed-recent",
			UserID:      "user-2",
			CharacterID: "char-completed",
			StartedAt:   now.Add(-25 * time.Minute),
			CompletedAt: &completedAt,
			Status:      new(api.SessionComplete),
		}
		store.docs["ses-stale"] = &api.Session{
			ID:                "ses-stale",
			UserID:            "user-3",
			CharacterID:       "char-stale",
			StartedAt:         now.Add(-3 * time.Hour),
			LastSeenTimestamp: &staleLastSeen,
			Status:            new(api.SessionPending),
		}
		store.docs["ses-inactive"] = &api.Session{
			ID:          "ses-inactive",
			UserID:      "user-4",
			CharacterID: "char-inactive",
			StartedAt:   now.Add(-CutOffDuration - 5*time.Minute),
			Status:      new(api.SessionPending),
		}

		uSvc := &fakeSessionUserService{
			users: map[string]*user.User{
				"user-1": {ID: "user-1", DisplayName: "One", PrimaryMembershipID: "1001", Memberships: []user.Membership{{ID: "1001", Type: 3}}},
				"user-2": {ID: "user-2", DisplayName: "Two", PrimaryMembershipID: "1002", Memberships: []user.Membership{{ID: "1002", Type: 3}}},
			},
		}
		d2Svc := &fakeSessionD2Service{
			activitiesByChr: map[string][]api.ActivityHistory{
				"char-active": {
					{InstanceID: "act-1", Activity: "Control", Mode: new("Control"), Period: now.Add(-5 * time.Minute)},
				},
			},
			perfsByAct: map[string]map[string]api.InstancePerformance{
				"act-1": {
					"char-active": {
						PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(10), Deaths: statVal(5)},
						Weapons:     map[string]api.WeaponInstanceMetrics{"1001": weaponMetrics(1001, 10, "Rose", "/img/rose.png")},
					},
				},
			},
		}

		svc := &service{
			store:            store,
			aggregateService: newFakeSessionAggregateService(),
			userService:      uSvc,
			d2Service:        d2Svc,
			snapshotService:  &fakeSessionSnapshotService{},
			manifestService:  &fakeSessionManifestService{},
			nowFn:            func() time.Time { return now },
		}

		if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true}); err != nil {
			t.Fatalf("RunTick failed: %v", err)
		}

		requested := d2Svc.perfCharIDsByAct["act-1"]
		if !slices.Contains(requested, "char-active") || !slices.Contains(requested, "char-completed") {
			t.Fatalf("expected active and recent completed characters in filter, got %v", requested)
		}
		if slices.Contains(requested, "char-stale") || slices.Contains(requested, "char-inactive") {
			t.Fatalf("expected stale and inactive characters excluded from filter, got %v", requested)
		}
	})

	t.Run("getUser short-circuits empty userID and caches canonical u.ID", func(t *testing.T) {
		canonicalUser := &user.User{
			ID:                  "doc-user-1",
			PrimaryMembershipID: "4611686018400001",
			DisplayName:         "Weegee",
			Memberships:         []user.Membership{{ID: "4611686018400001", Type: 3}},
		}
		uSvc := &fakeSessionUserService{
			users: map[string]*user.User{
				"4611686018400001": canonicalUser,
				"doc-user-1":       canonicalUser,
			},
		}
		runner := newTickRunner(service{userService: uSvc, nowFn: func() time.Time { return now }}, TickOptions{})

		if _, err := runner.getUser(context.Background(), ""); !errors.Is(err, user.NotFound) {
			t.Fatalf("expected user.NotFound for empty userID, got %v", err)
		}
		if uSvc.userCalls.Load() != 0 {
			t.Fatalf("expected 0 GetUser calls for empty userID, got %d", uSvc.userCalls.Load())
		}

		first, err := runner.getUser(context.Background(), "4611686018400001")
		if err != nil || first == nil || first.ID != "doc-user-1" {
			t.Fatalf("unexpected first lookup result: user=%+v err=%v", first, err)
		}
		second, err := runner.getUser(context.Background(), "doc-user-1")
		if err != nil || second != first {
			t.Fatalf("expected canonical ID cache hit, got user=%+v err=%v", second, err)
		}
		if uSvc.userCalls.Load() != 1 {
			t.Fatalf("expected 1 GetUser call across lookup alias + canonical ID, got %d", uSvc.userCalls.Load())
		}
	})
}

func TestRunTick_ConcurrentCandidateProcessingBoundedWallClockAndRaceSafety(t *testing.T) {
	now := time.Date(2026, 6, 2, 18, 0, 0, 0, time.UTC)
	callLatency := 25 * time.Millisecond

	recordPeak := func(cur, peak *atomic.Int32) func() {
		n := cur.Add(1)
		for {
			prev := peak.Load()
			if n <= prev || peak.CompareAndSwap(prev, n) {
				break
			}
		}
		return func() { cur.Add(-1) }
	}

	t.Run("processes 24 candidate sessions concurrently within default cap and preserves invariants", func(t *testing.T) {
		store := newMemorySessionStore()
		aggSvc := newFakeSessionAggregateService()
		aggSvc.nowFn = func() time.Time { return now }

		aggSvc.seed(api.Aggregate{
			ID:              "agg-seed-comp",
			ActivityID:      "act-seed-comp",
			ActivityDetails: api.ActivityHistory{InstanceID: "act-seed-comp", Activity: "Control", Mode: new("Control"), Period: now.Add(-15 * time.Minute)},
			CharacterIds:    []string{"char-comp-grace"},
			SessionIds:      []string{"ses-comp-grace"},
			Performance: map[string]api.InstancePerformance{
				"char-comp-grace": {PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(12), Deaths: statVal(6)}},
			},
		})
		aggSvc.seed(api.Aggregate{
			ID:              "agg-seed-stale",
			ActivityID:      "act-seed-stale",
			ActivityDetails: api.ActivityHistory{InstanceID: "act-seed-stale", Activity: "Control", Mode: new("Control"), Period: now.Add(-2 * time.Hour)},
			CharacterIds:    []string{"char-stale"},
			SessionIds:      []string{"ses-stale-comp"},
			Performance: map[string]api.InstancePerformance{
				"char-stale": {PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(9), Deaths: statVal(7)}},
			},
		})

		users := make(map[string]*user.User)
		activitiesByChr := make(map[string][]api.ActivityHistory)
		perfsByAct := make(map[string]map[string]api.InstancePerformance)

		addUser := func(uid, pmID, charID string) {
			if existing, ok := users[uid]; ok {
				existing.CharacterIDs = append(existing.CharacterIDs, charID)
				existing.Characters = append(existing.Characters, api.Character{Id: charID, Class: "Hunter"})
				return
			}
			users[uid] = &user.User{
				ID:                  uid,
				DisplayName:         "Player-" + uid,
				PrimaryMembershipID: pmID,
				Memberships:         []user.Membership{{ID: pmID, Type: 3}},
				CharacterIDs:        []string{charID},
				Characters:          []api.Character{{Id: charID, Class: "Hunter"}},
			}
		}

		completePerf := func(kills float64) api.InstancePerformance {
			return api.InstancePerformance{
				PlayerStats: api.PlayerStats{Standing: statVal(0), Kills: statVal(kills), Assists: statVal(4), Deaths: statVal(5)},
				Weapons:     map[string]api.WeaponInstanceMetrics{"1001": weaponMetrics(1001, kills, "Rose", "/img/rose.png")},
			}
		}

		for i := 1; i <= 14; i++ {
			sid := fmt.Sprintf("ses-solo-%02d", i)
			uid := fmt.Sprintf("user-solo-%02d", i)
			cid := fmt.Sprintf("char-solo-%02d", i)
			aid := fmt.Sprintf("act-solo-%02d", i)
			pmID := strconv.Itoa(7000 + i)

			store.docs[sid] = &api.Session{
				ID:          sid,
				UserID:      uid,
				CharacterID: cid,
				StartedAt:   now.Add(-30 * time.Minute),
				Status:      new(api.SessionPending),
			}
			addUser(uid, pmID, cid)
			activitiesByChr[cid] = []api.ActivityHistory{
				{InstanceID: aid, Activity: "Control", Mode: new("Control"), Period: now.Add(-5 * time.Minute)},
			}
			perfsByAct[aid] = map[string]api.InstancePerformance{cid: completePerf(float64(10 + i))}
		}

		perfsByAct["act-fireteam"] = make(map[string]api.InstancePerformance, 3)
		for i := 1; i <= 3; i++ {
			sid := fmt.Sprintf("ses-ft-%d", i)
			uid := fmt.Sprintf("user-ft-%d", i)
			cid := fmt.Sprintf("char-ft-%d", i)
			pmID := strconv.Itoa(8000 + i)

			store.docs[sid] = &api.Session{
				ID:          sid,
				UserID:      uid,
				CharacterID: cid,
				StartedAt:   now.Add(-25 * time.Minute),
				Status:      new(api.SessionPending),
			}
			addUser(uid, pmID, cid)
			activitiesByChr[cid] = []api.ActivityHistory{
				{InstanceID: "act-fireteam", Activity: "Trials", Mode: new("TrialsOfOsiris"), Period: now.Add(-4 * time.Minute)},
			}
			perfsByAct["act-fireteam"][cid] = completePerf(float64(8 + i))
		}

		for i := 1; i <= 2; i++ {
			sid := fmt.Sprintf("ses-multi-%d", i)
			cid := fmt.Sprintf("char-multi-%d", i)
			aid := fmt.Sprintf("act-multi-%d", i)

			store.docs[sid] = &api.Session{
				ID:          sid,
				UserID:      "user-shared",
				CharacterID: cid,
				StartedAt:   now.Add(-20 * time.Minute),
				Status:      new(api.SessionPending),
			}
			addUser("user-shared", "9001", cid)
			activitiesByChr[cid] = []api.ActivityHistory{
				{InstanceID: aid, Activity: "Rumble", Mode: new("Rumble"), Period: now.Add(-3 * time.Minute)},
			}
			perfsByAct[aid] = map[string]api.InstancePerformance{cid: completePerf(float64(15 + i))}
		}

		store.docs["ses-warn"] = &api.Session{
			ID:          "ses-warn",
			UserID:      "user-warn",
			CharacterID: "char-warn",
			StartedAt:   now.Add(-20 * time.Minute),
			Status:      new(api.SessionPending),
		}
		addUser("user-warn", "9101", "char-warn")
		activitiesByChr["char-warn"] = []api.ActivityHistory{
			{InstanceID: "act-warn", Activity: "Control", Mode: new("Control"), Period: now.Add(-2 * time.Minute)},
		}
		perfsByAct["act-warn"] = map[string]api.InstancePerformance{}

		store.docs["ses-err"] = &api.Session{
			ID:          "ses-err",
			UserID:      "user-err",
			CharacterID: "char-err",
			StartedAt:   now.Add(-20 * time.Minute),
			Status:      new(api.SessionPending),
		}
		addUser("user-err", "9201", "char-err")

		completedAt := now.Add(-3 * time.Minute)
		store.docs["ses-comp-grace"] = &api.Session{
			ID:           "ses-comp-grace",
			UserID:       "user-comp-grace",
			CharacterID:  "char-comp-grace",
			StartedAt:    now.Add(-40 * time.Minute),
			CompletedAt:  &completedAt,
			Status:       new(api.SessionComplete),
			AggregateIDs: []string{"agg-seed-comp"},
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 5000},
			},
		}
		addUser("user-comp-grace", "9301", "char-comp-grace")

		staleSeen := now.Add(-CutOffDuration - 10*time.Minute)
		store.docs["ses-stale-comp"] = &api.Session{
			ID:                "ses-stale-comp",
			UserID:            "user-stale",
			CharacterID:       "char-stale",
			StartedAt:         now.Add(-3 * time.Hour),
			LastSeenTimestamp: &staleSeen,
			Status:            new(api.SessionPending),
			AggregateIDs:      []string{"agg-seed-stale"},
			CompTracking: &api.CompTracking{
				Enabled: true,
				Start:   &api.CompRankSnapshot{Points: 4200},
			},
		}
		addUser("user-stale", "9401", "char-stale")

		store.docs["ses-inactive"] = &api.Session{
			ID:          "ses-inactive",
			UserID:      "user-inactive",
			CharacterID: "char-inactive",
			StartedAt:   now.Add(-CutOffDuration - 5*time.Minute),
			Status:      new(api.SessionPending),
		}
		addUser("user-inactive", "9501", "char-inactive")

		var inFlight, peakInFlight atomic.Int32

		uSvc := &fakeSessionUserService{
			users: users,
			getPrimaryMembershipFn: func(_ context.Context, userID string) (int64, int64, error) {
				u, ok := users[userID]
				if !ok {
					return 0, 0, user.NotFound
				}
				return u.PrimaryMembership()
			},
		}

		snapSvc := &fakeSessionSnapshotService{
			checkInCtxFn: func(_ context.Context, _, _, characterID string, _ snapshot.SessionCheckInOptions) (snapshot.SessionCheckInResult, error) {
				done := recordPeak(&inFlight, &peakInFlight)
				defer done()
				time.Sleep(callLatency)
				return snapshot.SessionCheckInResult{
					SnapshotID:      "snap-" + characterID,
					SnapshotHash:    "hash-" + characterID,
					HistoryRecorded: true,
					RecordedAt:      now,
				}, nil
			},
		}

		d2Svc := &fakeSessionD2Service{
			getActivityFn: func(_ context.Context, _ string, _ int64, characterID string) ([]api.ActivityHistory, error) {
				done := recordPeak(&inFlight, &peakInFlight)
				defer done()
				time.Sleep(callLatency)
				if characterID == "char-err" {
					return nil, errors.New("bungie 503 activity history")
				}
				return activitiesByChr[characterID], nil
			},
			getPerformancesFn: func(_ context.Context, activityID string, characterIDs []string) (map[string]api.InstancePerformance, error) {
				done := recordPeak(&inFlight, &peakInFlight)
				defer done()
				time.Sleep(callLatency)
				all := perfsByAct[activityID]
				filtered := make(map[string]api.InstancePerformance, len(characterIDs))
				for _, cid := range characterIDs {
					if perf, ok := all[cid]; ok {
						filtered[cid] = perf
					}
				}
				return filtered, nil
			},
			getCompRankFn: func(_ context.Context, _ int64, _ int64, _ string) (*api.CompRankSnapshot, error) {
				return &api.CompRankSnapshot{Points: 5150}, nil
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

		ctx := clog.NewContext(context.Background())
		start := time.Now()
		resp, err := svc.RunTick(ctx, TickOptions{})
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("RunTick failed: %v", err)
		}

		// Sequential minimum across 21 check-ins + 22 activity calls + 18 PGCR calls at 25ms each is >1.5s.
		if elapsed >= 950*time.Millisecond {
			t.Fatalf("expected concurrent RunTick to finish well below sequential floor (<950ms), took %v", elapsed)
		}
		if gotPeak := peakInFlight.Load(); gotPeak < 2 || gotPeak > defaultTickConcurrency {
			t.Fatalf("expected peak concurrent workers in [2, %d], got %d", defaultTickConcurrency, gotPeak)
		}

		if resp.TotalSessions != 24 {
			t.Fatalf("TotalSessions = %d, want 24", resp.TotalSessions)
		}
		if resp.SessionsProcessed != 19 {
			t.Fatalf("SessionsProcessed = %d, want 19", resp.SessionsProcessed)
		}
		if resp.SessionsSkipped != 2 {
			t.Fatalf("SessionsSkipped = %d, want 2", resp.SessionsSkipped)
		}
		if resp.SessionsErrored != 1 || resp.ErrorsTotal != 1 {
			t.Fatalf("SessionsErrored=%d ErrorsTotal=%d, want 1/1", resp.SessionsErrored, resp.ErrorsTotal)
		}
		if resp.SessionsStaleEnded != 1 {
			t.Fatalf("SessionsStaleEnded = %d, want 1", resp.SessionsStaleEnded)
		}
		if resp.SessionsInactiveDeleted != 1 {
			t.Fatalf("SessionsInactiveDeleted = %d, want 1", resp.SessionsInactiveDeleted)
		}
		if resp.LoadoutsSaved != 21 {
			t.Fatalf("LoadoutsSaved = %d, want 21", resp.LoadoutsSaved)
		}
		if resp.ActivitiesFoundTotal != 20 {
			t.Fatalf("ActivitiesFoundTotal = %d, want 20", resp.ActivitiesFoundTotal)
		}
		if resp.AggregatesAddedTotal != 19 {
			t.Fatalf("AggregatesAddedTotal = %d, want 19", resp.AggregatesAddedTotal)
		}
		if resp.CompFinalizedCount != 2 {
			t.Fatalf("CompFinalizedCount = %d, want 2", resp.CompFinalizedCount)
		}

		if got := d2Svc.perfCallsByAct["act-fireteam"]; got != 1 {
			t.Fatalf("expected 1 GetPerformances call for shared fireteam match, got %d", got)
		}
		ftAggs, err := aggSvc.GetAggregatesByActivity(context.Background(), []string{"act-fireteam"})
		if err != nil || len(ftAggs) != 1 {
			t.Fatalf("expected 1 shared fireteam aggregate, got len=%d err=%v", len(ftAggs), err)
		}
		if len(ftAggs[0].SnapshotLinks) != 3 || len(ftAggs[0].SessionIds) != 3 {
			t.Fatalf("expected all 3 fireteam members persisted without overwrite, got links=%d sessions=%v", len(ftAggs[0].SnapshotLinks), ftAggs[0].SessionIds)
		}

		if gotUsers := uSvc.userCalls.Load(); gotUsers != 21 {
			t.Fatalf("expected 21 coalesced GetUser calls across 22 active sessions, got %d", gotUsers)
		}

		bag := clog.BagFromContext(ctx)
		if bag == nil {
			t.Fatal("expected clog FieldBag on context")
		}
		if !bag.HasWarning() {
			t.Fatal("expected child missing_performance warning to roll up and mark parent bag HasWarning()=true")
		}
		attrs := bag.Attrs()
		assertAllWarningsAreStrings(t, attrs)

		if conc, ok := findIntAttr(attrs, "concurrency"); !ok || conc != defaultTickConcurrency {
			t.Fatalf("expected sessionService.runTick.concurrency=%d, got %d (ok=%v)", defaultTickConcurrency, conc, ok)
		}
		if hits, ok := findIntAttr(attrs, "pgcrCacheHits"); !ok || hits != 2 {
			t.Fatalf("expected pgcrCacheHits=2, got %d (ok=%v)", hits, ok)
		}
		if summaries, ok := findIntAttr(attrs, "summariesUpdated"); !ok || summaries != 19 {
			t.Fatalf("expected summariesUpdated=19, got %d (ok=%v)", summaries, ok)
		}
		if _, ok := findIntAttr(attrs, "wallDurationMs"); !ok {
			t.Fatal("expected wallDurationMs recorded by concurrent LoopRollupWithOptions")
		}
	})

	t.Run("honors custom MaxConcurrency cap", func(t *testing.T) {
		store := newMemorySessionStore()
		users := make(map[string]*user.User, 10)
		for i := 1; i <= 10; i++ {
			sid := fmt.Sprintf("ses-cap-%02d", i)
			uid := fmt.Sprintf("user-cap-%02d", i)
			cid := fmt.Sprintf("char-cap-%02d", i)
			pmID := strconv.Itoa(6000 + i)
			store.docs[sid] = &api.Session{
				ID:          sid,
				UserID:      uid,
				CharacterID: cid,
				StartedAt:   now.Add(-15 * time.Minute),
				Status:      new(api.SessionPending),
			}
			users[uid] = &user.User{
				ID:                  uid,
				PrimaryMembershipID: pmID,
				Memberships:         []user.Membership{{ID: pmID, Type: 3}},
			}
		}

		var inFlight, peakInFlight atomic.Int32
		d2Svc := &fakeSessionD2Service{
			getActivityFn: func(_ context.Context, _ string, _ int64, _ string) ([]api.ActivityHistory, error) {
				done := recordPeak(&inFlight, &peakInFlight)
				defer done()
				time.Sleep(20 * time.Millisecond)
				return nil, nil
			},
		}
		svc := &service{
			store:            store,
			aggregateService: newFakeSessionAggregateService(),
			userService:      &fakeSessionUserService{users: users},
			d2Service:        d2Svc,
			snapshotService:  &fakeSessionSnapshotService{},
			manifestService:  &fakeSessionManifestService{},
			nowFn:            func() time.Time { return now },
		}

		if _, err := svc.RunTick(clog.NewContext(context.Background()), TickOptions{SkipSave: true, MaxConcurrency: 3}); err != nil {
			t.Fatalf("RunTick failed: %v", err)
		}
		if gotPeak := peakInFlight.Load(); gotPeak < 2 || gotPeak > 3 {
			t.Fatalf("expected peak concurrent workers in [2, 3], got %d", gotPeak)
		}
	})
}

