package firestore

import (
	"context"
	"fmt"
	"oneTrick/core/domain"
	"oneTrick/utils"
	"strconv"
	"strings"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	placesCollection                 = "d2Places"
	activitiesCollection             = "d2Activities"
	classesCollection                = "d2Classes"
	inventoryBucketsCollection       = "d2InventoryBuckets"
	racesCollection                  = "d2Races"
	itemCategoriesCollection         = "d2ItemCategories"
	damageTypesCollection            = "d2DamageTypes"
	activityModesCollection          = "d2ActivityModes"
	statDefinitionsCollection        = "d2StatDefinitions"
	itemDefinitionsCollection        = "d2ItemDefinitions"
	sandboxPerksCollection           = "d2SandboxPerks"
	recordDefinitionsCollection      = "d2RecordDefinitions"
	progressionDefinitionsCollection = "d2ProgressionDefinitions"
	equipableItemSetsCollection      = "d2EquipableItemSets"

	manifestBatchSize = 30
	bungieAssetOrigin = "https://www.bungie.net"
)

func formatManifestURL(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "/") {
		return bungieAssetOrigin + trimmed
	}
	return bungieAssetOrigin + "/" + trimmed
}

type manifestDisplayDoc struct {
	Name        string `firestore:"name"`
	Description string `firestore:"description"`
	Icon        string `firestore:"icon"`
	HasIcon     bool   `firestore:"hasIcon"`
}

type placeDefinitionDoc struct {
	Hash              int64              `firestore:"hash"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
}

func (d placeDefinitionDoc) toDomain(fallbackHash int64) domain.PlaceManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.PlaceManifest{
		Hash:        hash,
		Name:        d.DisplayProperties.Name,
		Description: d.DisplayProperties.Description,
		IconURL:     formatManifestURL(d.DisplayProperties.Icon),
		HasIcon:     d.DisplayProperties.HasIcon,
	}
}

type activityDefinitionDoc struct {
	Hash                   int64              `firestore:"hash"`
	DisplayProperties      manifestDisplayDoc `firestore:"displayProperties"`
	PgcrImage              string             `firestore:"pgcrImage"`
	DirectActivityModeHash int64              `firestore:"directActivityModeHash"`
	DirectActivityModeType int                `firestore:"directActivityModeType"`
	ActivityModeHashes     []int64            `firestore:"activityModeHashes"`
	IsPvP                  bool               `firestore:"isPvP"`
}

func (d activityDefinitionDoc) toDomain(fallbackHash int64) domain.ActivityManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.ActivityManifest{
		Hash:                   hash,
		Name:                   d.DisplayProperties.Name,
		Description:            d.DisplayProperties.Description,
		PgcrImageURL:           formatManifestURL(d.PgcrImage),
		DirectActivityModeHash: d.DirectActivityModeHash,
		DirectActivityModeType: d.DirectActivityModeType,
		ActivityModeHashes:     d.ActivityModeHashes,
		IsPvP:                  d.IsPvP,
	}
}

type classDefinitionDoc struct {
	Hash              int64              `firestore:"hash"`
	ClassType         int                `firestore:"classType"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
}

func (d classDefinitionDoc) toDomain(fallbackHash int64) domain.ClassManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.ClassManifest{
		Hash:      hash,
		ClassType: d.ClassType,
		Name:      d.DisplayProperties.Name,
		HasIcon:   d.DisplayProperties.HasIcon,
	}
}

type inventoryBucketDoc struct {
	Hash              int64              `firestore:"hash"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
	Scope             int                `firestore:"scope"`
	Category          int                `firestore:"category"`
	BucketOrder       int                `firestore:"bucketOrder"`
}

func (d inventoryBucketDoc) toDomain(fallbackHash int64) domain.InventoryBucketManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.InventoryBucketManifest{
		Hash:        hash,
		Name:        d.DisplayProperties.Name,
		Description: d.DisplayProperties.Description,
		HasIcon:     d.DisplayProperties.HasIcon,
		Scope:       d.Scope,
		Category:    d.Category,
		BucketOrder: d.BucketOrder,
	}
}

type raceDefinitionDoc struct {
	// Stored as float64 in some synced manifest documents; float64 decodes both int64 and float64 in Firestore.
	Hash              float64            `firestore:"hash"`
	RaceType          int                `firestore:"raceType"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
}

