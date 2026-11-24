package stats

import (
	"cmp"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/set"
	"slices"
	"strconv"
	"strings"
)

type SimilarTierFilter string

const (
	SimilarTierAll  SimilarTierFilter = "all"
	SimilarTierHigh SimilarTierFilter = "high"
)

type SimilarSnapshotsParams struct {
	SnapshotID   string
	IncludeStats bool
	Tier         SimilarTierFilter
	GameMode     string
}

const (
	exoticTierType   int32 = 6
	legendaryTierTyp int32 = 5
	powerStatHash          = "1935470627"
)

var armorBucketKeys = []string{
	strconv.FormatUint(uint64(destiny.HelmetArmor), 10),
	strconv.FormatUint(uint64(destiny.GauntletsArmor), 10),
	strconv.FormatUint(uint64(destiny.ChestArmor), 10),
	strconv.FormatUint(uint64(destiny.LegArmor), 10),
	strconv.FormatUint(uint64(destiny.ClassArmor), 10),
}

var classArmorBucketKey = strconv.FormatUint(uint64(destiny.ClassArmor), 10)
var powerBucketKey = strconv.Itoa(destiny.Power)

// ComputeSimilarCounts groups all active (non-archived) snapshots by their BaseIdentityKey
// ("{kineticInstanceId}:{energyInstanceId}:{subclassItemHash}") and returns:
//   - similarCounts: map of snapshot ID -> count of OTHER active mergeable snapshots on the character (len(group)-1)
//   - peerGroups: map of snapshot ID -> slice of all active snapshot IDs in the same similarity group (including itself)
func ComputeSimilarCounts(snapshots []api.CharacterSnapshot) (map[string]int, map[string][]string) {
	similarCounts := make(map[string]int)
	peerGroups := make(map[string][]string)

	byKey := make(map[string][]string)
	seen := make(map[string]struct{}, len(snapshots))

	for i := range snapshots {
		snap := &snapshots[i]
		if snap.ID == "" || snapshot.IsArchived(snap) {
			continue
		}
		if _, dup := seen[snap.ID]; dup {
			continue
		}
		seen[snap.ID] = struct{}{}

		key, ok := snapshot.BaseIdentityKey(snap)
		if !ok {
			continue
		}
		byKey[key] = append(byKey[key], snap.ID)
	}

	for _, ids := range byKey {
		if len(ids) < 2 {
			continue
		}
		peerCount := len(ids) - 1
		for _, id := range ids {
			similarCounts[id] = peerCount
			peerGroups[id] = ids
		}
	}

	return similarCounts, peerGroups
}

// CompareSimilarLoadout classifies a candidate snapshot against a target snapshot ('high' vs 'base')
// and computes Exotic armor match, Power weapon match, and non-zero ClassStat deltas (candidate - target).
// Individual Legendary armor buckets are intentionally ignored.
func CompareSimilarLoadout(target, candidate *api.CharacterSnapshot, tierLookup func(int64) int32) api.SimilarSnapshotItem {
	var targetLoadout, candLoadout api.Loadout
	if target != nil {
		targetLoadout = target.Loadout
	}
	if candidate != nil {
		candLoadout = candidate.Loadout
	}

	exoticMatch := exoticArmorMatches(targetLoadout, candLoadout, tierLookup)
	powerMatch := powerWeaponMatches(targetLoadout, candLoadout)
	armorSetBonusMatch := armorSetBonusesMatch(target, candidate)
	statDiffs := computeStatDiffs(target, candidate)

	tier := api.SimilarityTierBase
	if exoticMatch {
		tier = api.SimilarityTierHigh
	}

	var candVal api.CharacterSnapshot
	if candidate != nil {
		candVal = *candidate
	}

	return api.SimilarSnapshotItem{
		Snapshot:           candVal,
		Tier:               tier,
		ExoticArmorMatch:   exoticMatch,
		PowerWeaponMatch:   powerMatch,
		ArmorSetBonusMatch: armorSetBonusMatch,
		StatDiffs:          statDiffs,
	}
}

