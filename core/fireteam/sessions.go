package fireteam

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/set"
	"oneTrick/utils"
	"strings"
	"time"

	clog "github.com/Shannor/canonical-logger"
	"golang.org/x/sync/errgroup"
)

const (
	bulkCompRankConcurrency = 6
	maxStartSessionTargets  = 12
)

type authorizedParty struct {
	caller  Player
	members map[string]Player
}

type compRankLookup struct {
	snapshot CompRankSnapshot
	ok       bool
}

func (s *service) StartSession(
	ctx context.Context,
	actorUserID string,
	rawTarget StartSessionTarget,
	trackComp bool,
) (Session, error) {
	actorID := strings.TrimSpace(actorUserID)
	if actorID == "" {
		return Session{}, ErrUnauthorized
	}
	target, err := rawTarget.Normalized()
	if err != nil {
		return Session{}, err
	}
	if s.sessions == nil {
		return Session{}, errors.New("session store not configured")
	}

	clog.WithGroupFields(ctx, "fireteamService.startSession", clog.Fields{
		"actorUserId": actorID,
		"userId":      target.UserID,
		"characterId": target.CharacterID,
		"trackComp":   trackComp,
	})

	party, err := s.authorizeMembers(ctx, actorID, []string{target.UserID})
	if err != nil {
		return Session{}, err
	}
	target = party.canonicalizeTarget(target)

	activeSessions, err := s.sessions.GetActiveSessionsByUserIDs(ctx, []string{target.UserID})
	if err != nil {
		return Session{}, fmt.Errorf("failed to check active sessions: %w", err)
	}
	if _, active := indexLatestActiveSessions(activeSessions)[target.Key()]; active {
		return Session{}, ErrAlreadyActive
	}

	ses := newPendingSession(target.UserID, target.CharacterID, party.caller.AuditField(actorID), s.currentTime())
	if trackComp {
		var startSnap *CompRankSnapshot
		if member, ok := party.members[target.UserID]; ok {
			if snap, hasSnap, fetchErr := s.fetchStartingCompRank(ctx, member, target.CharacterID); fetchErr != nil {
				clog.WithGroupField(ctx, "fireteamService.startSession", "warning", fmt.Sprintf("failed to fetch starting competitive rank: %v", fetchErr))
			} else if hasSnap {
				startSnap = &snap
			}
		} else {
			clog.WithGroupField(ctx, "fireteamService.startSession", "warning", "failed to resolve member for competitive rank")
		}
		ses = ses.WithCompTracking(startSnap)
	}

	writeResults, err := s.sessions.CreateSessionsIfInactive(ctx, []Session{ses})
	if err != nil {
		return Session{}, fmt.Errorf("failed to create session: %w", err)
	}
	if len(writeResults) == 0 {
		return Session{}, errors.New("session store returned empty result")
	}
	if !writeResults[0].Created {
		return Session{}, ErrAlreadyActive
	}

	clog.WithGroupField(ctx, "fireteamService.startSession", "sessionId", writeResults[0].Session.ID)
	return writeResults[0].Session, nil
}