func (d raceDefinitionDoc) toDomain(fallbackHash int64) domain.RaceManifest {
	hash := int64(d.Hash)
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.RaceManifest{
		Hash:        hash,
		RaceType:    d.RaceType,
		Name:        d.DisplayProperties.Name,
		Description: d.DisplayProperties.Description,
		HasIcon:     d.DisplayProperties.HasIcon,
	}
}

type itemCategoryDoc struct {
	Hash              int64              `firestore:"hash"`
	ShortTitle        string             `firestore:"shortTitle"`
	Visible           bool               `firestore:"visible"`
	Deprecated        bool               `firestore:"deprecated"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
}

func (d itemCategoryDoc) toDomain(fallbackHash int64) domain.ItemCategoryManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.ItemCategoryManifest{
		Hash:        hash,
		Name:        d.DisplayProperties.Name,
		Description: d.DisplayProperties.Description,
		ShortTitle:  d.ShortTitle,
		HasIcon:     d.DisplayProperties.HasIcon,
		Visible:     d.Visible,
		Deprecated:  d.Deprecated,
	}
}

type damageColorDoc struct {
	Red   int `firestore:"red"`
	Green int `firestore:"green"`
	Blue  int `firestore:"blue"`
	Alpha int `firestore:"alpha"`
}

type damageTypeDoc struct {
	Hash                int64              `firestore:"hash"`
	DisplayProperties   manifestDisplayDoc `firestore:"displayProperties"`
	TransparentIconPath string             `firestore:"transparentIconPath"`
	ShowIcon            bool               `firestore:"showIcon"`
	EnumValue           int                `firestore:"enumValue"`
	Color               damageColorDoc     `firestore:"color"`
}

func (d damageTypeDoc) toDomain(fallbackHash int64) domain.DamageTypeManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.DamageTypeManifest{
		Hash:                hash,
		Name:                d.DisplayProperties.Name,
		Description:         d.DisplayProperties.Description,
		Icon:                d.DisplayProperties.Icon,
		TransparentIconPath: d.TransparentIconPath,
		HasIcon:             d.DisplayProperties.HasIcon,
		ShowIcon:            d.ShowIcon,
		EnumValue:           d.EnumValue,
		Color: domain.Color{
			Red:   d.Color.Red,
			Green: d.Color.Green,
			Blue:  d.Color.Blue,
			Alpha: d.Color.Alpha,
		},
	}
}

type activityModeDoc struct {
	Hash              int64              `firestore:"hash"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
	PgcrImage         string             `firestore:"pgcrImage"`
	ModeType          int                `firestore:"modeType"`
	FriendlyName      string             `firestore:"friendlyName"`
	IsTeamBased       bool               `firestore:"isTeamBased"`
}

func (d activityModeDoc) toDomain(fallbackHash int64) domain.ActivityModeManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.ActivityModeManifest{
		Hash:         hash,
		Name:         d.DisplayProperties.Name,
		Description:  d.DisplayProperties.Description,
		FriendlyName: d.FriendlyName,
		ModeType:     d.ModeType,
		IconURL:      formatManifestURL(d.DisplayProperties.Icon),
		PgcrImageURL: formatManifestURL(d.PgcrImage),
		HasIcon:      d.DisplayProperties.HasIcon,
		IsTeamBased:  d.IsTeamBased,
	}
}

