package activity

import (
	"cmp"
	"context"
	"oneTrick/core/domain"
	"oneTrick/core/gamemode"
	"oneTrick/set"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidActivityID  = domain.ErrInvalidActivityID
	ErrActivityNotFound   = domain.ErrActivityNotFound
	ErrDestinyUnavailable = domain.ErrDestinyUnavailable
)

type ConfidenceLevel string

const (
	ConfidenceNotFound ConfidenceLevel = "notFound"
	ConfidenceNoMatch  ConfidenceLevel = "noMatch"
	ConfidenceLow      ConfidenceLevel = "low"
	ConfidenceMedium   ConfidenceLevel = "medium"
	ConfidenceHigh     ConfidenceLevel = "high"
)

type ConfidenceSource string

const (
	ConfidenceSourceSystem ConfidenceSource = "system"
	ConfidenceSourceUser   ConfidenceSource = "user"
)

type StatValuePair = domain.StatValuePair
type UniqueStatValue = domain.UniqueStatValue
type PlayerStats = domain.PlayerStats
type WeaponDisplay = domain.WeaponDisplay
type Color = domain.Color
type DamageInfo = domain.DamageInfo
type BaseItemInfo = domain.BaseItemInfo
type Perk = domain.Perk
type GunStat = domain.GunStat
type Socket = domain.Socket
type ItemProperties = domain.ItemProperties
type WeaponMetrics = domain.WeaponMetrics
type Performance = domain.Performance
type ItemSnapshot = domain.ItemSnapshot
type ClassStat = domain.ClassStat
type ArmorSetBonusPerk = domain.ArmorSetBonusPerk
type ArmorSetBonus = domain.ArmorSetBonus

type CharacterSnapshot struct {
	ID              string
	Name            string
	Description     string
	UserID          string
	Hash            string
	CharacterID     string
	Stats           map[string]ClassStat
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Loadout         map[string]ItemSnapshot
	MergedIntoID    string
	MergedAt        *time.Time
	MergedFromIDs   []string
	ArmorSetBonuses []ArmorSetBonus
}

type ActivityInfo = domain.ActivityInfo
type Team = domain.Team

type SnapshotLink struct {
	CharacterID        string
	SnapshotID         string
	OriginalSnapshotID string
	SessionID          string
	ConfidenceLevel    ConfidenceLevel
	ConfidenceSource   ConfidenceSource
	CreatedAt          time.Time
	UserID             string
	DisplayName        string
	CharacterClass     string
	EmblemURL          string
}

type Aggregate struct {
	ID              string
	ActivityID      string
	SessionIDs      []string
	CharacterIDs    []string
	SnapshotIDs     []string
	ActivityDetails ActivityInfo
	SnapshotLinks   map[string]SnapshotLink
	Performance     map[string]Performance
	CreatedAt       time.Time
}

type Membership = domain.Membership

type User struct {
	ID                  string
	MemberID            string
	PrimaryMembershipID string
	UniqueName          string
	DisplayName         string
	Memberships         []Membership
	CreatedAt           time.Time
	CharacterIDs        []string
}

type Participant struct {
	CharacterID     string
	MembershipID    string
	MembershipType  int32
	DisplayName     string
	DisplayNameCode string
	CharacterClass  string
	LightLevel      int32
	EmblemIconURL   string
	TeamID          string
	Standing        int32
	Score           StatValuePair
	Completed       bool
	FireteamID      string
	Performance     Performance
	UserID          string
	SessionID       string
	SnapshotID      string
	ConfidenceLevel ConfidenceLevel
}

type MatchReport struct {
	Activity        ActivityInfo
	Teams           []Team
	Participants    []Participant
	Aggregate       *Aggregate
	Snapshots       map[string]CharacterSnapshot
	Users           map[string]User
	PostGameEntries []map[string]any
}

// CarnageReport represents the live Post-Game Carnage Report returned by the Bungie gateway.
type CarnageReport struct {
	Period          time.Time
	ActivityDetails CarnageActivityDetails
	Teams           []Team
	Entries         []CarnageEntry
	RawEntries      []map[string]any
}

type CarnageActivityDetails struct {
	ReferenceID          int64
	DirectorActivityHash int64
	InstanceID           string
	Mode                 int
	ModeName             string
	IsPrivate            bool
	HasIsPrivate         bool
}