func (s *service) StartSessions(
	ctx context.Context,
	actorUserID string,
	targets []StartSessionTarget,
	trackComp bool,
) ([]StartSessionResult, error) {
	actorID := strings.TrimSpace(actorUserID)
	if actorID == "" {
		return nil, ErrUnauthorized
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%w: members must contain at least one target", ErrInvalidRequest)
	}
	if len(targets) > maxStartSessionTargets {
		return nil, fmt.Errorf("%w: members cannot exceed %d targets", ErrInvalidRequest, maxStartSessionTargets)
	}
	if s.sessions == nil {
		return nil, errors.New("session store not configured")
	}

	normalized, uniqueUserIDs, nonSelfCount, err := normalizeStartTargets(actorID, targets)
	if err != nil {
		return nil, err
	}

	clog.WithGroupFields(ctx, "fireteamService.startSessions", clog.Fields{
		"actorUserId":      actorID,
		"requestedCount":   len(targets),
		"uniquePairCount":  len(normalized),
		"nonSelfUserCount": nonSelfCount,
		"trackComp":        trackComp,
	})

	party, err := s.authorizeMembers(ctx, actorID, uniqueUserIDs)
	if err != nil {
		return nil, err
	}

	canonicalTargets, canonicalUserIDs := party.canonicalizeTargets(normalized)

	activeDocs, err := s.sessions.GetActiveSessionsByUserIDs(ctx, canonicalUserIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch active sessions: %w", err)
	}

	activeByPair := indexLatestActiveSessions(activeDocs)
	pendingTargets := filterPendingTargets(canonicalTargets, activeByPair)
	if len(pendingTargets) == 0 {
		results, startedCount, skippedCount := buildStartSessionResults(canonicalTargets, activeByPair, nil)
		clog.WithGroupFields(ctx, "fireteamService.startSessions", clog.Fields{
			"startedCount": startedCount,
			"skippedCount": skippedCount,
		})
		return results, nil
	}

	var compRanks []compRankLookup
	var compRankFailedCount int
	if trackComp {
		compRanks, compRankFailedCount = s.fetchBulkStartingCompRanks(ctx, pendingTargets, party)
	}

	newSessions := buildPendingSessions(
		pendingTargets,
		party.caller.AuditField(actorID),
		s.currentTime(),
		trackComp,
		compRanks,
	)

	writeResults, err := s.sessions.CreateSessionsIfInactive(ctx, newSessions)
	if err != nil {
		return nil, fmt.Errorf("failed to create fireteam sessions: %w", err)
	}
	if len(writeResults) != len(pendingTargets) {
		return nil, fmt.Errorf("session store returned %d session results, expected %d", len(writeResults), len(pendingTargets))
	}

	results, startedCount, skippedCount := buildStartSessionResults(canonicalTargets, activeByPair, writeResults)

	clog.WithGroupFields(ctx, "fireteamService.startSessions", clog.Fields{
		"startedCount":        startedCount,
		"skippedCount":        skippedCount,
		"compRankFailedCount": compRankFailedCount,
	})
	return results, nil
}

func normalizeStartTargets(actorID string, targets []StartSessionTarget) ([]StartSessionTarget, []string, int, error) {
	seenPairs := set.New[SessionKey]()
	normalized := make([]StartSessionTarget, 0, len(targets))
	uniqueUserIDs := set.NewOrdered[string]()
	nonSelfUserIDs := set.New[string]()

	for _, raw := range targets {
		target, err := raw.Normalized()
		if err != nil {
			return nil, nil, 0, err
		}
		key := target.Key()
		if seenPairs.Contains(key) {
			continue
		}
		seenPairs.Add(key)
		normalized = append(normalized, target)
		uniqueUserIDs.Add(target.UserID)
		if target.UserID != actorID {
			nonSelfUserIDs.Add(target.UserID)
		}
	}
	return normalized, uniqueUserIDs.ToSlice(), nonSelfUserIDs.Size(), nil
}

func (p authorizedParty) canonicalizeTarget(target StartSessionTarget) StartSessionTarget {
	if member, ok := p.members[target.UserID]; ok && member.ID != "" {
		target.UserID = member.ID
	}
	return target
}

func (p authorizedParty) canonicalizeTargets(targets []StartSessionTarget) ([]StartSessionTarget, []string) {
	seenPairs := set.New[SessionKey]()
	canonical := make([]StartSessionTarget, 0, len(targets))
	uniqueUserIDs := set.NewOrdered[string]()

	for _, target := range targets {
		resolved := p.canonicalizeTarget(target)
		key := resolved.Key()
		if seenPairs.Contains(key) {
			continue
		}
		seenPairs.Add(key)
		canonical = append(canonical, resolved)
		uniqueUserIDs.Add(resolved.UserID)
	}
	return canonical, uniqueUserIDs.ToSlice()
}

func indexLatestActiveSessions(docs []Session) map[SessionKey]Session {
	activeByPair := make(map[SessionKey]Session, len(docs))
	for _, doc := range docs {
		if doc.AggregateIDs == nil {
			doc.AggregateIDs = make([]string, 0)
		}
		key := doc.Key()
		if prev, exists := activeByPair[key]; exists && !doc.IsNewerThan(prev) {
			continue
		}
		activeByPair[key] = doc
	}
	return activeByPair
}

func filterPendingTargets(
	targets []StartSessionTarget,
	activeByPair map[SessionKey]Session,
) []StartSessionTarget {
	pending := make([]StartSessionTarget, 0, len(targets))
	for _, target := range targets {
		if _, active := activeByPair[target.Key()]; active {
			continue
		}
		pending = append(pending, target)
	}
	return pending
}

