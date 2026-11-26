package activity

import (
	"cmp"
	"context"
	"fmt"
	"oneTrick/core/gamemode"
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
	itemDefs := s.fetchItemDefinitions(ctx, report.Entries)

	snapshotsByChar, missingSnapshotCount, err := s.loadSnapshots(ctx, agg, hasAgg)
	if err != nil {
		return MatchReport{}, fmt.Errorf("load snapshots for activity %s: %w", trimmedID, err)
	}

	userIdx, err := s.resolveUsers(ctx, report.Entries, agg, hasAgg, snapshotsByChar)
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

	details := report.ActivityDetails
	instanceID := details.InstanceID
	if instanceID == "" {
		instanceID = activityID
	}

	info := ActivityInfo{
		ReferenceID:  details.ReferenceID,
		ActivityHash: details.DirectorActivityHash,
		InstanceID:   instanceID,
		Period:       report.Period,
		IsPrivate:    details.IsPrivate,
		HasIsPrivate: details.HasIsPrivate,
		Mode:         details.ModeName,
	}

	if s.manifest == nil {
		info.EstimatedGameMode = gamemode.EstimateName(info.Mode, info.Activity)
		return info
	}

	hashSet := set.NewOrdered[int64]()
	if details.ReferenceID != 0 {
		hashSet.Add(details.ReferenceID)
	}
	if details.DirectorActivityHash != 0 {
		hashSet.Add(details.DirectorActivityHash)
	}

	var refDef, dirDef ActivityManifest
	if hashSet.Size() > 0 {
		defs, err := s.manifest.GetActivitiesByIDs(ctx, hashSet.ToSlice())
		if err != nil {
			clog.WithGroupFields(ctx, "activityService.getActivity", clog.Fields{
				"manifestActivityWarning": err.Error(),
			})
		} else {
			refDef = defs[details.ReferenceID]
			dirDef = defs[details.DirectorActivityHash]
		}
	}

	info.Location = refDef.Name
	info.Description = refDef.Description
	info.ImageURL = refDef.PgcrImageURL
	info.Activity = dirDef.Name
	if info.Activity == "" {
		info.Activity = refDef.Name
	}

	if dirDef.Name == "Iron Banner: Tribute" || strings.Contains(strings.ToLower(dirDef.Name), "tribute") {
		info.Mode = "Iron Banner Tribute"
	}
	info.EstimatedGameMode = gamemode.EstimateName(info.Mode, info.Activity)

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
			info.ActivityIcon = modeDef.IconURL
		}
	}

	return info
}