type CarnageWeapon struct {
	ReferenceID int64
	Stats       map[string]UniqueStatValue
}

type CarnageEntry struct {
	CharacterID           string
	MembershipID          string
	BungieNetMembershipID string
	MembershipType        int32
	DisplayName           string
	DisplayNameCode       string
	CharacterClass        string
	LightLevel            int32
	EmblemHash            int64
	Standing              int32
	HasStanding           bool
	Score                 StatValuePair
	Completed             bool
	PlayerStats           PlayerStats
	ExtraStats            map[string]UniqueStatValue
	Weapons               []CarnageWeapon
}

type ItemManifest = domain.ItemManifest
type ActivityManifest = domain.ActivityManifest
type ActivityModeManifest = domain.ActivityModeManifest

type CarnageReportGateway interface {
	GetPostGameCarnageReport(ctx context.Context, activityID string) (CarnageReport, error)
}

type ManifestLookup interface {
	GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]ItemManifest, error)
	GetActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]ActivityManifest, error)
	GetActivityMode(ctx context.Context, hash int64, modeType int) (ActivityModeManifest, bool, error)
}

type AggregateReader interface {
	GetByActivityID(ctx context.Context, activityID string) (Aggregate, bool, error)
}

type SnapshotReader interface {
	GetByIDs(ctx context.Context, snapshotIDs []string) ([]CharacterSnapshot, error)
}

type UserLookup interface {
	FindByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]User, error)
	GetByIDs(ctx context.Context, userIDs []string) ([]User, error)
}

func (l SnapshotLink) ActiveConfidenceLevel(snapshotID string) ConfidenceLevel {
	if strings.TrimSpace(l.SessionID) == "" && strings.TrimSpace(snapshotID) == "" {
		return ""
	}
	return l.ConfidenceLevel
}

func (a Aggregate) LinkFor(characterID string) (SnapshotLink, bool) {
	if len(a.SnapshotLinks) == 0 {
		return SnapshotLink{}, false
	}
	link, ok := a.SnapshotLinks[characterID]
	return link, ok
}

func (a Aggregate) PerformanceFor(characterID string) (Performance, bool) {
	if len(a.Performance) == 0 {
		return Performance{}, false
	}
	perf, ok := a.Performance[characterID]
	return perf, ok
}

func (a Aggregate) UniqueSnapshotIDs() []string {
	if len(a.SnapshotLinks) == 0 {
		return nil
	}
	ids := set.NewOrdered[string]()
	for _, link := range a.SnapshotLinks {
		if id := strings.TrimSpace(link.SnapshotID); id != "" {
			ids.Add(id)
		}
	}
	return ids.ToSlice()
}

func (w CarnageWeapon) ToMetrics(def ItemManifest, hasDef bool, stored WeaponMetrics, hasStored bool) WeaponMetrics {
	var (
		display    WeaponDisplay
		bucketHash int64
		itemType   string
	)
	if hasDef {
		display = def.ToWeaponDisplay()
		bucketHash = def.BucketHash
		itemType = def.ItemTypeDisplayName
	}

	var props *ItemProperties
	if hasStored && stored.Properties != nil {
		props = stored.Properties
		if bucketHash == 0 && props.BaseInfo.BucketHash != 0 {
			bucketHash = props.BaseInfo.BucketHash
		}
		if itemType == "" && props.BaseInfo.ItemTypeDisplayName != "" {
			itemType = props.BaseInfo.ItemTypeDisplayName
		}
	}

	return WeaponMetrics{
		ReferenceID:         w.ReferenceID,
		Display:             display,
		HasDisplay:          hasDef,
		BucketHash:          bucketHash,
		ItemTypeDisplayName: itemType,
		Properties:          props,
		Stats:               w.Stats,
	}
}

func (d CarnageActivityDetails) ManifestActivityHashes() []int64 {
	hashes := set.NewOrdered[int64]()
	if d.ReferenceID != 0 {
		hashes.Add(d.ReferenceID)
	}
	if d.DirectorActivityHash != 0 {
		hashes.Add(d.DirectorActivityHash)
	}
	return hashes.ToSlice()
}