func (s *service) fetchBulkStartingCompRanks(
	ctx context.Context,
	pendingTargets []StartSessionTarget,
	party authorizedParty,
) ([]compRankLookup, int) {
	lookups := make([]compRankLookup, len(pendingTargets))
	if s.compRanks == nil {
		clog.WithGroupField(ctx, "fireteamService.startSessions", "warning", "competitive rank reader not configured")
		return lookups, 0
	}

	compErrors := make([]error, len(pendingTargets))
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(bulkCompRankConcurrency)

	for idx, target := range pendingTargets {
		member, hasMember := party.members[target.UserID]
		g.Go(func() error {
			if !hasMember {
				compErrors[idx] = errors.New("authorized member missing")
				return nil
			}
			snap, ok, fetchErr := s.fetchStartingCompRank(gCtx, member, target.CharacterID)
			if fetchErr != nil {
				compErrors[idx] = fetchErr
				return nil
			}
			lookups[idx] = compRankLookup{snapshot: snap, ok: ok}
			return nil
		})
	}
	_ = g.Wait()

	failedCount := 0
	for _, fetchErr := range compErrors {
		if fetchErr != nil {
			failedCount++
		}
	}
	if failedCount > 0 {
		clog.WithGroupFields(ctx, "fireteamService.startSessions", clog.Fields{
			"warning":             "failed to fetch starting competitive rank for one or more members",
			"compRankFailedCount": failedCount,
		})
	}
	return lookups, failedCount
}

func buildPendingSessions(
	pendingTargets []StartSessionTarget,
	startedBy AuditField,
	now time.Time,
	trackComp bool,
	compRanks []compRankLookup,
) []Session {
	newSessions := make([]Session, 0, len(pendingTargets))
	for idx, target := range pendingTargets {
		ses := newPendingSession(target.UserID, target.CharacterID, startedBy, now)
		if trackComp {
			var startSnap *CompRankSnapshot
			if idx < len(compRanks) && compRanks[idx].ok {
				snap := compRanks[idx].snapshot
				startSnap = &snap
			}
			ses = ses.WithCompTracking(startSnap)
		}
		newSessions = append(newSessions, ses)
	}
	return newSessions
}

func buildStartSessionResults(
	targets []StartSessionTarget,
	activeByPair map[SessionKey]Session,
	writeResults []CreateSessionResult,
) ([]StartSessionResult, int, int) {
	results := make([]StartSessionResult, 0, len(targets))
	writeIdx := 0
	startedCount := 0
	skippedCount := 0
	for _, target := range targets {
		if existing, active := activeByPair[target.Key()]; active {
			results = append(results, StartSessionResult{
				UserID:      target.UserID,
				CharacterID: target.CharacterID,
				Outcome:     StartSessionOutcomeSkippedActive,
				Session:     existing,
			})
			skippedCount++
			continue
		}
		wr := writeResults[writeIdx]
		writeIdx++
		outcome := StartSessionOutcomeSkippedActive
		if wr.Created {
			outcome = StartSessionOutcomeStarted
			startedCount++
		} else {
			skippedCount++
		}
		results = append(results, StartSessionResult{
			UserID:      target.UserID,
			CharacterID: target.CharacterID,
			Outcome:     outcome,
			Session:     wr.Session,
		})
	}
	return results, startedCount, skippedCount
}