func resolveInlineItemTierType(item api.ItemSnapshot) int32 {
	if item.ItemProperties.BaseInfo.TierType != 0 {
		return item.ItemProperties.BaseInfo.TierType
	}
	tierName := strings.TrimSpace(item.ItemProperties.BaseInfo.TierTypeName)
	displayTypeAndTier := strings.ToLower(strings.TrimSpace(item.ItemProperties.BaseInfo.ItemTypeAndTierDisplayName))

	if strings.EqualFold(tierName, "Exotic") || strings.HasPrefix(displayTypeAndTier, "exotic ") {
		return exoticTierType
	}
	if strings.EqualFold(tierName, "Legendary") || strings.HasPrefix(displayTypeAndTier, "legendary ") {
		return legendaryTierTyp
	}
	return 0
}

func findInlineExoticArmor(loadout api.Loadout) (string, api.ItemSnapshot, bool) {
	for _, bucketKey := range armorBucketKeys {
		item, ok := loadout[bucketKey]
		if !ok {
			continue
		}
		if resolveInlineItemTierType(item) == exoticTierType {
			return bucketKey, item, true
		}
	}
	return "", api.ItemSnapshot{}, false
}

func collectMissingArmorTierHashes(target *api.CharacterSnapshot, allSnapshots []api.CharacterSnapshot, candidateIndices []int) []int64 {
	var missing *set.OrderedSet[int64]
	collectFromLoadout := func(loadout api.Loadout) {
		if loadout == nil {
			return
		}
		if _, _, ok := findInlineExoticArmor(loadout); ok {
			return
		}
		for _, bucketKey := range armorBucketKeys {
			item, ok := loadout[bucketKey]
			if !ok || item.ItemHash == 0 {
				continue
			}
			if resolveInlineItemTierType(item) == 0 {
				if missing == nil {
					missing = set.NewOrdered[int64]()
				}
				missing.Add(item.ItemHash)
			}
		}
	}

	if target != nil {
		collectFromLoadout(target.Loadout)
	}
	for _, idx := range candidateIndices {
		collectFromLoadout(allSnapshots[idx].Loadout)
	}
	if missing == nil || missing.Size() == 0 {
		return nil
	}
	return missing.ToSlice()
}

func findEquippedExoticArmor(loadout api.Loadout, tierLookup func(int64) int32) (string, api.ItemSnapshot, bool) {
	if loadout == nil {
		return "", api.ItemSnapshot{}, false
	}
	if bucketKey, item, ok := findInlineExoticArmor(loadout); ok {
		return bucketKey, item, true
	}
	if tierLookup == nil {
		return "", api.ItemSnapshot{}, false
	}
	for _, bucketKey := range armorBucketKeys {
		item, ok := loadout[bucketKey]
		if !ok || item.ItemHash == 0 {
			continue
		}
		if resolveInlineItemTierType(item) == 0 && tierLookup(item.ItemHash) == exoticTierType {
			return bucketKey, item, true
		}
	}
	return "", api.ItemSnapshot{}, false
}

func exoticArmorMatches(target, candidate api.Loadout, tierLookup func(int64) int32) bool {
	tBucket, tItem, tFound := findEquippedExoticArmor(target, tierLookup)
	cBucket, cItem, cFound := findEquippedExoticArmor(candidate, tierLookup)

	if !tFound && !cFound {
		return true
	}
	if tFound != cFound || tBucket != cBucket {
		return false
	}

	// Prismatic Exotic Class Items (Relativism, Stoicism, Solipsism) share a single ItemHash per class
	// while rolling random pairs of exotic perks, so require matching instance ID (or active perks fallback).
	if tBucket == classArmorBucketKey {
		if tItem.ItemHash != 0 && cItem.ItemHash != 0 && tItem.ItemHash != cItem.ItemHash {
			return false
		}
		tInst := snapshot.EffectiveInstanceID(tItem)
		cInst := snapshot.EffectiveInstanceID(cItem)
		tHashStr := strconv.FormatInt(tItem.ItemHash, 10)
		cHashStr := strconv.FormatInt(cItem.ItemHash, 10)

		if tInst != "" && cInst != "" && tInst != tHashStr && cInst != cHashStr {
			return tInst == cInst
		}
		return tItem.ItemHash == cItem.ItemHash && equalPerkHashes(tItem.ItemProperties.Perks, cItem.ItemProperties.Perks)
	}

	return tItem.ItemHash != 0 && tItem.ItemHash == cItem.ItemHash
}

