package destiny

type Manifest struct {
	ArtDyeChannelDefinition                  map[string]any                       `json:"DestinyArtDyeChannelDefinition"`
	ArtDyeReferenceDefinition                map[string]any                       `json:"DestinyArtDyeReferenceDefinition"`
	PlaceDefinition                          map[string]PlaceDefinition           `json:"DestinyPlaceDefinition"`
	ActivityDefinition                       map[string]ActivityDefinition        `json:"DestinyActivityDefinition"`
	ActivityTypeDefinition                   map[string]any                       `json:"DestinyActivityTypeDefinition"`
	ClassDefinition                          map[string]ClassDefinition           `json:"DestinyClassDefinition"`
	GenderDefinition                         map[string]any                       `json:"DestinyGenderDefinition"`
	InventoryBucketDefinition                map[string]InventoryBucketDefinition `json:"DestinyInventoryBucketDefinition"`
	RaceDefinition                           map[string]any                       `json:"DestinyRaceDefinition"`
	UnlockDefinition                         map[string]any                       `json:"DestinyUnlockDefinition"`
	StatGroupDefinition                      map[string]any                       `json:"DestinyStatGroupDefinition"`
	ProgressionMappingDefinition             map[string]any                       `json:"DestinyProgressionMappingDefinition"`
	FactionDefinition                        map[string]any                       `json:"DestinyFactionDefinition"`
	VendorGroupDefinition                    map[string]any                       `json:"DestinyVendorGroupDefinition"`
	RewardSourceDefinition                   map[string]any                       `json:"DestinyRewardSourceDefinition"`
	UnlockValueDefinition                    map[string]any                       `json:"DestinyUnlockValueDefinition"`
	RewardMappingDefinition                  map[string]any                       `json:"DestinyRewardMappingDefinition"`
	RewardSheetDefinition                    map[string]any                       `json:"DestinyRewardSheetDefinition"`
	ItemCategoryDefinition                   map[string]ItemCategory              `json:"DestinyItemCategoryDefinition"`
	DamageTypeDefinition                     map[string]DamageType                `json:"DestinyDamageTypeDefinition"`
	ActivityModeDefinition                   map[string]any                       `json:"DestinyActivityModeDefinition"`
	MedalTierDefinition                      map[string]any                       `json:"DestinyMedalTierDefinition"`
	AchievementDefinition                    map[string]any                       `json:"DestinyAchievementDefinition"`
	ActivityGraphDefinition                  map[string]any                       `json:"DestinyActivityGraphDefinition"`
	ActivityInteractableDefinition           map[string]any                       `json:"DestinyActivityInteractableDefinition"`
	BondDefinition                           map[string]any                       `json:"DestinyBondDefinition"`
	CharacterCustomizationCategoryDefinition map[string]any                       `json:"DestinyCharacterCustomizationCategoryDefinition"`
	CharacterCustomizationOptionDefinition   map[string]any                       `json:"DestinyCharacterCustomizationOptionDefinition"`
	CollectibleDefinition                    map[string]any                       `json:"DestinyCollectibleDefinition"`
	DestinationDefinition                    map[string]any                       `json:"DestinyDestinationDefinition"`
	EntitlementOfferDefinition               map[string]any                       `json:"DestinyEntitlementOfferDefinition"`
	EquipmentSlotDefinition                  map[string]any                       `json:"DestinyEquipmentSlotDefinition"`
	EventCardDefinition                      map[string]any                       `json:"DestinyEventCardDefinition"`
	FireteamFinderActivityGraphDefinition    map[string]any                       `json:"DestinyFireteamFinderActivityGraphDefinition"`
	FireteamFinderActivitySetDefinition      map[string]any                       `json:"DestinyFireteamFinderActivitySetDefinition"`
	FireteamFinderLabelDefinition            map[string]any                       `json:"DestinyFireteamFinderLabelDefinition"`
	FireteamFinderLabelGroupDefinition       map[string]any                       `json:"DestinyFireteamFinderLabelGroupDefinition"`
	FireteamFinderOptionDefinition           map[string]any                       `json:"DestinyFireteamFinderOptionDefinition"`
	FireteamFinderOptionGroupDefinition      map[string]any                       `json:"DestinyFireteamFinderOptionGroupDefinition"`
	StatDefinition                           map[string]StatDefinition            `json:"DestinyStatDefinition"`
	InventoryItemDefinition                  map[string]ItemDefinition            `json:"DestinyInventoryItemDefinition"`
	InventoryItemLiteDefinition              map[string]any                       `json:"DestinyInventoryItemLiteDefinition"`
	ItemTierTypeDefinition                   map[string]any                       `json:"DestinyItemTierTypeDefinition"`
	LoadoutColorDefinition                   map[string]any                       `json:"DestinyLoadoutColorDefinition"`
	LoadoutIconDefinition                    map[string]any                       `json:"DestinyLoadoutIconDefinition"`
	LoadoutNameDefinition                    map[string]any                       `json:"DestinyLoadoutNameDefinition"`
	LocationDefinition                       map[string]any                       `json:"DestinyLocationDefinition"`
	LoreDefinition                           map[string]any                       `json:"DestinyLoreDefinition"`
	MaterialRequirementSetDefinition         map[string]any                       `json:"DestinyMaterialRequirementSetDefinition"`
	MetricDefinition                         map[string]any                       `json:"DestinyMetricDefinition"`
	ObjectiveDefinition                      map[string]any                       `json:"DestinyObjectiveDefinition"`
	SandboxPerkDefinition                    map[string]SandboxPerkDefinition     `json:"DestinySandboxPerkDefinition"`
	PlatformBucketMappingDefinition          map[string]any                       `json:"DestinyPlatformBucketMappingDefinition"`
	PlugSetDefinition                        map[string]any                       `json:"DestinyPlugSetDefinition"`
	PowerCapDefinition                       map[string]any                       `json:"DestinyPowerCapDefinition"`
	PresentationNodeDefinition               map[string]any                       `json:"DestinyPresentationNodeDefinition"`
	ProgressionDefinition                    map[string]any                       `json:"DestinyProgressionDefinition"`
	ProgressionLevelRequirementDefinition    map[string]any                       `json:"DestinyProgressionLevelRequirementDefinition"`
	RecordDefinition                         map[string]any                       `json:"DestinyRecordDefinition"`
	RewardAdjusterPointerDefinition          map[string]any                       `json:"DestinyRewardAdjusterPointerDefinition"`
	RewardAdjusterProgressionMapDefinition   map[string]any                       `json:"DestinyRewardAdjusterProgressionMapDefinition"`
	RewardItemListDefinition                 map[string]any                       `json:"DestinyRewardItemListDefinition"`
	SackRewardItemListDefinition             map[string]any                       `json:"DestinySackRewardItemListDefinition"`
	SandboxPatternDefinition                 map[string]any                       `json:"DestinySandboxPatternDefinition"`
	SeasonDefinition                         map[string]any                       `json:"DestinySeasonDefinition"`
	SeasonPassDefinition                     map[string]any                       `json:"DestinySeasonPassDefinition"`
	SocialCommendationDefinition             map[string]any                       `json:"DestinySocialCommendationDefinition"`
	SocketCategoryDefinition                 map[string]any                       `json:"DestinySocketCategoryDefinition"`
	SocketTypeDefinition                     map[string]any                       `json:"DestinySocketTypeDefinition"`
	TraitDefinition                          map[string]any                       `json:"DestinyTraitDefinition"`
	UnlockCountMappingDefinition             map[string]any                       `json:"DestinyUnlockCountMappingDefinition"`
	UnlockEventDefinition                    map[string]any                       `json:"DestinyUnlockEventDefinition"`
	UnlockExpressionMappingDefinition        map[string]any                       `json:"DestinyUnlockExpressionMappingDefinition"`
	VendorDefinition                         map[string]any                       `json:"DestinyVendorDefinition"`
	MilestoneDefinition                      map[string]any                       `json:"DestinyMilestoneDefinition"`
	ActivityModifierDefinition               map[string]any                       `json:"DestinyActivityModifierDefinition"`
	ReportReasonCategoryDefinition           map[string]any                       `json:"DestinyReportReasonCategoryDefinition"`
	ArtifactDefinition                       map[string]any                       `json:"DestinyArtifactDefinition"`
	BreakerTypeDefinition                    map[string]any                       `json:"DestinyBreakerTypeDefinition"`
	ChecklistDefinition                      map[string]any                       `json:"DestinyChecklistDefinition"`
	EnergyTypeDefinition                     map[string]any                       `json:"DestinyEnergyTypeDefinition"`
	SocialCommendationNodeDefinition         map[string]any                       `json:"DestinySocialCommendationNodeDefinition"`
	GuardianRankDefinition                   map[string]any                       `json:"DestinyGuardianRankDefinition"`
	GuardianRankConstantsDefinition          map[string]any                       `json:"DestinyGuardianRankConstantsDefinition"`
	LoadoutConstantsDefinition               map[string]any                       `json:"DestinyLoadoutConstantsDefinition"`
	FireteamFinderConstantsDefinition        map[string]any                       `json:"DestinyFireteamFinderConstantsDefinition"`
	GlobalConstantsDefinition                map[string]any                       `json:"DestinyGlobalConstantsDefinition"`
}

