package activity

import (
	"cmp"
	"context"
	"fmt"
	"oneTrick/set"
	"slices"
	"strconv"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

type Service interface {
	GetActivity(ctx context.Context, activityID string) (MatchReport, error)
}

type service struct {
	carnage    CarnageReportGateway
	manifest   ManifestLookup
	aggregates AggregateReader
	snapshots  SnapshotReader
	users      UserLookup
}

var _ Service = (*service)(nil)

func NewService(
	carnage CarnageReportGateway,
	manifest ManifestLookup,
	aggregates AggregateReader,
	snapshots SnapshotReader,
	users UserLookup,
) Service {
	return &service{
		carnage:    carnage,
		manifest:   manifest,
		aggregates: aggregates,
		snapshots:  snapshots,
		users:      users,
	}
}

func (s *service) GetActivity(ctx context.Context, activityID string) (MatchReport, error) {
	trimmedID := strings.TrimSpace(activityID)
	if trimmedID == "" {
		return MatchReport{}, fmt.Errorf("%w: activityId is required", ErrInvalidActivityID)
	}
	if _, err := strconv.ParseInt(trimmedID, 10, 64); err != nil {
		return MatchReport{}, fmt.Errorf("%w: %v", ErrInvalidActivityID, err)
	}

	clog.WithGroupField(ctx, "activityService.getActivity", "activityId", trimmedID)

	report, err := s.carnage.GetPostGameCarnageReport(ctx, trimmedID)
	if err != nil {
		return MatchReport{}, err
	}

	var (
		agg    Aggregate
		hasAgg bool
	)
	if s.aggregates != nil {
		agg, hasAgg, err = s.aggregates.GetByActivityID(ctx, trimmedID)
		if err != nil {
			return MatchReport{}, fmt.Errorf("load aggregate %s: %w", trimmedID, err)
		}
	}

	activityInfo := s.resolveActivityInfo(ctx, trimmedID, report, agg, hasAgg)
	itemDefs := s.fetchItemDefinitions(ctx, report)

	snapshotsByChar, missingSnapshotCount, err := s.loadSnapshots(ctx, agg, hasAgg)
	if err != nil {
		return MatchReport{}, fmt.Errorf("load snapshots for activity %s: %w", trimmedID, err)
	}

	userIdx, err := s.resolveUsers(ctx, report, agg, hasAgg, snapshotsByChar)
	if err != nil {
		return MatchReport{}, fmt.Errorf("resolve users for activity %s: %w", trimmedID, err)
	}

	participants, usersByChar, metrics := buildParticipants(
		report,
		itemDefs,
		agg,
		hasAgg,
		snapshotsByChar,
		userIdx,
	)

	participants = sortedParticipants(participants, report.Teams)

	teams := report.Teams
	if teams == nil {
		teams = make([]Team, 0)
	}

	var aggPtr *Aggregate
	if hasAgg {
		aggCopy := agg
		aggPtr = &aggCopy
	}

	logFields := clog.Fields{
		"hasAggregate":         hasAgg,
		"teamCount":            len(teams),
		"participantCount":     len(participants),
		"trackedSnapshotCount": metrics.trackedSnapshotCount,
		"trackedSessionCount":  metrics.trackedSessionCount,
		"registeredUserCount":  metrics.registeredUserCount,
	}
	if missingSnapshotCount > 0 {
		logFields["missingSnapshotCount"] = missingSnapshotCount
	}
	clog.WithGroupFields(ctx, "activityService.getActivity", logFields)

	return MatchReport{
		Activity:        activityInfo,
		Teams:           teams,
		Participants:    participants,
		Aggregate:       aggPtr,
		Snapshots:       snapshotsByChar,
		Users:           usersByChar,
		PostGameEntries: report.RawEntries,
	}, nil
}

func (s *service) resolveActivityInfo(
	ctx context.Context,
	activityID string,
	report CarnageReport,
	agg Aggregate,
	hasAgg bool,
) ActivityInfo {
	if hasAgg {
		return agg.ActivityDetails
	}
	if s.manifest == nil {
		return report.ToActivityInfo(activityID, ActivityManifest{}, ActivityManifest{}, "")
	}

	details := report.ActivityDetails
	var refDef, dirDef ActivityManifest
	if hashes := details.ManifestActivityHashes(); len(hashes) > 0 {
		defs, err := s.manifest.GetActivitiesByIDs(ctx, hashes)
		if err != nil {
			clog.WithGroupFields(ctx, "activityService.getActivity", clog.Fields{
				"manifestActivityWarning": err.Error(),
			})
		} else {
			refDef = defs[details.ReferenceID]
			dirDef = defs[details.DirectorActivityHash]
		}
	}

	var activityIcon string
	modeHash := dirDef.DirectActivityModeHash
	if modeHash == 0 {
		modeHash = refDef.DirectActivityModeHash
	}
	if modeHash != 0 || details.Mode != 0 {
		modeDef, ok, err := s.manifest.GetActivityMode(ctx, modeHash, details.Mode)
		if err != nil {
			clog.WithGroupFields(ctx, "activityService.getActivity", clog.Fields{
				"manifestModeWarning": err.Error(),
			})
		} else if ok {
			activityIcon = modeDef.IconURL
		}
	}

	return report.ToActivityInfo(activityID, refDef, dirDef, activityIcon)
}

func (s *service) fetchItemDefinitions(ctx context.Context, report CarnageReport) map[int64]ItemManifest {
	if s.manifest == nil {
		return make(map[int64]ItemManifest)
	}

	hashes := report.ManifestItemIDs()
	if len(hashes) == 0 {
		return make(map[int64]ItemManifest)
	}

	defs, err := s.manifest.GetItemsByIDs(ctx, hashes)
	if err != nil {
		clog.WithGroupFields(ctx, "activityService.getActivity", clog.Fields{
			"manifestItemsWarning": err.Error(),
		})
		return make(map[int64]ItemManifest)
	}
	if defs == nil {
		return make(map[int64]ItemManifest)
	}
	return defs
}

func (s *service) loadSnapshots(
	ctx context.Context,
	agg Aggregate,
	hasAgg bool,
) (map[string]CharacterSnapshot, int, error) {
	snapshotsByChar := make(map[string]CharacterSnapshot)
	if !hasAgg || s.snapshots == nil {
		return snapshotsByChar, 0, nil
	}

	snapshotIDs := agg.UniqueSnapshotIDs()
	if len(snapshotIDs) == 0 {
		return snapshotsByChar, 0, nil
	}

	snaps, err := s.snapshots.GetByIDs(ctx, snapshotIDs)
	if err != nil {
		return nil, 0, err
	}

	snapsByID := make(map[string]CharacterSnapshot, len(snaps))
	for _, snap := range snaps {
		snapsByID[snap.ID] = snap
	}

	missingCount := 0
	for charID, link := range agg.SnapshotLinks {
		snapID := strings.TrimSpace(link.SnapshotID)
		if snapID == "" {
			continue
		}
		snap, ok := snapsByID[snapID]
		if !ok {
			missingCount++
			continue
		}
		key := charID
		if key == "" {
			key = snap.CharacterID
		}
		snapshotsByChar[key] = snap
	}

	return snapshotsByChar, missingCount, nil
}

type userIndex struct {
	byID           map[string]User
	byMembershipID map[string]User
	byCharacterID  map[string]User
}

func newUserIndex(capacity int) userIndex {
	return userIndex{
		byID:           make(map[string]User, capacity),
		byMembershipID: make(map[string]User, capacity*2),
		byCharacterID:  make(map[string]User, capacity*3),
	}
}

func (idx *userIndex) add(users []User) {
	for _, u := range users {
		if id := strings.TrimSpace(u.ID); id != "" {
			idx.byID[id] = u
		}
		if id := strings.TrimSpace(u.PrimaryMembershipID); id != "" {
			idx.byMembershipID[id] = u
		}
		if id := strings.TrimSpace(u.MemberID); id != "" {
			idx.byMembershipID[id] = u
		}
		for _, m := range u.Memberships {
			if id := strings.TrimSpace(m.ID); id != "" {
				idx.byMembershipID[id] = u
			}
		}
		for _, cID := range u.CharacterIDs {
			if id := strings.TrimSpace(cID); id != "" {
				idx.byCharacterID[id] = u
			}
		}
	}
}

func (idx userIndex) hasID(userID string) bool {
	_, ok := idx.byID[userID]
	return ok
}

func (idx userIndex) resolveForParticipant(
	charID string,
	entry CarnageEntry,
	link SnapshotLink,
	hasLink bool,
	snap CharacterSnapshot,
	hasSnap bool,
) (User, bool, string) {
	if hasSnap {
		if uid := strings.TrimSpace(snap.UserID); uid != "" {
			if u, ok := idx.byID[uid]; ok {
				return u, true, u.ID
			}
			return User{}, false, uid
		}
	}

	if hasLink {
		if uid := strings.TrimSpace(link.UserID); uid != "" {
			if u, ok := idx.byID[uid]; ok {
				return u, true, u.ID
			}
			return User{}, false, uid
		}
	}

	if id := strings.TrimSpace(entry.MembershipID); id != "" {
		if u, ok := idx.byMembershipID[id]; ok {
			return u, true, u.ID
		}
	}
	if id := strings.TrimSpace(entry.BungieNetMembershipID); id != "" {
		if u, ok := idx.byMembershipID[id]; ok {
			return u, true, u.ID
		}
	}
	if u, ok := idx.byCharacterID[charID]; ok {
		return u, true, u.ID
	}
	return User{}, false, ""
}

func (s *service) resolveUsers(
	ctx context.Context,
	report CarnageReport,
	agg Aggregate,
	hasAgg bool,
	snapshotsByChar map[string]CharacterSnapshot,
) (userIndex, error) {
	if s.users == nil {
		return newUserIndex(0), nil
	}

	var matchedUsers []User
	if membershipIDs := report.MembershipLookupIDs(); len(membershipIDs) > 0 {
		var err error
		matchedUsers, err = s.users.FindByPartyMembershipIDs(ctx, membershipIDs)
		if err != nil {
			return newUserIndex(0), err
		}
	}

	idx := newUserIndex(len(matchedUsers))
	idx.add(matchedUsers)

	missingUserIDs := set.NewOrdered[string]()
	for _, snap := range snapshotsByChar {
		if uID := strings.TrimSpace(snap.UserID); uID != "" && !idx.hasID(uID) {
			missingUserIDs.Add(uID)
		}
	}
	if hasAgg {
		for _, link := range agg.SnapshotLinks {
			if uID := strings.TrimSpace(link.UserID); uID != "" && !idx.hasID(uID) {
				missingUserIDs.Add(uID)
			}
		}
	}

	if missingUserIDs.Size() > 0 {
		extraUsers, err := s.users.GetByIDs(ctx, missingUserIDs.ToSlice())
		if err != nil {
			return newUserIndex(0), err
		}
		idx.add(extraUsers)
	}

	return idx, nil
}

type participantMetrics struct {
	trackedSnapshotCount int
	trackedSessionCount  int
	registeredUserCount  int
}

func buildParticipants(
	report CarnageReport,
	itemDefs map[int64]ItemManifest,
	agg Aggregate,
	hasAgg bool,
	snapshotsByChar map[string]CharacterSnapshot,
	userIdx userIndex,
) ([]Participant, map[string]User, participantMetrics) {
	hasTeams := report.HasTeams()
	participants := make([]Participant, 0, len(report.Entries))
	usersByChar := make(map[string]User)
	var metrics participantMetrics

	for _, entry := range report.Entries {
		charID := strings.TrimSpace(entry.CharacterID)
		if charID == "" {
			continue
		}

		var (
			link       SnapshotLink
			hasLink    bool
			storedPerf *Performance
		)
		if hasAgg {
			link, hasLink = agg.LinkFor(charID)
			if p, ok := agg.PerformanceFor(charID); ok {
				storedPerf = &p
			}
		}

		snap, hasSnap := snapshotsByChar[charID]

		emblemIconURL := ""
		if entry.EmblemHash != 0 {
			if def, ok := itemDefs[entry.EmblemHash]; ok && def.IconURL != "" {
				emblemIconURL = def.IconURL
			}
		}
		if emblemIconURL == "" && hasLink && link.EmblemURL != "" {
			emblemIconURL = link.EmblemURL
		}

		charClass := entry.CharacterClass
		if charClass == "" && hasLink {
			charClass = link.CharacterClass
		}

		resolvedUser, hasUser, resolvedUserID := userIdx.resolveForParticipant(charID, entry, link, hasLink, snap, hasSnap)
		if hasUser {
			usersByChar[charID] = resolvedUser
		}

		var (
			sessionID       string
			snapshotID      string
			confidenceLevel ConfidenceLevel
		)
		if hasSnap {
			snapshotID = snap.ID
		}
		if hasLink {
			sessionID = strings.TrimSpace(link.SessionID)
			confidenceLevel = link.ActiveConfidenceLevel(snapshotID)
		}

		if snapshotID != "" {
			metrics.trackedSnapshotCount++
		}
		if sessionID != "" {
			metrics.trackedSessionCount++
		}
		if resolvedUserID != "" {
			metrics.registeredUserCount++
		}

		participants = append(participants, Participant{
			CharacterID:     charID,
			MembershipID:    entry.MembershipID,
			MembershipType:  entry.MembershipType,
			DisplayName:     entry.DisplayName,
			DisplayNameCode: entry.DisplayNameCode,
			CharacterClass:  charClass,
			LightLevel:      entry.LightLevel,
			EmblemIconURL:   emblemIconURL,
			TeamID:          entry.TeamID(hasTeams),
			Standing:        entry.ResolvedStanding(),
			Score:           entry.Score,
			Completed:       entry.Completed,
			FireteamID:      entry.FireteamID(),
			Performance:     entry.BuildPerformance(itemDefs, storedPerf),
			UserID:          resolvedUserID,
			SessionID:       sessionID,
			SnapshotID:      snapshotID,
			ConfidenceLevel: confidenceLevel,
		})
	}

	// Preserve compatibility for any matched snapshot or aggregate link whose character wasn't in PGCR entries.
	for charID, snap := range snapshotsByChar {
		if _, exists := usersByChar[charID]; exists {
			continue
		}
		if u, ok := userIdx.byID[strings.TrimSpace(snap.UserID)]; ok {
			usersByChar[charID] = u
		}
	}
	if hasAgg {
		for charID, link := range agg.SnapshotLinks {
			if _, exists := usersByChar[charID]; exists {
				continue
			}
			if u, ok := userIdx.byID[strings.TrimSpace(link.UserID)]; ok {
				usersByChar[charID] = u
			} else if u, ok := userIdx.byCharacterID[charID]; ok {
				usersByChar[charID] = u
			}
		}
	}

	return participants, usersByChar, metrics
}

type teamSortKey struct {
	standingRank       int
	minParticipantRank int32
	hasParticipantRank bool
	originalTeamIndex  int
	isUnknownTeam      bool
}

func compareTeamSortKey(a, b teamSortKey) int {
	if a.isUnknownTeam != b.isUnknownTeam {
		if !a.isUnknownTeam {
			return -1
		}
		return 1
	}
	if c := cmp.Compare(a.standingRank, b.standingRank); c != 0 {
		return c
	}
	if a.hasParticipantRank && b.hasParticipantRank {
		if c := cmp.Compare(a.minParticipantRank, b.minParticipantRank); c != 0 {
			return c
		}
	}
	return cmp.Compare(a.originalTeamIndex, b.originalTeamIndex)
}

func sortedParticipants(participants []Participant, teams []Team) []Participant {
	out := slices.Clone(participants)
	if len(out) <= 1 {
		return out
	}

	if len(teams) > 0 {
		teamKeys := make(map[string]teamSortKey, len(teams))
		for i, t := range teams {
			teamKeys[t.ID] = teamSortKey{
				standingRank:      t.StandingRank(),
				originalTeamIndex: i,
			}
		}
		for _, p := range out {
			if key, ok := teamKeys[p.TeamID]; ok {
				if !key.hasParticipantRank || p.Standing < key.minParticipantRank {
					key.minParticipantRank = p.Standing
					key.hasParticipantRank = true
					teamKeys[p.TeamID] = key
				}
			}
		}
		unknownKey := teamSortKey{
			standingRank:      3,
			originalTeamIndex: len(teams),
			isUnknownTeam:     true,
		}
		keyOf := func(teamID string) teamSortKey {
			if k, ok := teamKeys[teamID]; ok {
				return k
			}
			return unknownKey
		}

		slices.SortFunc(out, func(a, b Participant) int {
			if c := compareTeamSortKey(keyOf(a.TeamID), keyOf(b.TeamID)); c != 0 {
				return c
			}
			return a.ComparePerformanceDesc(b)
		})
		return out
	}

	slices.SortFunc(out, func(a, b Participant) int {
		if c := cmp.Compare(a.Standing, b.Standing); c != 0 {
			return c
		}
		return a.ComparePerformanceDesc(b)
	})
	return out
}