func equalPerkHashes(a, b []api.Perk) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	aHashes := make([]int64, len(a))
	bHashes := make([]int64, len(b))
	for i := range a {
		aHashes[i] = a[i].Hash
	}
	for i := range b {
		bHashes[i] = b[i].Hash
	}
	slices.SortFunc(aHashes, cmp.Compare[int64])
	slices.SortFunc(bHashes, cmp.Compare[int64])
	return slices.Equal(aHashes, bHashes)
}

func powerWeaponMatches(target, candidate api.Loadout) bool {
	if target == nil && candidate == nil {
		return true
	}
	var tPow, cPow api.ItemSnapshot
	var tOK, cOK bool
	if target != nil {
		tPow, tOK = target[powerBucketKey]
	}
	if candidate != nil {
		cPow, cOK = candidate[powerBucketKey]
	}
	if !tOK && !cOK {
		return true
	}
	if tOK != cOK {
		return false
	}

	tInst := snapshot.EffectiveInstanceID(tPow)
	cInst := snapshot.EffectiveInstanceID(cPow)
	tHashStr := strconv.FormatInt(tPow.ItemHash, 10)
	cHashStr := strconv.FormatInt(cPow.ItemHash, 10)

	if tInst != "" && cInst != "" && tInst != tHashStr && cInst != cHashStr {
		return tInst == cInst
	}
	return tPow.ItemHash != 0 && tPow.ItemHash == cPow.ItemHash
}

func computeStatDiffs(target, candidate *api.CharacterSnapshot) *map[string]int {
	if target == nil && candidate == nil {
		return nil
	}
	var tStats, cStats map[string]api.ClassStat
	if target != nil && target.Stats != nil {
		tStats = *target.Stats
	}
	if candidate != nil && candidate.Stats != nil {
		cStats = *candidate.Stats
	}
	if len(tStats) == 0 && len(cStats) == 0 {
		return nil
	}

	keys := make(map[string]struct{}, len(tStats)+len(cStats))
	for k := range tStats {
		keys[k] = struct{}{}
	}
	for k := range cStats {
		keys[k] = struct{}{}
	}

	diffs := make(map[string]int)
	for k := range keys {
		if k == powerStatHash {
			continue
		}
		tStat, tOK := tStats[k]
		cStat, cOK := cStats[k]

		label := k
		if tOK && strings.TrimSpace(tStat.Name) != "" {
			label = strings.TrimSpace(tStat.Name)
		} else if cOK && strings.TrimSpace(cStat.Name) != "" {
			label = strings.TrimSpace(cStat.Name)
		}

		if strings.EqualFold(label, "Power") || strings.EqualFold(label, "Light") {
			continue
		}

		delta := int(cStat.Value) - int(tStat.Value)
		if delta != 0 {
			diffs[label] = delta
		}
	}

	if len(diffs) == 0 {
		return nil
	}
	return &diffs
}

type unlockedSetPerkKey struct {
	setHash  int64
	perkHash int64
}

func unlockedArmorSetPerks(snap *api.CharacterSnapshot) map[unlockedSetPerkKey]struct{} {
	if snap == nil || snap.ArmorSetBonuses == nil || len(*snap.ArmorSetBonuses) == 0 {
		return nil
	}
	unlocked := make(map[unlockedSetPerkKey]struct{})
	for _, bonus := range *snap.ArmorSetBonuses {
		for _, perk := range bonus.ActivePerks {
			unlocked[unlockedSetPerkKey{setHash: bonus.SetHash, perkHash: perk.Hash}] = struct{}{}
		}
	}
	if len(unlocked) == 0 {
		return nil
	}
	return unlocked
}

func armorSetBonusesMatch(target, candidate *api.CharacterSnapshot) bool {
	tUnlocked := unlockedArmorSetPerks(target)
	cUnlocked := unlockedArmorSetPerks(candidate)
	if len(tUnlocked) != len(cUnlocked) {
		return false
	}
	for key := range tUnlocked {
		if _, ok := cUnlocked[key]; !ok {
			return false
		}
	}
	return true
}