// PlaceDefinition Information around all places a player could actually go in Destiny 2
type PlaceDefinition struct {
	DisplayProperties PlaceDisplayProperties `json:"displayProperties"`
	Hash              int64                  `json:"hash"`
	Index             int                    `json:"index"`
	Redacted          bool                   `json:"redacted"`
	Blacklisted       bool                   `json:"blacklisted"`
}

type PlaceDisplayProperties struct {
	Description string `json:"description"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	HasIcon     bool   `json:"hasIcon"`
}
type ClassDefinition struct {
	ClassType                      int                    `json:"classType"`
	DisplayProperties              ClassDisplayProperties `json:"displayProperties"`
	GenderedClassNames             map[string]string      `json:"genderedClassNames"`
	GenderedClassNamesByGenderHash map[string]string      `json:"genderedClassNamesByGenderHash"`
	Hash                           int64                  `json:"hash"`
	Index                          int                    `json:"index"`
	Redacted                       bool                   `json:"redacted"`
	Blacklisted                    bool                   `json:"blacklisted"`
}

type ClassDisplayProperties struct {
	Name    string `json:"name"`
	HasIcon bool   `json:"hasIcon"`
}
type InventoryBucketDefinition struct {
	DisplayProperties      InventoryDisplayProperties `json:"displayProperties"`
	Scope                  int                        `json:"scope"`
	Category               int                        `json:"category"`
	BucketOrder            int                        `json:"bucketOrder"`
	ItemCount              int                        `json:"itemCount"`
	Location               int                        `json:"location"`
	HasTransferDestination bool                       `json:"hasTransferDestination"`
	Enabled                bool                       `json:"enabled"`
	FIFO                   bool                       `json:"fifo"`
	Hash                   int64                      `json:"hash"`
	Index                  int                        `json:"index"`
	Redacted               bool                       `json:"redacted"`
	Blacklisted            bool                       `json:"blacklisted"`
}

type InventoryDisplayProperties struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name"`
	HasIcon     bool   `json:"hasIcon"`
}