type statDefinitionDoc struct {
	Hash              int64              `firestore:"hash"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
	AggregationType   int                `firestore:"aggregationType"`
	StatCategory      int                `firestore:"statCategory"`
}

func (d statDefinitionDoc) toDomain(fallbackHash int64) domain.StatManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.StatManifest{
		Hash:            hash,
		Name:            d.DisplayProperties.Name,
		Description:     d.DisplayProperties.Description,
		IconURL:         formatManifestURL(d.DisplayProperties.Icon),
		HasIcon:         d.DisplayProperties.HasIcon,
		AggregationType: d.AggregationType,
		StatCategory:    d.StatCategory,
	}
}

type itemInventoryDoc struct {
	BucketTypeHash int64  `firestore:"bucketTypeHash"`
	TierTypeName   string `firestore:"tierTypeName"`
	TierType       int    `firestore:"tierType"`
}

type itemEquippingBlockDoc struct {
	EquipmentSlotTypeHash int64  `firestore:"equipmentSlotTypeHash"`
	EquipableItemSetHash  *int64 `firestore:"equipableItemSetHash,omitempty"`
}

type itemDefinitionDoc struct {
	Hash                       int64                 `firestore:"hash"`
	DisplayProperties          manifestDisplayDoc    `firestore:"displayProperties"`
	Inventory                  itemInventoryDoc      `firestore:"inventory"`
	EquippingBlock             itemEquippingBlockDoc `firestore:"equippingBlock"`
	ItemTypeDisplayName        string                `firestore:"itemTypeDisplayName"`
	ItemTypeAndTierDisplayName string                `firestore:"itemTypeAndTierDisplayName"`
}

func (d itemDefinitionDoc) toDomain(fallbackHash int64) domain.ItemManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	bucketHash := d.Inventory.BucketTypeHash
	if bucketHash == 0 {
		bucketHash = d.EquippingBlock.EquipmentSlotTypeHash
	}
	return domain.ItemManifest{
		Hash:                       hash,
		Name:                       d.DisplayProperties.Name,
		Description:                d.DisplayProperties.Description,
		IconURL:                    formatManifestURL(d.DisplayProperties.Icon),
		HasIcon:                    d.DisplayProperties.HasIcon,
		ItemTypeDisplayName:        d.ItemTypeDisplayName,
		ItemTypeAndTierDisplayName: d.ItemTypeAndTierDisplayName,
		BucketHash:                 bucketHash,
		TierTypeName:               d.Inventory.TierTypeName,
		TierType:                   int32(d.Inventory.TierType),
		EquipableItemSetHash:       d.EquippingBlock.EquipableItemSetHash,
	}
}

type perkDefinitionDoc struct {
	Hash              int64              `firestore:"hash"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
	IsDisplayable     bool               `firestore:"isDisplayable"`
	DamageType        int                `firestore:"damageType"`
	DamageTypeHash    int64              `firestore:"damageTypeHash"`
}

func (d perkDefinitionDoc) toDomain(fallbackHash int64) domain.PerkManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.PerkManifest{
		Hash:           hash,
		Name:           d.DisplayProperties.Name,
		Description:    d.DisplayProperties.Description,
		IconURL:        formatManifestURL(d.DisplayProperties.Icon),
		HasIcon:        d.DisplayProperties.HasIcon,
		IsDisplayable:  d.IsDisplayable,
		DamageType:     d.DamageType,
		DamageTypeHash: d.DamageTypeHash,
	}
}

type recordTitlesByGenderDoc struct {
	Male   string `firestore:"male"`
	Female string `firestore:"female"`
}

type recordTitleInfoDoc struct {
	HasTitle       bool                    `firestore:"hasTitle"`
	TitlesByGender recordTitlesByGenderDoc `firestore:"titlesByGender"`
}

