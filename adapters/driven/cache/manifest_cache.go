package cache

import (
	"context"
	"oneTrick/core/activity"
	"oneTrick/core/domain"
	"oneTrick/utils"
	"sync"

	clog "github.com/Shannor/canonical-logger"
)

type ManifestReader interface {
	GetPlaces(ctx context.Context) (map[int64]domain.PlaceManifest, error)
	GetActivities(ctx context.Context) (map[int64]domain.ActivityManifest, error)
	GetClasses(ctx context.Context) (map[int64]domain.ClassManifest, error)
	GetInventoryBuckets(ctx context.Context) (map[int64]domain.InventoryBucketManifest, error)
	GetRaces(ctx context.Context) (map[int64]domain.RaceManifest, error)
	GetItemCategories(ctx context.Context) (map[int64]domain.ItemCategoryManifest, error)
	GetDamageTypes(ctx context.Context) (map[int64]domain.DamageTypeManifest, error)
	GetActivityModes(ctx context.Context) (map[int64]domain.ActivityModeManifest, error)
	GetStats(ctx context.Context) (map[int64]domain.StatManifest, error)
	GetItems(ctx context.Context) (map[int64]domain.ItemManifest, error)
	GetPerks(ctx context.Context) (map[int64]domain.PerkManifest, error)
	GetRecords(ctx context.Context) (map[int64]domain.RecordManifest, error)
	GetProgressions(ctx context.Context) (map[int64]domain.ProgressionManifest, error)
	GetEquipableItemSets(ctx context.Context) (map[int64]domain.EquipableItemSetManifest, error)

	GetItem(ctx context.Context, hash int64) (domain.ItemManifest, bool, error)
	GetActivity(ctx context.Context, hash int64) (domain.ActivityManifest, bool, error)
	GetActivityModeByHash(ctx context.Context, hash int64) (domain.ActivityModeManifest, bool, error)
	GetRecord(ctx context.Context, hash int64) (domain.RecordManifest, bool, error)
	GetProgression(ctx context.Context, hash int64) (domain.ProgressionManifest, bool, error)

	GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]domain.ItemManifest, error)
	GetPerksByIDs(ctx context.Context, ids []int64) (map[int64]domain.PerkManifest, error)
	GetActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityManifest, error)
	GetActivityModesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityModeManifest, error)
}

type collectionCache[T any] struct {
	mu    sync.RWMutex
	items map[int64]T
}

func (c *collectionCache[T]) getOrLoad(
	ctx context.Context,
	group string,
	loader func(context.Context) (map[int64]T, error),
) (map[int64]T, error) {
	c.mu.RLock()
	if c.items != nil {
		cached := c.items
		c.mu.RUnlock()
		clog.WithGroupFields(ctx, group, clog.Fields{
			"count":  len(cached),
			"cached": true,
		})
		return cached, nil
	}
	c.mu.RUnlock()

	loaded, err := loader(ctx)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.items = loaded
	c.mu.Unlock()

	clog.WithGroupField(ctx, group, "cached", false)
	return loaded, nil
}

func (c *collectionCache[T]) lookup(hash int64) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.items == nil {
		var zero T
		return zero, false
	}
	val, ok := c.items[hash]
	return val, ok
}

type hashCache[T any] struct {
	mu    sync.RWMutex
	items map[int64]T
}

func (c *hashCache[T]) getOrLoad(
	ctx context.Context,
	group string,
	hash int64,
	colCache *collectionCache[T],
	loader func(context.Context, int64) (T, bool, error),
) (T, bool, error) {
	var zero T
	if hash == 0 {
		return zero, false, nil
	}

	if colCache != nil {
		if val, ok := colCache.lookup(hash); ok {
			clog.WithGroupFields(ctx, group, clog.Fields{
				"hash":   hash,
				"cached": true,
			})
			return val, true, nil
		}
	}

	c.mu.RLock()
	if c.items != nil {
		if cached, ok := c.items[hash]; ok {
			c.mu.RUnlock()
			clog.WithGroupFields(ctx, group, clog.Fields{
				"hash":   hash,
				"cached": true,
			})
			return cached, true, nil
		}
	}
	c.mu.RUnlock()

	loaded, found, err := loader(ctx, hash)
	if err != nil {
		return zero, false, err
	}
	if !found {
		clog.WithGroupField(ctx, group, "cached", false)
		return zero, false, nil
	}

	c.mu.Lock()
	if c.items == nil {
		c.items = make(map[int64]T)
	}
	c.items[hash] = loaded
	c.mu.Unlock()

	clog.WithGroupField(ctx, group, "cached", false)
	return loaded, true, nil
}