type ItemCategory struct {
	Hash                    int64               `json:"hash"`
	Index                   int                 `json:"index"`
	Visible                 bool                `json:"visible"`
	Deprecated              bool                `json:"deprecated"`
	ShortTitle              string              `json:"shortTitle"`
	DisplayProperties       ItemCategoryDisplay `json:"displayProperties"`
	GroupCategoryOnly       bool                `json:"groupCategoryOnly"`
	ParentCategoryHashes    []int64             `json:"parentCategoryHashes"`
	GroupedCategoryHashes   []int64             `json:"groupedCategoryHashes"`
	ItemTypeRegex           string              `json:"itemTypeRegex"`
	GrantDestinyItemType    int64               `json:"grantDestinyItemType"`
	GrantDestinySubType     int64               `json:"grantDestinySubType"`
	GrantDestinyClass       int64               `json:"grantDestinyClass"`
	GrantDestinyBreakerType int64               `json:"grantDestinyBreakerType"`
	OriginBucketIdentifier  string              `json:"originBucketIdentifier"`
	IsPlug                  bool                `json:"isPlug"`
	Redacted                bool                `json:"redacted"`
	Blacklisted             bool                `json:"blacklisted"`
}

type ItemCategoryDisplay struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	HasIcon     bool   `json:"hasIcon"`
}

