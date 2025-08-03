package destiny

import (
	"cloud.google.com/go/firestore"
	"context"
	"oneTrick/utils"
	"strconv"
)

type ManifestService interface {
	GetPlaces(ctx context.Context) (map[string]PlaceDefinition, error)
	GetActivities(ctx context.Context) (map[string]ActivityDefinition, error)
	GetClasses(ctx context.Context) (map[string]ClassDefinition, error)
	GetInventoryBuckets(ctx context.Context) (map[string]InventoryBucketDefinition, error)
	GetRaces(ctx context.Context) (map[string]RaceDefinition, error)
	GetItemCategories(ctx context.Context) (map[string]ItemCategory, error)
	GetDamageTypes(ctx context.Context) (map[string]DamageType, error)
	GetActivityModes(ctx context.Context) (map[string]ActivityModeDefinition, error)
	GetStats(ctx context.Context) (map[string]StatDefinition, error)
	GetItems(ctx context.Context) (map[string]ItemDefinition, error)
	GetPerks(ctx context.Context) (map[string]PerkDefinition, error)
	GetRecords(ctx context.Context) (map[string]RecordDefinition, error)
}
type ManifestCollection string

const (
	PlaceCollection            ManifestCollection = "d2Places"
	ActivityCollection         ManifestCollection = "d2Activities"
	ClassCollection            ManifestCollection = "d2Classes"
	InventoryBucketCollection  ManifestCollection = "d2InventoryBuckets"
	RaceCollection             ManifestCollection = "d2Races"
	ItemCategoryCollection     ManifestCollection = "d2ItemCategories"
	DamageCollection           ManifestCollection = "d2DamageTypes"
	ActivityModeCollection     ManifestCollection = "d2ActivityModes"
	StatDefinitionCollection   ManifestCollection = "d2StatDefinitions"
	ItemDefinitionCollection   ManifestCollection = "d2ItemDefinitions"
	SandboxPerkCollection      ManifestCollection = "d2SandboxPerks"
	RecordDefinitionCollection ManifestCollection = "d2RecordDefinitions"
)

// ManifestService provides access to the current Destiny manifest data
type manifestService struct {
	db  *firestore.Client
	env string
}

func NewManifestService(db *firestore.Client, env string) ManifestService {
	return &manifestService{
		db:  db,
		env: env,
	}
}
func (m *manifestService) GetPlaces(ctx context.Context) (map[string]PlaceDefinition, error) {
	docs, err := m.db.Collection(string(PlaceCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[PlaceDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[PlaceDefinition, string](results, func(t PlaceDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetActivities(ctx context.Context) (map[string]ActivityDefinition, error) {
	docs, err := m.db.Collection(string(ActivityCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ActivityDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ActivityDefinition, string](results, func(t ActivityDefinition) string {
		return strconv.FormatInt(int64(t.Hash), 10)
	})
}

func (m *manifestService) GetClasses(ctx context.Context) (map[string]ClassDefinition, error) {
	docs, err := m.db.Collection(string(ClassCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ClassDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ClassDefinition, string](results, func(t ClassDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetInventoryBuckets(ctx context.Context) (map[string]InventoryBucketDefinition, error) {
	docs, err := m.db.Collection(string(InventoryBucketCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[InventoryBucketDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[InventoryBucketDefinition, string](results, func(t InventoryBucketDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetRaces(ctx context.Context) (map[string]RaceDefinition, error) {
	docs, err := m.db.Collection(string(RaceCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[RaceDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[RaceDefinition, string](results, func(t RaceDefinition) string {
		return strconv.FormatInt(int64(t.Hash), 10)
	})
}

func (m *manifestService) GetItemCategories(ctx context.Context) (map[string]ItemCategory, error) {
	docs, err := m.db.Collection(string(ItemCategoryCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ItemCategory](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ItemCategory, string](results, func(t ItemCategory) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetDamageTypes(ctx context.Context) (map[string]DamageType, error) {
	docs, err := m.db.Collection(string(DamageCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[DamageType](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[DamageType, string](results, func(t DamageType) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetActivityModes(ctx context.Context) (map[string]ActivityModeDefinition, error) {
	docs, err := m.db.Collection(string(ActivityModeCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ActivityModeDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ActivityModeDefinition, string](results, func(t ActivityModeDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetStats(ctx context.Context) (map[string]StatDefinition, error) {
	docs, err := m.db.Collection(string(StatDefinitionCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[StatDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[StatDefinition, string](results, func(t StatDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetItems(ctx context.Context) (map[string]ItemDefinition, error) {
	docs, err := m.db.Collection(string(ItemDefinitionCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ItemDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ItemDefinition, string](results, func(t ItemDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetPerks(ctx context.Context) (map[string]PerkDefinition, error) {
	docs, err := m.db.Collection(string(SandboxPerkCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[PerkDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[PerkDefinition, string](results, func(t PerkDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetRecords(ctx context.Context) (map[string]RecordDefinition, error) {
	docs, err := m.db.Collection(string(RecordDefinitionCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[RecordDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[RecordDefinition, string](results, func(t RecordDefinition) string {
		return strconv.FormatInt(int64(t.Hash), 10)
	})
}