func (s *service) fetchItemDefinitions(ctx context.Context, entries []CarnageEntry) map[int64]ItemManifest {
	if s.manifest == nil || len(entries) == 0 {
		return make(map[int64]ItemManifest)
	}

	hashes := set.NewOrdered[int64]()
	for _, entry := range entries {
		if entry.EmblemHash != 0 {
			hashes.Add(entry.EmblemHash)
		}
		for _, w := range entry.Weapons {
			if w.ReferenceID != 0 {
				hashes.Add(w.ReferenceID)
			}
		}
	}
	if hashes.Size() == 0 {
		return make(map[int64]ItemManifest)
	}

	defs, err := s.manifest.GetItemsByIDs(ctx, hashes.ToSlice())
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
	if !hasAgg || len(agg.SnapshotLinks) == 0 || s.snapshots == nil {
		return snapshotsByChar, 0, nil
	}

	snapshotIDs := set.NewOrdered[string]()
	for _, link := range agg.SnapshotLinks {
		if id := strings.TrimSpace(link.SnapshotID); id != "" {
			snapshotIDs.Add(id)
		}
	}
	if snapshotIDs.Size() == 0 {
		return snapshotsByChar, 0, nil
	}

	snaps, err := s.snapshots.GetByIDs(ctx, snapshotIDs.ToSlice())
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

func buildUserIndex(userSlices ...[]User) userIndex {
	total := 0
	for _, s := range userSlices {
		total += len(s)
	}
	idx := userIndex{
		byID:           make(map[string]User, total),
		byMembershipID: make(map[string]User, total*2),
		byCharacterID:  make(map[string]User, total*3),
	}
	for _, users := range userSlices {
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
	return idx
}

func (s *service) resolveUsers(
	ctx context.Context,
	entries []CarnageEntry,
	agg Aggregate,
	hasAgg bool,
	snapshotsByChar map[string]CharacterSnapshot,
) (userIndex, error) {
	if s.users == nil {
		return buildUserIndex(), nil
	}

	membershipIDs := set.NewOrdered[string]()
	for _, entry := range entries {
		if id := strings.TrimSpace(entry.MembershipID); id != "" {
			membershipIDs.Add(id)
		}
		if id := strings.TrimSpace(entry.BungieNetMembershipID); id != "" {
			membershipIDs.Add(id)
		}
	}

	var matchedUsers []User
	if membershipIDs.Size() > 0 {
		var err error
		matchedUsers, err = s.users.FindByPartyMembershipIDs(ctx, membershipIDs.ToSlice())
		if err != nil {
			return buildUserIndex(), err
		}
	}

	foundIDs := set.New[string]()
	for _, u := range matchedUsers {
		if id := strings.TrimSpace(u.ID); id != "" {
			foundIDs.Add(id)
		}
	}

	missingUserIDs := set.NewOrdered[string]()
	for _, snap := range snapshotsByChar {
		if uID := strings.TrimSpace(snap.UserID); uID != "" && !foundIDs.Contains(uID) {
			missingUserIDs.Add(uID)
		}
	}
	if hasAgg {
		for _, link := range agg.SnapshotLinks {
			if uID := strings.TrimSpace(link.UserID); uID != "" && !foundIDs.Contains(uID) {
				missingUserIDs.Add(uID)
			}
		}
	}

	var extraUsers []User
	if missingUserIDs.Size() > 0 {
		var err error
		extraUsers, err = s.users.GetByIDs(ctx, missingUserIDs.ToSlice())
		if err != nil {
			return buildUserIndex(), err
		}
	}

	return buildUserIndex(matchedUsers, extraUsers), nil
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
	hasTeams := len(report.Teams) > 0
	participants := make([]Participant, 0, len(report.Entries))
	usersByChar := make(map[string]User)
	var metrics participantMetrics

	for _, entry := range report.Entries {
		charID := strings.TrimSpace(entry.CharacterID)
		if charID == "" {
			continue
		}

		var (
			link    SnapshotLink
			hasLink bool
		)
		if hasAgg && agg.SnapshotLinks != nil {
			link, hasLink = agg.SnapshotLinks[charID]
		}

		snap, hasSnap := snapshotsByChar[charID]

		var storedPerf *Performance
		if hasAgg && agg.Performance != nil {
			if p, ok := agg.Performance[charID]; ok {
				storedPerf = &p
			}
		}

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

		standing := entry.Standing
		if !entry.HasStanding && entry.PlayerStats.Standing.Present {
			standing = int32(entry.PlayerStats.Standing.Value)
		}

		teamID := extractTeamID(entry.PlayerStats.Team, hasTeams)
		fireteamID := extractFireteamID(entry.PlayerStats.FireteamID)

		resolvedUser, resolvedUserID := resolveParticipantUser(charID, entry, link, hasLink, snap, hasSnap, userIdx)
		if resolvedUser != nil {
			usersByChar[charID] = *resolvedUser
		}

		var (
			sessionID       string
			snapshotID      string
			confidenceLevel ConfidenceLevel
		)
		if hasLink {
			sessionID = strings.TrimSpace(link.SessionID)
			confidenceLevel = link.ConfidenceLevel
		}
		if hasSnap {
			snapshotID = snap.ID
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

		perf := buildParticipantPerformance(entry, itemDefs, storedPerf)

		participants = append(participants, Participant{
			CharacterID:     charID,
			MembershipID:    entry.MembershipID,
			MembershipType:  entry.MembershipType,
			DisplayName:     entry.DisplayName,
			DisplayNameCode: entry.DisplayNameCode,
			CharacterClass:  charClass,
			LightLevel:      entry.LightLevel,
			EmblemIconURL:   emblemIconURL,
			TeamID:          teamID,
			Standing:        standing,
			Score:           entry.Score,
			Completed:       entry.Completed,
			FireteamID:      fireteamID,
			Performance:     perf,
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

func resolveParticipantUser(
	charID string,
	entry CarnageEntry,
	link SnapshotLink,
	hasLink bool,
	snap CharacterSnapshot,
	hasSnap bool,
	idx userIndex,
) (*User, string) {
	if hasSnap && strings.TrimSpace(snap.UserID) != "" {
		uid := strings.TrimSpace(snap.UserID)
		if u, ok := idx.byID[uid]; ok {
			uCopy := u
			return &uCopy, u.ID
		}
		return nil, uid
	}

	if hasLink && strings.TrimSpace(link.UserID) != "" {
		uid := strings.TrimSpace(link.UserID)
		if u, ok := idx.byID[uid]; ok {
			uCopy := u
			return &uCopy, u.ID
		}
		return nil, uid
	}

	if id := strings.TrimSpace(entry.MembershipID); id != "" {
		if u, ok := idx.byMembershipID[id]; ok {
			uCopy := u
			return &uCopy, u.ID
		}
	}
	if id := strings.TrimSpace(entry.BungieNetMembershipID); id != "" {
		if u, ok := idx.byMembershipID[id]; ok {
			uCopy := u
			return &uCopy, u.ID
		}
	}
	if u, ok := idx.byCharacterID[charID]; ok {
		uCopy := u
		return &uCopy, u.ID
	}
	return nil, ""
}

func buildParticipantPerformance(
	entry CarnageEntry,
	itemDefs map[int64]ItemManifest,
	storedPerf *Performance,
) Performance {
	weapons := make(map[string]WeaponMetrics, len(entry.Weapons))

	for _, w := range entry.Weapons {
		if w.ReferenceID == 0 {
			continue
		}
		key := strconv.FormatInt(w.ReferenceID, 10)
		wm := WeaponMetrics{
			ReferenceID: w.ReferenceID,
			Stats:       w.Stats,
		}

		if def, ok := itemDefs[w.ReferenceID]; ok {
			wm.HasDisplay = true
			wm.Display = WeaponDisplay{
				Name:        def.Name,
				Description: def.ItemTypeAndTierDisplayName,
				Icon:        def.IconURL,
				HasIcon:     def.HasIcon,
			}
			wm.BucketHash = def.BucketHash
			wm.ItemTypeDisplayName = def.ItemTypeDisplayName
		}

		if storedPerf != nil {
			if storedWeapon, ok := storedPerf.Weapons[key]; ok && storedWeapon.Properties != nil {
				wm.Properties = storedWeapon.Properties
				if wm.BucketHash == 0 && storedWeapon.Properties.BaseInfo.BucketHash != 0 {
					wm.BucketHash = storedWeapon.Properties.BaseInfo.BucketHash
				}
				if wm.ItemTypeDisplayName == "" && storedWeapon.Properties.BaseInfo.ItemTypeDisplayName != "" {
					wm.ItemTypeDisplayName = storedWeapon.Properties.BaseInfo.ItemTypeDisplayName
				}
			}
		}

		weapons[key] = wm
	}

	return Performance{
		PlayerStats: entry.PlayerStats,
		Weapons:     weapons,
		Extra:       entry.ExtraStats,
	}
}

func extractTeamID(teamStat StatValuePair, hasTeams bool) string {
	if !hasTeams || !teamStat.Present || teamStat.Value < 0 {
		return ""
	}
	return strconv.FormatInt(int64(teamStat.Value), 10)
}

func extractFireteamID(ftStat StatValuePair) string {
	if !ftStat.Present {
		return ""
	}
	// Prefer DisplayValue to avoid float64 precision loss on 64-bit Bungie fireteam IDs.
	trimmed := strings.TrimSpace(ftStat.DisplayValue)
	if trimmed != "" && trimmed != "0" {
		return trimmed
	}
	if ftStat.Value != 0 {
		return strconv.FormatInt(int64(ftStat.Value), 10)
	}
	return ""
}

func sortedParticipants(participants []Participant, teams []Team) []Participant {
	out := slices.Clone(participants)
	if len(out) <= 1 {
		return out
	}

	if len(teams) > 0 {
		teamOrder := make(map[string]int, len(teams))
		for i, t := range teams {
			teamOrder[t.ID] = i
		}
		unknownOrder := len(teams)
		orderOf := func(teamID string) int {
			if ord, ok := teamOrder[teamID]; ok {
				return ord
			}
			return unknownOrder
		}

		slices.SortFunc(out, func(a, b Participant) int {
			if c := cmp.Compare(orderOf(a.TeamID), orderOf(b.TeamID)); c != 0 {
				return c
			}
			return compareParticipantPerformanceDesc(a, b)
		})
		return out
	}

	slices.SortFunc(out, func(a, b Participant) int {
		if c := cmp.Compare(a.Standing, b.Standing); c != 0 {
			return c
		}
		return compareParticipantPerformanceDesc(a, b)
	})
	return out
}

func compareParticipantPerformanceDesc(a, b Participant) int {
	if c := cmp.Compare(b.Score.Value, a.Score.Value); c != 0 {
		return c
	}
	if c := cmp.Compare(b.Performance.PlayerStats.Kills.Value, a.Performance.PlayerStats.Kills.Value); c != 0 {
		return c
	}
	if c := cmp.Compare(b.Performance.PlayerStats.KDA.Value, a.Performance.PlayerStats.KDA.Value); c != 0 {
		return c
	}
	return cmp.Compare(a.CharacterID, b.CharacterID)
}
