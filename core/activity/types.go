package activity

import (
	"context"
	"errors"
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