func (c *hashCache[T]) getOrLoadMany(
	ctx context.Context,
	group string,
	ids []int64,
	colCache *collectionCache[T],
	loader func(context.Context, []int64) (map[int64]T, error),
) (map[int64]T, error) {
	uniqueIDs := utils.UniqueNonZero(ids)
	if len(uniqueIDs) == 0 {
		return make(map[int64]T), nil
	}

	result := make(map[int64]T, len(uniqueIDs))
	remaining := uniqueIDs

	if colCache != nil {
		colCache.mu.RLock()
		if colCache.items != nil {
			remaining = make([]int64, 0, len(uniqueIDs))
			for _, id := range uniqueIDs {
				if def, ok := colCache.items[id]; ok {
					result[id] = def
				} else {
					remaining = append(remaining, id)
				}
			}
		}
		colCache.mu.RUnlock()
	}

	missing := make([]int64, 0, len(remaining))
	if len(remaining) > 0 {
		c.mu.RLock()
		for _, id := range remaining {
			if c.items != nil {
				if cached, ok := c.items[id]; ok {
					result[id] = cached
					continue
				}
			}
			missing = append(missing, id)
		}
		c.mu.RUnlock()
	}

	if len(missing) == 0 {
		clog.WithGroupFields(ctx, group, clog.Fields{
			"requested": len(uniqueIDs),
			"returned":  len(result),
			"cached":    true,
		})
		return result, nil
	}

	loaded, err := loader(ctx, missing)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.items == nil {
		c.items = make(map[int64]T, len(loaded))
	}
	for id, val := range loaded {
		c.items[id] = val
		result[id] = val
	}
	c.mu.Unlock()

	clog.WithGroupFields(ctx, group, clog.Fields{
		"requested": len(uniqueIDs),
		"returned":  len(result),
		"cacheHits": len(uniqueIDs) - len(missing),
		"cached":    false,
	})
	return result, nil
}

// ManifestCache decorates a ManifestReader with concurrency-safe in-memory read-through caches
// and directly satisfies core/activity.ManifestLookup.
type ManifestCache struct {
	inner ManifestReader

	places            collectionCache[domain.PlaceManifest]
	classes           collectionCache[domain.ClassManifest]
	inventoryBuckets  collectionCache[domain.InventoryBucketManifest]
	races             collectionCache[domain.RaceManifest]
	itemCategories    collectionCache[domain.ItemCategoryManifest]
	damageTypes       collectionCache[domain.DamageTypeManifest]
	activityModes     collectionCache[domain.ActivityModeManifest]
	stats             collectionCache[domain.StatManifest]
	perks             collectionCache[domain.PerkManifest]
	progressions      collectionCache[domain.ProgressionManifest]
	equipableItemSets collectionCache[domain.EquipableItemSetManifest]

	itemsByHash         hashCache[domain.ItemManifest]
	perksByHash         hashCache[domain.PerkManifest]
	activitiesByHash    hashCache[domain.ActivityManifest]
	activityModesByHash hashCache[domain.ActivityModeManifest]
	recordsByHash       hashCache[domain.RecordManifest]
	progressionsByHash  hashCache[domain.ProgressionManifest]
}

var _ activity.ManifestLookup = (*ManifestCache)(nil)

func NewManifestCache(inner ManifestReader) *ManifestCache {
	return &ManifestCache{inner: inner}
}

func (c *ManifestCache) GetPlaces(ctx context.Context) (map[int64]domain.PlaceManifest, error) {
	return c.places.getOrLoad(ctx, "manifestCache.getPlaces", c.inner.GetPlaces)
}

func (c *ManifestCache) GetActivities(ctx context.Context) (map[int64]domain.ActivityManifest, error) {
	return c.inner.GetActivities(ctx)
}

func (c *ManifestCache) GetClasses(ctx context.Context) (map[int64]domain.ClassManifest, error) {
	return c.classes.getOrLoad(ctx, "manifestCache.getClasses", c.inner.GetClasses)
}

func (c *ManifestCache) GetInventoryBuckets(ctx context.Context) (map[int64]domain.InventoryBucketManifest, error) {
	return c.inventoryBuckets.getOrLoad(ctx, "manifestCache.getInventoryBuckets", c.inner.GetInventoryBuckets)
}

func (c *ManifestCache) GetRaces(ctx context.Context) (map[int64]domain.RaceManifest, error) {
	return c.races.getOrLoad(ctx, "manifestCache.getRaces", c.inner.GetRaces)
}

func (c *ManifestCache) GetItemCategories(ctx context.Context) (map[int64]domain.ItemCategoryManifest, error) {
	return c.itemCategories.getOrLoad(ctx, "manifestCache.getItemCategories", c.inner.GetItemCategories)
}

