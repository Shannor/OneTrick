package activity

import (
	"cmp"
	"context"
	"errors"
	"oneTrick/core/gamemode"
	"oneTrick/set"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidActivityID  = errors.New("invalid activity id")
	ErrActivityNotFound   = errors.New("activity not found")
	ErrDestinyUnavailable = errors.New("destiny 2 servers unavailable")
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

type StatValuePair struct {
	Value        float64
	DisplayValue string
	Present      bool
}

type UniqueStatValue struct {
	Name       string
	Basic      StatValuePair
	PGA        StatValuePair
	Weighted   StatValuePair
	ActivityID *int64
}

type PlayerStats struct {
	Kills      StatValuePair
	Assists    StatValuePair
	Deaths     StatValuePair
	KD         StatValuePair
	KDA        StatValuePair
	Standing   StatValuePair
	Team       StatValuePair
	FireteamID StatValuePair
	TimePlayed StatValuePair
}

type WeaponDisplay struct {
	Name        string
	Description string
	Icon        string
	HasIcon     bool
}

type Color struct {
	Red   int
	Green int
	Blue  int
	Alpha int
}

type DamageInfo struct {
	DamageType      string
	DamageIcon      string
	TransparentIcon string
	Color           Color
}

type BaseItemInfo struct {
	Name                       string
	Icon                       string
	ItemHash                   int64
	InstanceID                 string
	BucketHash                 int64
	ItemTypeAndTierDisplayName string
	ItemTypeDisplayName        string
	TierTypeName               string
	TierType                   int32
	StyleBasicInfo             *BaseItemInfo
	Damage                     *DamageInfo
	EquipableItemSetHash       *int64
}

type Perk struct {
	Name        string
	Description string
	Hash        int64
	IconPath    string
}

type GunStat struct {
	Hash        int64
	Value       int64
	Name        string
	Description string
}

type Socket struct {
	PlugHash                  int
	IsEnabled                 *bool
	IsVisible                 *bool
	Name                      string
	Description               string
	Icon                      string
	ItemTypeDisplayName       string
	ItemTypeTieredDisplayName string
}

type ItemProperties struct {
	CharacterID string
	BaseInfo    BaseItemInfo
	Perks       []Perk
	Stats       map[string]GunStat
	Sockets     []Socket
	HasSockets  bool
}

type WeaponMetrics struct {
	ReferenceID         int64
	Display             WeaponDisplay
	HasDisplay          bool
	BucketHash          int64
	ItemTypeDisplayName string
	Properties          *ItemProperties
	Stats               map[string]UniqueStatValue
}

type Performance struct {
	PlayerStats PlayerStats
	Weapons     map[string]WeaponMetrics
	Extra       map[string]UniqueStatValue
}

type ItemSnapshot struct {
	InstanceID string
	Name       string
	ItemHash   int64
	BucketHash *int64
	Details    ItemProperties
}

type ClassStat struct {
	Name            string
	Description     string
	Icon            string
	HasIcon         bool
	StatCategory    int
	AggregationType int
	Value           int32
}

type ArmorSetBonusPerk struct {
	RequiredSetCount int
	Hash             int64
	Name             string
	Description      string
	IconPath         string
}

type ArmorSetBonus struct {
	SetHash     int64
	SetName     string
	ActiveCount int
	ActivePerks []ArmorSetBonusPerk
}

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

type ActivityInfo struct {
	ReferenceID       int64
	ActivityHash      int64
	InstanceID        string
	Location          string
	Description       string
	Activity          string
	ImageURL          string
	ActivityIcon      string
	Period            time.Time
	IsPrivate         bool
	HasIsPrivate      bool
	Mode              string
	EstimatedGameMode string
}

type Team struct {
	ID       string
	Standing string
	Score    string
	TeamName string
}

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

type Membership struct {
	ID          string
	Type        int64
	DisplayName string
}

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

type ItemManifest struct {
	Hash                       int64
	Name                       string
	Description                string
	IconURL                    string
	HasIcon                    bool
	ItemTypeDisplayName        string
	ItemTypeAndTierDisplayName string
	BucketHash                 int64
}

type ActivityManifest struct {
	Hash                   int64
	Name                   string
	Description            string
	PgcrImageURL           string
	DirectActivityModeHash int64
}

type ActivityModeManifest struct {
	Hash     int64
	ModeType int
	IconURL  string
}

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

func (t Team) StandingRank() int {
	switch strings.ToLower(strings.TrimSpace(t.Standing)) {
	case "victory":
		return 0
	case "defeat":
		return 2
	default:
		return 1
	}
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

func (m ItemManifest) ToWeaponDisplay() WeaponDisplay {
	return WeaponDisplay{
		Name:        m.Name,
		Description: m.ItemTypeAndTierDisplayName,
		Icon:        m.IconURL,
		HasIcon:     m.HasIcon,
	}
}

func (w *WeaponMetrics) ApplyManifest(def ItemManifest) {
	w.HasDisplay = true
	w.Display = def.ToWeaponDisplay()
	w.BucketHash = def.BucketHash
	w.ItemTypeDisplayName = def.ItemTypeDisplayName
}

func (w *WeaponMetrics) MergeStoredProperties(stored WeaponMetrics) {
	if stored.Properties == nil {
		return
	}
	w.Properties = stored.Properties
	if w.BucketHash == 0 && stored.Properties.BaseInfo.BucketHash != 0 {
		w.BucketHash = stored.Properties.BaseInfo.BucketHash
	}
	if w.ItemTypeDisplayName == "" && stored.Properties.BaseInfo.ItemTypeDisplayName != "" {
		w.ItemTypeDisplayName = stored.Properties.BaseInfo.ItemTypeDisplayName
	}
}

func (info *ActivityInfo) ApplyManifest(refDef, dirDef ActivityManifest) {
	info.Location = refDef.Name
	info.Description = refDef.Description
	info.ImageURL = refDef.PgcrImageURL
	info.Activity = dirDef.Name
	if info.Activity == "" {
		info.Activity = refDef.Name
	}
	info.Mode = gamemode.NormalizeModeName(info.Mode, dirDef.Name)
	info.EstimatedGameMode = gamemode.EstimateName(info.Mode, info.Activity)
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
		wm := WeaponMetrics{
			ReferenceID: w.ReferenceID,
			Stats:       w.Stats,
		}
		if def, ok := itemDefs[w.ReferenceID]; ok {
			wm.ApplyManifest(def)
		}
		if storedPerf != nil {
			if storedWeapon, ok := storedPerf.Weapons[key]; ok {
				wm.MergeStoredProperties(storedWeapon)
			}
		}
		weapons[key] = wm
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