type ItemDefinition struct {
	Hash               int64                 `json:"hash"`
	Index              int                   `json:"index"`
	DisplayProperties  ItemDisplayProperties `json:"displayProperties"`
	Inventory          Inventory             `json:"inventory"`
	Stats              ItemStats             `json:"stats"`
	EquippingBlock     EquippingBlock        `json:"equippingBlock"`
	TranslationBlock   TranslationBlock      `json:"translationBlock"`
	Quality            Quality               `json:"quality"`
	InvestmentStats    []InvestmentStat      `json:"investmentStats"`
	Perks              []ItemPerk            `json:"perks"`
	AllowActions       bool                  `json:"allowActions"`
	NonTransferrable   bool                  `json:"nonTransferrable"`
	ItemCategoryHashes []int64               `json:"itemCategoryHashes"`
	SpecialItemType    int                   `json:"specialItemType"`
	ItemType           int                   `json:"itemType"`
	ItemSubType        int                   `json:"itemSubType"`
	ClassType          int                   `json:"classType"`
	BreakerType        int                   `json:"breakerType"`
	Equippable         bool                  `json:"equippable"`
	DefaultDamageType  int                   `json:"defaultDamageType"`
	IsWrapper          bool                  `json:"isWrapper"`
	TraitIds           []string              `json:"traitIds"`
	TraitHashes        []int64               `json:"traitHashes"`
	Redacted           bool                  `json:"redacted"`
	Blacklisted        bool                  `json:"blacklisted"`
}

type ItemDisplayProperties struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	HasIcon     bool   `json:"hasIcon"`
}

type Inventory struct {
	MaxStackSize             int    `json:"maxStackSize"`
	BucketTypeHash           int64  `json:"bucketTypeHash"`
	TierTypeHash             int64  `json:"tierTypeHash"`
	IsInstanceItem           bool   `json:"isInstanceItem"`
	NonTransferrableOriginal bool   `json:"nonTransferrableOriginal"`
	TierTypeName             string `json:"tierTypeName"`
	TierType                 int    `json:"tierType"`
}

type ItemStats struct {
	DisablePrimaryStatDisplay bool                `json:"disablePrimaryStatDisplay"`
	StatGroupHash             int64               `json:"statGroupHash"`
	Stats                     map[string]ItemStat `json:"stats"`
	HasDisplayableStats       bool                `json:"hasDisplayableStats"`
	PrimaryBaseStatHash       int64               `json:"primaryBaseStatHash"`
}
type ItemPerk struct {
	PerkHash                 int64  `json:"perkHash"`
	PerkVisibility           int    `json:"perkVisibility"`
	RequirementDisplayString string `json:"requirementDisplayString"`
}
type ItemStat struct {
	StatHash       int64 `json:"statHash"`
	Value          int   `json:"value"`
	Minimum        int   `json:"minimum"`
	Maximum        int   `json:"maximum"`
	DisplayMaximum int   `json:"displayMaximum"`
}

type EquippingBlock struct {
	UniqueLabelHash       int64 `json:"uniqueLabelHash"`
	EquipmentSlotTypeHash int64 `json:"equipmentSlotTypeHash"`
}

type TranslationBlock struct {
}

type Quality struct {
}