func (r CarnageReport) ToActivityInfo(fallbackID string, refDef, dirDef ActivityManifest, activityIcon string) ActivityInfo {
	details := r.ActivityDetails
	instanceID := details.InstanceID
	if instanceID == "" {
		instanceID = fallbackID
	}

	activityName := dirDef.Name
	if activityName == "" {
		activityName = refDef.Name
	}

	mode := gamemode.NormalizeModeName(details.ModeName, dirDef.Name)
	return ActivityInfo{
		ReferenceID:       details.ReferenceID,
		ActivityHash:      details.DirectorActivityHash,
		InstanceID:        instanceID,
		Location:          refDef.Name,
		Description:       refDef.Description,
		Activity:          activityName,
		ImageURL:          refDef.PgcrImageURL,
		ActivityIcon:      activityIcon,
		Period:            r.Period,
		IsPrivate:         details.IsPrivate,
		HasIsPrivate:      details.HasIsPrivate,
		Mode:              mode,
		EstimatedGameMode: gamemode.EstimateName(mode, activityName),
	}
}

func (r CarnageReport) HasTeams() bool {
	return len(r.Teams) > 0
}

func (r CarnageReport) ManifestItemIDs() []int64 {
	if len(r.Entries) == 0 {
		return nil
	}
	hashes := set.NewOrdered[int64]()
	for _, entry := range r.Entries {
		if entry.EmblemHash != 0 {
			hashes.Add(entry.EmblemHash)
		}
		for _, w := range entry.Weapons {
			if w.ReferenceID != 0 {
				hashes.Add(w.ReferenceID)
			}
		}
	}
	return hashes.ToSlice()
}

func (r CarnageReport) MembershipLookupIDs() []string {
	if len(r.Entries) == 0 {
		return nil
	}
	ids := set.NewOrdered[string]()
	for _, entry := range r.Entries {
		if id := strings.TrimSpace(entry.MembershipID); id != "" {
			ids.Add(id)
		}
		if id := strings.TrimSpace(entry.BungieNetMembershipID); id != "" {
			ids.Add(id)
		}
	}
	return ids.ToSlice()
}

func (e CarnageEntry) ResolvedStanding() int32 {
	if !e.HasStanding && e.PlayerStats.Standing.Present {
		return int32(e.PlayerStats.Standing.Value)
	}
	return e.Standing
}

func (e CarnageEntry) TeamID(hasTeams bool) string {
	teamStat := e.PlayerStats.Team
	if !hasTeams || !teamStat.Present || teamStat.Value < 0 {
		return ""
	}
	return strconv.FormatInt(int64(teamStat.Value), 10)
}

func (e CarnageEntry) FireteamID() string {
	ftStat := e.PlayerStats.FireteamID
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

func (e CarnageEntry) BuildPerformance(itemDefs map[int64]ItemManifest, storedPerf *Performance) Performance {
	weapons := make(map[string]WeaponMetrics, len(e.Weapons))
	for _, w := range e.Weapons {
		if w.ReferenceID == 0 {
			continue
		}
		key := strconv.FormatInt(w.ReferenceID, 10)
		def, hasDef := itemDefs[w.ReferenceID]
		var (
			stored    WeaponMetrics
			hasStored bool
		)
		if storedPerf != nil {
			stored, hasStored = storedPerf.Weapons[key]
		}
		weapons[key] = w.ToMetrics(def, hasDef, stored, hasStored)
	}
	return Performance{
		PlayerStats: e.PlayerStats,
		Weapons:     weapons,
		Extra:       e.ExtraStats,
	}
}

func (p Participant) ComparePerformanceDesc(other Participant) int {
	if p.Completed != other.Completed {
		if p.Completed {
			return -1
		}
		return 1
	}
	if c := cmp.Compare(other.Score.Value, p.Score.Value); c != 0 {
		return c
	}
	if c := cmp.Compare(other.Performance.PlayerStats.Kills.Value, p.Performance.PlayerStats.Kills.Value); c != 0 {
		return c
	}
	if c := cmp.Compare(other.Performance.PlayerStats.KDA.Value, p.Performance.PlayerStats.KDA.Value); c != 0 {
		return c
	}
	return cmp.Compare(p.CharacterID, other.CharacterID)
}
