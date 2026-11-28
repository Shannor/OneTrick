package domain

import (
	"strings"
	"time"
)

type Color struct {
	Red   int
	Green int
	Blue  int
	Alpha int
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

type Character struct {
	ID                  string
	Class               string
	Race                string
	CurrentTitle        string
	Light               int64
	EmblemURL           string
	EmblemBackgroundURL string
	EmblemColor         Color
	Stats               map[string]ClassStat
}

type Membership struct {
	ID          string
	Type        int64
	DisplayName string
}

type OAuthToken struct {
	AccessToken      string
	TokenType        string
	ExpiresIn        int
	RefreshToken     string
	RefreshExpiresIn int
	MembershipID     string
}

type BungieAccount struct {
	BungieNetMembershipID string
	DisplayName           string
	UniqueName            string
	PrimaryMembershipID   string
	DestinyMemberships    []Membership
}

type BungieUserSearchResult struct {
	BungieNetMembershipID string
	PrimaryMembershipID   string
	PrimaryMembershipType int64
	DisplayName           string
	UniqueName            string
	IconPath              string
	AlternateNames        []string
}

type CompRankSnapshot struct {
	Points       int
	StepIndex    *int
	DivisionName string
	Icon         string
	RecordedAt   *time.Time
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

type ItemSnapshot struct {
	InstanceID string
	Name       string
	ItemHash   int64
	BucketHash *int64
	Details    ItemProperties
}

type Loadout map[string]ItemSnapshot

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

type LiveLoadout struct {
	Loadout         Loadout
	Stats           map[string]ClassStat
	ArmorSetBonuses []ArmorSetBonus
	MintedAt        *time.Time
}

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

type ActivityQueryMode string

const (
	ActivityQueryAllPvP      ActivityQueryMode = "allPvP"
	ActivityQueryCompetitive ActivityQueryMode = "competitive"
	ActivityQueryQuickplay   ActivityQueryMode = "quickplay"
	ActivityQueryIronBanner  ActivityQueryMode = "ironBanner"
)

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