// authorizeMembers verifies that every non-self target user is present in the
// caller's current Bungie party using at most 1 Bungie party call and 1 batched
// user lookup. When all targets are the caller themselves, 0 Bungie calls are made.
func (s *service) authorizeMembers(ctx context.Context, actorUserID string, targetUserIDs []string) (authorizedParty, error) {
	actorID := strings.TrimSpace(actorUserID)
	clog.WithGroupFields(ctx, "fireteamService.authorizeMembers", clog.Fields{
		"actorUserId": actorID,
		"targetCount": len(targetUserIDs),
	})
	if actorID == "" {
		return authorizedParty{}, ErrUserNotFound
	}

	caller, err := s.users.GetPlayer(ctx, actorID)
	if err != nil {
		return authorizedParty{}, fmt.Errorf("failed to fetch caller: %w", err)
	}

	nonSelfTargets := collectNonSelfTargetIDs(actorID, caller.ID, targetUserIDs)
	members := make(map[string]Player, 1+len(nonSelfTargets))
	members[caller.ID] = caller
	if actorID != caller.ID {
		members[actorID] = caller
	}

	if len(nonSelfTargets) == 0 {
		clog.WithGroupFields(ctx, "fireteamService.authorizeMembers", clog.Fields{
			"nonSelfCount":      0,
			"partyCheckSkipped": true,
		})
		return authorizedParty{caller: caller, members: members}, nil
	}

	pmID, membershipType, err := caller.PrimaryMembership()
	if err != nil {
		return authorizedParty{}, err
	}

	rawPartyIDs, err := fetchPartyMembershipIDs(ctx, s.party, s.bungiePartyTimeout(), pmID, membershipType)
	if err != nil {
		return authorizedParty{}, err
	}

	partyIDSet, lookupIDs := parsePartyMembershipIDs(rawPartyIDs, caller)
	if partyIDSet.Size() == 0 {
		clog.WithGroupFields(ctx, "fireteamService.authorizeMembers", clog.Fields{
			"nonSelfCount":      len(nonSelfTargets),
			"partySize":         0,
			"partyCheckSkipped": false,
		})
		return authorizedParty{}, ErrNotInFireteam
	}

	if len(lookupIDs) > 0 {
		matchedPlayers, err := s.users.FindPlayersByPartyMembershipIDs(ctx, lookupIDs)
		if err != nil {
			return authorizedParty{}, fmt.Errorf("failed to fetch fireteam users: %w", err)
		}
		for _, p := range matchedPlayers {
			if p.ID != "" && p.MatchesAnyMembership(partyIDSet) {
				members[p.ID] = p
			}
		}
	}

	for _, targetID := range nonSelfTargets {
		if _, ok := members[targetID]; !ok {
			clog.WithGroupFields(ctx, "fireteamService.authorizeMembers", clog.Fields{
				"nonSelfCount":             len(nonSelfTargets),
				"partySize":                partyIDSet.Size(),
				"partyCheckSkipped":        false,
				"unauthorizedTargetUserId": targetID,
			})
			return authorizedParty{}, fmt.Errorf("%w: user %s", ErrNotInFireteam, targetID)
		}
	}

	clog.WithGroupFields(ctx, "fireteamService.authorizeMembers", clog.Fields{
		"nonSelfCount":      len(nonSelfTargets),
		"partySize":         partyIDSet.Size(),
		"partyCheckSkipped": false,
	})
	return authorizedParty{caller: caller, members: members}, nil
}

func collectNonSelfTargetIDs(actorID, callerID string, targetUserIDs []string) []string {
	nonSelf := set.NewOrdered[string]()
	for _, rawTarget := range targetUserIDs {
		targetID := strings.TrimSpace(rawTarget)
		if targetID == "" || targetID == actorID || targetID == callerID {
			continue
		}
		nonSelf.Add(targetID)
	}
	return nonSelf.ToSlice()
}

func parsePartyMembershipIDs(rawPartyIDs []string, caller Player) (*set.Set[string], []string) {
	callerIndex := indexPlayersByMembership(nil, caller)
	partyIDSet := set.New[string]()
	lookupIDs := make([]string, 0, len(rawPartyIDs))
	for _, rawID := range rawPartyIDs {
		mID := strings.TrimSpace(rawID)
		if mID == "" {
			continue
		}
		partyIDSet.Add(mID)
		if _, isCaller := callerIndex[mID]; !isCaller {
			lookupIDs = append(lookupIDs, mID)
		}
	}
	return partyIDSet, utils.UniqueTrimmedStrings(lookupIDs)
}

func (s *service) fetchStartingCompRank(ctx context.Context, member Player, characterID string) (CompRankSnapshot, bool, error) {
	if s.compRanks == nil {
		return CompRankSnapshot{}, false, errors.New("competitive rank reader not configured")
	}
	pmID, membershipType, err := member.PrimaryMembership()
	if err != nil {
		return CompRankSnapshot{}, false, fmt.Errorf("failed to resolve primary membership: %w", err)
	}
	rankCtx, cancel := context.WithTimeout(ctx, s.bungiePartyTimeout())
	defer cancel()
	snap, ok, err := s.compRanks.GetCompetitiveRank(rankCtx, pmID, membershipType, characterID)
	if err != nil {
		return CompRankSnapshot{}, false, fmt.Errorf("failed to fetch starting competitive rank: %w", err)
	}
	return snap, ok, nil
}

func fetchPartyMembershipIDs(
	ctx context.Context,
	party PartyReader,
	timeout time.Duration,
	primaryMembershipID int64,
	membershipType int64,
) ([]string, error) {
	partyCtx, cancel := context.WithTimeout(ctx, timeout)
	rawPartyIDs, err := party.GetPartyMembershipIDs(partyCtx, primaryMembershipID, membershipType)
	cancel()
	if err != nil {
		if errors.Is(err, ErrDestinyUnavailable) || (ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded)) {
			return nil, fmt.Errorf("%w: %w", ErrDestinyUnavailable, err)
		}
		return nil, fmt.Errorf("failed to fetch party members: %w", err)
	}
	return rawPartyIDs, nil
}
