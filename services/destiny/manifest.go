package destiny

import (
	"context"
	"fmt"
	"oneTrick/utils"
	"strconv"

	"cloud.google.com/go/firestore"
)

// ManifestService provides access to Destiny 2 manifest definitions stored in Firestore
// This service retrieves both collections of definitions and individual definitions by hash
type ManifestService interface {
	// GetPlaces retrieves all place definitions from the manifest
	// Returns a map with hash values as keys and PlaceDefinition structs as values
	GetPlaces(ctx context.Context) (map[string]PlaceDefinition, error)

	// GetActivities retrieves all activity definitions from the manifest
	// Returns a map with hash values as keys and ActivityDefinition structs as values
	GetActivities(ctx context.Context) (map[string]ActivityDefinition, error)

	// GetClasses retrieves all class definitions from the manifest
	// Returns a map with hash values as keys and ClassDefinition structs as values
	GetClasses(ctx context.Context) (map[string]ClassDefinition, error)

	// GetInventoryBuckets retrieves all inventory bucket definitions from the manifest
	// Returns a map with hash values as keys and InventoryBucketDefinition structs as values
	GetInventoryBuckets(ctx context.Context) (map[string]InventoryBucketDefinition, error)

	// GetRaces retrieves all race definitions from the manifest
	// Returns a map with hash values as keys and RaceDefinition structs as values
	GetRaces(ctx context.Context) (map[string]RaceDefinition, error)

	// GetItemCategories retrieves all item category definitions from the manifest
	// Returns a map with hash values as keys and ItemCategory structs as values
	GetItemCategories(ctx context.Context) (map[string]ItemCategory, error)

	// GetDamageTypes retrieves all damage type definitions from the manifest
	// Returns a map with hash values as keys and DamageType structs as values
	GetDamageTypes(ctx context.Context) (map[string]DamageType, error)

	// GetActivityModes retrieves all activity mode definitions from the manifest
	// Returns a map with hash values as keys and ActivityModeDefinition structs as values
	GetActivityModes(ctx context.Context) (map[string]ActivityModeDefinition, error)

	// GetStats retrieves all stat definitions from the manifest
	// Returns a map with hash values as keys and StatDefinition structs as values
	GetStats(ctx context.Context) (map[string]StatDefinition, error)

	// GetItems retrieves all item definitions from the manifest
	// Returns a map with hash values as keys and ItemDefinition structs as values
	GetItems(ctx context.Context) (map[string]ItemDefinition, error)

	// GetPerks retrieves all perk definitions from the manifest
	// Returns a map with hash values as keys and PerkDefinition structs as values
	GetPerks(ctx context.Context) (map[string]PerkDefinition, error)

	// GetRecords retrieves all record definitions from the manifest
	// Returns a map with hash values as keys and RecordDefinition structs as values
	GetRecords(ctx context.Context) (map[string]RecordDefinition, error)

	// GetItem retrieves a single item definition from the manifest by its hash
	// More efficient than GetItems when only one item is needed
	// Returns nil and an error if the item isn't found
	GetItem(ctx context.Context, hash int64) (*ItemDefinition, error)

	// GetActivity retrieves a single activity definition from the manifest by its hash
	// More efficient than GetActivities when only one activity is needed
	// Returns nil and an error if the activity isn't found
	GetActivity(ctx context.Context, hash int64) (*ActivityDefinition, error)

	// GetActivityMode retrieves a single activity mode definition from the manifest by its hash
	// More efficient than GetActivityModes when only one activity mode is needed
	// Returns nil and an error if the activity mode isn't found
	GetActivityMode(ctx context.Context, hash int64) (*ActivityModeDefinition, error)
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

// GetItem retrieves a single item definition from the manifest by its hash
func (m *manifestService) GetItem(ctx context.Context, hash int64) (*ItemDefinition, error) {
	hashStr := strconv.FormatInt(hash, 10)

	doc, err := m.db.Collection(string(ItemDefinitionCollection)).Doc(hashStr).Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get item definition: %w", err)
	}

	var result ItemDefinition
	if err := doc.DataTo(&result); err != nil {
		return nil, fmt.Errorf("failed to convert item definition: %w", err)
	}

	return &result, nil
}

// GetActivity retrieves a single activity definition from the manifest by its hash
func (m *manifestService) GetActivity(ctx context.Context, hash int64) (*ActivityDefinition, error) {
	if hash == 0 {
		return nil, fmt.Errorf("activity hash cannot be zero")
	}

	hashStr := strconv.FormatInt(hash, 10)

	doc, err := m.db.Collection(string(ActivityCollection)).Doc(hashStr).Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get activity definition: %w", err)
	}

	var result ActivityDefinition
	if err := doc.DataTo(&result); err != nil {
		return nil, fmt.Errorf("failed to convert activity definition: %w", err)
	}

	return &result, nil
}

// GetActivityMode retrieves a single activity mode definition from the manifest by its hash
func (m *manifestService) GetActivityMode(ctx context.Context, hash int64) (*ActivityModeDefinition, error) {
	if hash == 0 {
		return nil, fmt.Errorf("activity mode hash cannot be zero")
	}

	hashStr := strconv.FormatInt(hash, 10)

	doc, err := m.db.Collection(string(ActivityModeCollection)).Doc(hashStr).Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get activity mode definition: %w", err)
	}

	var result ActivityModeDefinition
	if err := doc.DataTo(&result); err != nil {
		return nil, fmt.Errorf("failed to convert activity mode definition: %w", err)
	}

	return &result, nil
}