func (c *ManifestCache) GetDamageTypes(ctx context.Context) (map[int64]domain.DamageTypeManifest, error) {
	return c.damageTypes.getOrLoad(ctx, "manifestCache.getDamageTypes", c.inner.GetDamageTypes)
}

func (c *ManifestCache) GetActivityModes(ctx context.Context) (map[int64]domain.ActivityModeManifest, error) {
	return c.activityModes.getOrLoad(ctx, "manifestCache.getActivityModes", c.inner.GetActivityModes)
}

func (c *ManifestCache) GetStats(ctx context.Context) (map[int64]domain.StatManifest, error) {
	return c.stats.getOrLoad(ctx, "manifestCache.getStats", c.inner.GetStats)
}

func (c *ManifestCache) GetItems(ctx context.Context) (map[int64]domain.ItemManifest, error) {
	return c.inner.GetItems(ctx)
}

func (c *ManifestCache) GetPerks(ctx context.Context) (map[int64]domain.PerkManifest, error) {
	return c.perks.getOrLoad(ctx, "manifestCache.getPerks", c.inner.GetPerks)
}

func (c *ManifestCache) GetRecords(ctx context.Context) (map[int64]domain.RecordManifest, error) {
	return c.inner.GetRecords(ctx)
}

func (c *ManifestCache) GetProgressions(ctx context.Context) (map[int64]domain.ProgressionManifest, error) {
	return c.progressions.getOrLoad(ctx, "manifestCache.getProgressions", c.inner.GetProgressions)
}

func (c *ManifestCache) GetEquipableItemSets(ctx context.Context) (map[int64]domain.EquipableItemSetManifest, error) {
	return c.equipableItemSets.getOrLoad(ctx, "manifestCache.getEquipableItemSets", c.inner.GetEquipableItemSets)
}

func (c *ManifestCache) GetItem(ctx context.Context, hash int64) (domain.ItemManifest, bool, error) {
	return c.itemsByHash.getOrLoad(ctx, "manifestCache.getItem", hash, nil, c.inner.GetItem)
}

func (c *ManifestCache) GetActivity(ctx context.Context, hash int64) (domain.ActivityManifest, bool, error) {
	return c.activitiesByHash.getOrLoad(ctx, "manifestCache.getActivity", hash, nil, c.inner.GetActivity)
}

func (c *ManifestCache) GetActivityModeByHash(ctx context.Context, hash int64) (domain.ActivityModeManifest, bool, error) {
	return c.activityModesByHash.getOrLoad(ctx, "manifestCache.getActivityMode", hash, &c.activityModes, c.inner.GetActivityModeByHash)
}

func (c *ManifestCache) GetActivityMode(ctx context.Context, hash int64, modeType int) (domain.ActivityModeManifest, bool, error) {
	if hash != 0 {
		def, ok, err := c.GetActivityModeByHash(ctx, hash)
		if err == nil && ok {
			return def, true, nil
		}
	}
	if modeType == 0 {
		return domain.ActivityModeManifest{}, false, nil
	}
	modes, err := c.GetActivityModes(ctx)
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

func (c *ManifestCache) GetRecord(ctx context.Context, hash int64) (domain.RecordManifest, bool, error) {
	return c.recordsByHash.getOrLoad(ctx, "manifestCache.getRecord", hash, nil, c.inner.GetRecord)
}

func (c *ManifestCache) GetProgression(ctx context.Context, hash int64) (domain.ProgressionManifest, bool, error) {
	return c.progressionsByHash.getOrLoad(ctx, "manifestCache.getProgression", hash, &c.progressions, c.inner.GetProgression)
}

func (c *ManifestCache) GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]domain.ItemManifest, error) {
	return c.itemsByHash.getOrLoadMany(ctx, "manifestCache.getItemsByIDs", ids, nil, c.inner.GetItemsByIDs)
}

func (c *ManifestCache) GetPerksByIDs(ctx context.Context, ids []int64) (map[int64]domain.PerkManifest, error) {
	return c.perksByHash.getOrLoadMany(ctx, "manifestCache.getPerksByIDs", ids, &c.perks, c.inner.GetPerksByIDs)
}

func (c *ManifestCache) GetActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityManifest, error) {
	return c.activitiesByHash.getOrLoadMany(ctx, "manifestCache.getActivitiesByIDs", ids, nil, c.inner.GetActivitiesByIDs)
}

func (c *ManifestCache) GetActivityModesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityModeManifest, error) {
	return c.activityModesByHash.getOrLoadMany(ctx, "manifestCache.getActivityModesByIDs", ids, &c.activityModes, c.inner.GetActivityModesByIDs)
}