type InvestmentStat struct {
	StatTypeHash          int64 `json:"statTypeHash"`
	Value                 int   `json:"value"`
	IsConditionallyActive bool  `json:"isConditionallyActive"`
}
type ActivityDefinition struct {
	ActivityLightLevel        int                       `json:"activityLightLevel"`
	ActivityLocationMappings  []any                     `json:"activityLocationMappings"`
	ActivityModeHashes        []int                     `json:"activityModeHashes"`
	ActivityModeTypes         []int                     `json:"activityModeTypes"`
	ActivityTypeHash          int                       `json:"activityTypeHash"`
	Blacklisted               bool                      `json:"blacklisted"`
	Challenges                []any                     `json:"challenges"`
	CompletionUnlockHash      int                       `json:"completionUnlockHash"`
	DestinationHash           int                       `json:"destinationHash"`
	DirectActivityModeHash    int                       `json:"directActivityModeHash"`
	DirectActivityModeType    int                       `json:"directActivityModeType"`
	DisplayProperties         ActivityDisplayProperties `json:"displayProperties"`
	Hash                      int                       `json:"hash"`
	Index                     int                       `json:"index"`
	InheritFromFreeRoam       bool                      `json:"inheritFromFreeRoam"`
	InsertionPoints           []any                     `json:"insertionPoints"`
	IsPlaylist                bool                      `json:"isPlaylist"`
	IsPvP                     bool                      `json:"isPvP"`
	Matchmaking               ActivityMatchmaking       `json:"matchmaking"`
	Modifiers                 []any                     `json:"modifiers"`
	OptionalUnlockStrings     []any                     `json:"optionalUnlockStrings"`
	OriginalDisplayProperties ActivityDisplayProperties `json:"originalDisplayProperties"`
	PgcrImage                 string                    `json:"pgcrImage"`
	PlaceHash                 int                       `json:"placeHash"`
	PlaylistItems             []any                     `json:"playlistItems"`
	Redacted                  bool                      `json:"redacted"`
	ReleaseIcon               string                    `json:"releaseIcon"`
	ReleaseTime               int                       `json:"releaseTime"`
	Rewards                   []any                     `json:"rewards"`
	SuppressOtherRewards      bool                      `json:"suppressOtherRewards"`
	Tier                      int                       `json:"tier"`
}

type ActivityDisplayProperties struct {
	Description string `json:"description"`
	HasIcon     bool   `json:"hasIcon"`
	Icon        string `json:"icon"`
	Name        string `json:"name"`
}

type ActivityMatchmaking struct {
	IsMatchmade          bool `json:"isMatchmade"`
	MaxParty             int  `json:"maxParty"`
	MaxPlayers           int  `json:"maxPlayers"`
	MinParty             int  `json:"minParty"`
	RequiresGuardianOath bool `json:"requiresGuardianOath"`
}

type SandboxPerkDefinition struct {
	Hash              int64                       `json:"hash"`
	Index             int                         `json:"index"`
	DisplayProperties DamageTypeDisplayProperties `json:"displayProperties"`
	IsDisplayable     bool                        `json:"isDisplayable"`
	DamageType        int                         `json:"damageType"`
	DamageTypeHash    int64                       `json:"damageTypeHash"`
	Redacted          bool                        `json:"redacted"`
	Blacklisted       bool                        `json:"blacklisted"`
}

type DamageTypeDisplayProperties struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Icon          string         `json:"icon"`
	IconSequences []IconSequence `json:"iconSequences"`
	HasIcon       bool           `json:"hasIcon"`
}

type StatDefinition struct {
	Hash              int64                 `json:"hash"`
	Index             int                   `json:"index"`
	DisplayProperties StatDisplayProperties `json:"displayProperties"`
	AggregationType   int                   `json:"aggregationType"`
	HasComputedBlock  bool                  `json:"hasComputedBlock"`
	StatCategory      int                   `json:"statCategory"`
	Interpolate       bool                  `json:"interpolate"`
	Redacted          bool                  `json:"redacted"`
	Blacklisted       bool                  `json:"blacklisted"`
}

type StatDisplayProperties struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Icon          string         `json:"icon"`
	IconSequences []IconSequence `json:"iconSequences"`
	HasIcon       bool           `json:"hasIcon"`
}

type IconSequence struct {
	Frames []string `json:"frames"`
}

type DamageType struct {
	DisplayProperties   DamageDisplayProperties `json:"displayProperties"`
	TransparentIconPath string                  `json:"transparentIconPath"`
	ShowIcon            bool                    `json:"showIcon"`
	EnumValue           int                     `json:"enumValue"`
	Color               DamageColor             `json:"color"`
	Hash                uint64                  `json:"hash"`
	Index               int                     `json:"index"`
	Redacted            bool                    `json:"redacted"`
	Blacklisted         bool                    `json:"blacklisted"`
}
type DamageDisplayProperties struct {
	Description string `json:"description"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	HasIcon     bool   `json:"hasIcon"`
}

type DamageColor struct {
	Red   int `json:"red"`
	Green int `json:"green"`
	Blue  int `json:"blue"`
	Alpha int `json:"alpha"`
}
