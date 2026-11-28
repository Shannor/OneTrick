package domain

type PlaceManifest struct {
	Hash        int64
	Name        string
	Description string
	IconURL     string
	HasIcon     bool
}

type ActivityManifest struct {
	Hash                   int64
	Name                   string
	Description            string
	PgcrImageURL           string
	DirectActivityModeHash int64
	DirectActivityModeType int
	ActivityModeHashes     []int64
	IsPvP                  bool
}

type ClassManifest struct {
	Hash      int64
	ClassType int
	Name      string
	HasIcon   bool
}

type InventoryBucketManifest struct {
	Hash        int64
	Name        string
	Description string
	HasIcon     bool
	Scope       int
	Category    int
	BucketOrder int
}

type RaceManifest struct {
	Hash        int64
	RaceType    int
	Name        string
	Description string
	HasIcon     bool
}

type ItemCategoryManifest struct {
	Hash        int64
	Name        string
	Description string
	ShortTitle  string
	HasIcon     bool
	Visible     bool
	Deprecated  bool
}

type DamageTypeManifest struct {
	Hash                int64
	Name                string
	Description         string
	Icon                string
	TransparentIconPath string
	HasIcon             bool
	ShowIcon            bool
	EnumValue           int
	Color               Color
}

type ActivityModeManifest struct {
	Hash         int64
	Name         string
	Description  string
	FriendlyName string
	ModeType     int
	IconURL      string
	PgcrImageURL string
	HasIcon      bool
	IsTeamBased  bool
}

type StatManifest struct {
	Hash            int64
	Name            string
	Description     string
	IconURL         string
	HasIcon         bool
	AggregationType int
	StatCategory    int
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
	TierTypeName               string
	TierType                   int32
	EquipableItemSetHash       *int64
}

func (m ItemManifest) ToWeaponDisplay() WeaponDisplay {
	return WeaponDisplay{
		Name:        m.Name,
		Description: m.ItemTypeAndTierDisplayName,
		Icon:        m.IconURL,
		HasIcon:     m.HasIcon,
	}
}

type PerkManifest struct {
	Hash           int64
	Name           string
	Description    string
	IconURL        string
	HasIcon        bool
	IsDisplayable  bool
	DamageType     int
	DamageTypeHash int64
}

type RecordManifest struct {
	Hash        int64
	Name        string
	Description string
	IconURL     string
	HasIcon     bool
	HasTitle    bool
	MaleTitle   string
	FemaleTitle string
}

func (r RecordManifest) ResolvedTitle() string {
	if r.MaleTitle != "" {
		return r.MaleTitle
	}
	if r.FemaleTitle != "" {
		return r.FemaleTitle
	}
	return r.Name
}

type ProgressionStep struct {
	StepName      string
	ProgressTotal int
	Icon          string
}

type ProgressionManifest struct {
	Hash        int64
	Name        string
	Description string
	IconURL     string
	HasIcon     bool
	Steps       []ProgressionStep
}

func isTransitionalProgressionStep(name string) bool {
	return name == "PROMOTION" || name == "RELEGATION"
}

// StepForPoints resolves the rank/division step corresponding to a cumulative point total
// using manifest step totals, mapping transitional series steps ("PROMOTION" / "RELEGATION")
// to their adjacent division when an explicit stepIndex is unavailable.
func (p ProgressionManifest) StepForPoints(points int) (ProgressionStep, bool) {
	if len(p.Steps) == 0 {
		return ProgressionStep{}, false
	}
	if points < 0 {
		points = 0
	}

	cumulative := 0
	matchedIdx := -1
	for i, step := range p.Steps {
		if step.ProgressTotal <= 0 {
			continue
		}
		if points < cumulative+step.ProgressTotal {
			matchedIdx = i
			break
		}
		cumulative += step.ProgressTotal
	}

	if matchedIdx == -1 {
		for i := len(p.Steps) - 1; i >= 0; i-- {
			if p.Steps[i].StepName != "" && !isTransitionalProgressionStep(p.Steps[i].StepName) {
				return p.Steps[i], true
			}
		}
		return p.Steps[len(p.Steps)-1], true
	}

	switch p.Steps[matchedIdx].StepName {
	case "PROMOTION":
		for i := matchedIdx - 1; i >= 0; i-- {
			if p.Steps[i].StepName != "" && !isTransitionalProgressionStep(p.Steps[i].StepName) {
				return p.Steps[i], true
			}
		}
	case "RELEGATION":
		for i := matchedIdx + 1; i < len(p.Steps); i++ {
			if p.Steps[i].StepName != "" && !isTransitionalProgressionStep(p.Steps[i].StepName) {
				return p.Steps[i], true
			}
		}
	}

	return p.Steps[matchedIdx], true
}

type ItemSetPerk struct {
	RequiredSetCount int
	SandboxPerkHash  int64
}

type EquipableItemSetManifest struct {
	Hash        int64
	Name        string
	Description string
	IconURL     string
	HasIcon     bool
	SetItems    []int64
	SetPerks    []ItemSetPerk
}