type recordDefinitionDoc struct {
	Hash              int64              `firestore:"hash"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
	TitleInfo         recordTitleInfoDoc `firestore:"titleInfo"`
}

func (d recordDefinitionDoc) toDomain(fallbackHash int64) domain.RecordManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	return domain.RecordManifest{
		Hash:        hash,
		Name:        d.DisplayProperties.Name,
		Description: d.DisplayProperties.Description,
		IconURL:     formatManifestURL(d.DisplayProperties.Icon),
		HasIcon:     d.DisplayProperties.HasIcon,
		HasTitle:    d.TitleInfo.HasTitle,
		MaleTitle:   d.TitleInfo.TitlesByGender.Male,
		FemaleTitle: d.TitleInfo.TitlesByGender.Female,
	}
}

type progressionStepDoc struct {
	StepName      string `firestore:"stepName"`
	ProgressTotal int    `firestore:"progressTotal"`
	Icon          string `firestore:"icon"`
}

type progressionDefinitionDoc struct {
	Hash              int64                `firestore:"hash"`
	DisplayProperties manifestDisplayDoc   `firestore:"displayProperties"`
	Steps             []progressionStepDoc `firestore:"steps"`
}

func (d progressionDefinitionDoc) toDomain(fallbackHash int64) domain.ProgressionManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	steps := make([]domain.ProgressionStep, 0, len(d.Steps))
	for _, s := range d.Steps {
		steps = append(steps, domain.ProgressionStep{
			StepName:      s.StepName,
			ProgressTotal: s.ProgressTotal,
			Icon:          s.Icon,
		})
	}
	return domain.ProgressionManifest{
		Hash:        hash,
		Name:        d.DisplayProperties.Name,
		Description: d.DisplayProperties.Description,
		IconURL:     formatManifestURL(d.DisplayProperties.Icon),
		HasIcon:     d.DisplayProperties.HasIcon,
		Steps:       steps,
	}
}

type itemSetPerkDoc struct {
	RequiredSetCount int   `firestore:"requiredSetCount"`
	SandboxPerkHash  int64 `firestore:"sandboxPerkHash"`
}

type equipableItemSetDoc struct {
	Hash              int64              `firestore:"hash"`
	DisplayProperties manifestDisplayDoc `firestore:"displayProperties"`
	SetItems          []int64            `firestore:"setItems"`
	SetPerks          []itemSetPerkDoc   `firestore:"setPerks"`
}

func (d equipableItemSetDoc) toDomain(fallbackHash int64) domain.EquipableItemSetManifest {
	hash := d.Hash
	if hash == 0 {
		hash = fallbackHash
	}
	perks := make([]domain.ItemSetPerk, 0, len(d.SetPerks))
	for _, p := range d.SetPerks {
		perks = append(perks, domain.ItemSetPerk{
			RequiredSetCount: p.RequiredSetCount,
			SandboxPerkHash:  p.SandboxPerkHash,
		})
	}
	return domain.EquipableItemSetManifest{
		Hash:        hash,
		Name:        d.DisplayProperties.Name,
		Description: d.DisplayProperties.Description,
		IconURL:     formatManifestURL(d.DisplayProperties.Icon),
		HasIcon:     d.DisplayProperties.HasIcon,
		SetItems:    d.SetItems,
		SetPerks:    perks,
	}
}

// ManifestStore reads Destiny 2 Manifest definitions across the 14 Firestore d2* collections.
type ManifestStore struct {
	db *firestore.Client
}

func NewManifestStore(db *firestore.Client) *ManifestStore {
	return &ManifestStore{db: db}
}

func parseDocIDHash(doc *firestore.DocumentSnapshot) int64 {
	if doc == nil || doc.Ref == nil {
		return 0
	}
	parsed, err := strconv.ParseInt(doc.Ref.ID, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func loadAllManifestDocs[Doc any, Dom any](
	ctx context.Context,
	db *firestore.Client,
	collection string,
	group string,
	toDomain func(Doc, int64) Dom,
	hashOf func(Dom) int64,
) (map[int64]Dom, error) {
	if db == nil {
		return nil, fmt.Errorf("manifest database client is not configured")
	}
	docs, err := db.Collection(collection).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results := make(map[int64]Dom, len(docs))
	for _, snap := range docs {
		if !snap.Exists() {
			continue
		}
		var raw Doc
		if err := snap.DataTo(&raw); err != nil {
			return nil, fmt.Errorf("decode %s/%s: %w", collection, snap.Ref.ID, err)
		}
		dom := toDomain(raw, parseDocIDHash(snap))
		if h := hashOf(dom); h != 0 {
			results[h] = dom
		}
	}
	clog.WithGroupField(ctx, group, "count", len(results))
	return results, nil
}

func loadManifestDocByHash[Doc any, Dom any](
	ctx context.Context,
	db *firestore.Client,
	collection string,
	group string,
	hash int64,
	toDomain func(Doc, int64) Dom,
) (Dom, bool, error) {
	var zero Dom
	clog.WithGroupField(ctx, group, "hash", hash)
	if hash == 0 {
		return zero, false, nil
	}
	if db == nil {
		return zero, false, fmt.Errorf("manifest database client is not configured")
	}

	snap, err := db.Collection(collection).Doc(strconv.FormatInt(hash, 10)).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return zero, false, nil
		}
		return zero, false, fmt.Errorf("get %s/%d: %w", collection, hash, err)
	}
	var raw Doc
	if err := snap.DataTo(&raw); err != nil {
		return zero, false, fmt.Errorf("decode %s/%d: %w", collection, hash, err)
	}
	return toDomain(raw, hash), true, nil
}

func batchedLoadManifestDocs[Doc any, Dom any](
	ctx context.Context,
	db *firestore.Client,
	collection string,
	group string,
	ids []int64,
	toDomain func(Doc, int64) Dom,
	hashOf func(Dom) int64,
) (map[int64]Dom, error) {
	uniqueIDs := utils.UniqueNonZero(ids)
	if len(uniqueIDs) == 0 {
		return make(map[int64]Dom), nil
	}
	if db == nil {
		return nil, fmt.Errorf("manifest database client is not configured")
	}

	col := db.Collection(collection)
	results := make(map[int64]Dom, len(uniqueIDs))
	missingCount := 0

	for _, chunk := range utils.Chunk(uniqueIDs, manifestBatchSize) {
		docRefs := make([]*firestore.DocumentRef, len(chunk))
		for i, id := range chunk {
			docRefs[i] = col.Doc(strconv.FormatInt(id, 10))
		}
		docs, err := db.GetAll(ctx, docRefs)
		if err != nil {
			return nil, err
		}
		for i, snap := range docs {
			if !snap.Exists() {
				missingCount++
				continue
			}
			var raw Doc
			if err := snap.DataTo(&raw); err != nil {
				return nil, fmt.Errorf("decode %s/%d: %w", collection, chunk[i], err)
			}
			dom := toDomain(raw, chunk[i])
			h := hashOf(dom)
			if h == 0 {
				h = chunk[i]
			}
			results[h] = dom
		}
	}

	clog.WithGroupFields(ctx, group, clog.Fields{
		"requested": len(uniqueIDs),
		"returned":  len(results),
	})
	if missingCount > 0 {
		clog.WithGroupFields(ctx, group, clog.Fields{
			"warning":      "some manifest definitions not found",
			"missingCount": missingCount,
		})
	}
	return results, nil
}

func (m *ManifestStore) GetPlaces(ctx context.Context) (map[int64]domain.PlaceManifest, error) {
	return loadAllManifestDocs(ctx, m.db, placesCollection, "manifestStore.getPlaces", placeDefinitionDoc.toDomain, func(v domain.PlaceManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetActivities(ctx context.Context) (map[int64]domain.ActivityManifest, error) {
	return loadAllManifestDocs(ctx, m.db, activitiesCollection, "manifestStore.getActivities", activityDefinitionDoc.toDomain, func(v domain.ActivityManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetClasses(ctx context.Context) (map[int64]domain.ClassManifest, error) {
	return loadAllManifestDocs(ctx, m.db, classesCollection, "manifestStore.getClasses", classDefinitionDoc.toDomain, func(v domain.ClassManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetInventoryBuckets(ctx context.Context) (map[int64]domain.InventoryBucketManifest, error) {
	return loadAllManifestDocs(ctx, m.db, inventoryBucketsCollection, "manifestStore.getInventoryBuckets", inventoryBucketDoc.toDomain, func(v domain.InventoryBucketManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetRaces(ctx context.Context) (map[int64]domain.RaceManifest, error) {
	return loadAllManifestDocs(ctx, m.db, racesCollection, "manifestStore.getRaces", raceDefinitionDoc.toDomain, func(v domain.RaceManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetItemCategories(ctx context.Context) (map[int64]domain.ItemCategoryManifest, error) {
	return loadAllManifestDocs(ctx, m.db, itemCategoriesCollection, "manifestStore.getItemCategories", itemCategoryDoc.toDomain, func(v domain.ItemCategoryManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetDamageTypes(ctx context.Context) (map[int64]domain.DamageTypeManifest, error) {
	return loadAllManifestDocs(ctx, m.db, damageTypesCollection, "manifestStore.getDamageTypes", damageTypeDoc.toDomain, func(v domain.DamageTypeManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetActivityModes(ctx context.Context) (map[int64]domain.ActivityModeManifest, error) {
	return loadAllManifestDocs(ctx, m.db, activityModesCollection, "manifestStore.getActivityModes", activityModeDoc.toDomain, func(v domain.ActivityModeManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetStats(ctx context.Context) (map[int64]domain.StatManifest, error) {
	return loadAllManifestDocs(ctx, m.db, statDefinitionsCollection, "manifestStore.getStats", statDefinitionDoc.toDomain, func(v domain.StatManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetItems(ctx context.Context) (map[int64]domain.ItemManifest, error) {
	return loadAllManifestDocs(ctx, m.db, itemDefinitionsCollection, "manifestStore.getItems", itemDefinitionDoc.toDomain, func(v domain.ItemManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetPerks(ctx context.Context) (map[int64]domain.PerkManifest, error) {
	return loadAllManifestDocs(ctx, m.db, sandboxPerksCollection, "manifestStore.getPerks", perkDefinitionDoc.toDomain, func(v domain.PerkManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetRecords(ctx context.Context) (map[int64]domain.RecordManifest, error) {
	return loadAllManifestDocs(ctx, m.db, recordDefinitionsCollection, "manifestStore.getRecords", recordDefinitionDoc.toDomain, func(v domain.RecordManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetProgressions(ctx context.Context) (map[int64]domain.ProgressionManifest, error) {
	return loadAllManifestDocs(ctx, m.db, progressionDefinitionsCollection, "manifestStore.getProgressions", progressionDefinitionDoc.toDomain, func(v domain.ProgressionManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetEquipableItemSets(ctx context.Context) (map[int64]domain.EquipableItemSetManifest, error) {
	return loadAllManifestDocs(ctx, m.db, equipableItemSetsCollection, "manifestStore.getEquipableItemSets", equipableItemSetDoc.toDomain, func(v domain.EquipableItemSetManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetItem(ctx context.Context, hash int64) (domain.ItemManifest, bool, error) {
	return loadManifestDocByHash(ctx, m.db, itemDefinitionsCollection, "manifestStore.getItem", hash, itemDefinitionDoc.toDomain)
}

func (m *ManifestStore) GetActivity(ctx context.Context, hash int64) (domain.ActivityManifest, bool, error) {
	return loadManifestDocByHash(ctx, m.db, activitiesCollection, "manifestStore.getActivity", hash, activityDefinitionDoc.toDomain)
}

func (m *ManifestStore) GetActivityModeByHash(ctx context.Context, hash int64) (domain.ActivityModeManifest, bool, error) {
	return loadManifestDocByHash(ctx, m.db, activityModesCollection, "manifestStore.getActivityMode", hash, activityModeDoc.toDomain)
}

func (m *ManifestStore) GetActivityMode(ctx context.Context, hash int64, modeType int) (domain.ActivityModeManifest, bool, error) {
	if hash != 0 {
		def, ok, err := m.GetActivityModeByHash(ctx, hash)
		if err != nil {
			return domain.ActivityModeManifest{}, false, err
		}
		if ok {
			return def, true, nil
		}
	}
	if modeType == 0 {
		return domain.ActivityModeManifest{}, false, nil
	}
	modes, err := m.GetActivityModes(ctx)
	if err != nil {
		return domain.ActivityModeManifest{}, false, err
	}
	for _, def := range modes {
		if def.ModeType == modeType {
			return def, true, nil
		}
	}
	return domain.ActivityModeManifest{}, false, nil
}

func (m *ManifestStore) GetRecord(ctx context.Context, hash int64) (domain.RecordManifest, bool, error) {
	return loadManifestDocByHash(ctx, m.db, recordDefinitionsCollection, "manifestStore.getRecord", hash, recordDefinitionDoc.toDomain)
}

func (m *ManifestStore) GetProgression(ctx context.Context, hash int64) (domain.ProgressionManifest, bool, error) {
	return loadManifestDocByHash(ctx, m.db, progressionDefinitionsCollection, "manifestStore.getProgression", hash, progressionDefinitionDoc.toDomain)
}

func (m *ManifestStore) GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]domain.ItemManifest, error) {
	return batchedLoadManifestDocs(ctx, m.db, itemDefinitionsCollection, "manifestStore.getItemsByIDs", ids, itemDefinitionDoc.toDomain, func(v domain.ItemManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetPerksByIDs(ctx context.Context, ids []int64) (map[int64]domain.PerkManifest, error) {
	return batchedLoadManifestDocs(ctx, m.db, sandboxPerksCollection, "manifestStore.getPerksByIDs", ids, perkDefinitionDoc.toDomain, func(v domain.PerkManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityManifest, error) {
	return batchedLoadManifestDocs(ctx, m.db, activitiesCollection, "manifestStore.getActivitiesByIDs", ids, activityDefinitionDoc.toDomain, func(v domain.ActivityManifest) int64 { return v.Hash })
}

func (m *ManifestStore) GetActivityModesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityModeManifest, error) {
	return batchedLoadManifestDocs(ctx, m.db, activityModesCollection, "manifestStore.getActivityModesByIDs", ids, activityModeDoc.toDomain, func(v domain.ActivityModeManifest) int64 { return v.Hash })
}
