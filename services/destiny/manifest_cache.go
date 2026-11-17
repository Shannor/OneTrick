package destiny

import (
	"context"
	"strconv"
	"sync"

	clog "github.com/Shannor/canonical-logger"
)

// collectionCache is a reusable pipeline wrapper for map-returning ManifestService methods.
// It checks the in-memory cache first, and on a miss delegates to the inner service method.
type collectionCache[T any] struct {
	mu    sync.RWMutex
	items map[string]T
}

func (c *collectionCache[T]) getOrLoad(
	ctx context.Context,
	group string,
	loader func(context.Context) (map[string]T, error),
) (map[string]T, error) {
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

// hashCache is a reusable pipeline wrapper for single-definition hash lookups on ManifestService.
type hashCache[T any] struct {
	mu    sync.RWMutex
	items map[int64]*T
}

func (c *hashCache[T]) getOrLoad(
	ctx context.Context,
	group string,
	hash int64,
	loader func(context.Context, int64) (*T, error),
) (*T, error) {
	if hash == 0 {
		return loader(ctx, hash)
	}

	c.mu.RLock()
	if c.items != nil {
		if cached, ok := c.items[hash]; ok {
			c.mu.RUnlock()
			clog.WithGroupFields(ctx, group, clog.Fields{
				"hash":   hash,
				"cached": true,
			})
			return cached, nil
		}
	}
	c.mu.RUnlock()

	loaded, err := loader(ctx, hash)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.items == nil {
		c.items = make(map[int64]*T)
	}
	c.items[hash] = loaded
	c.mu.Unlock()

	clog.WithGroupField(ctx, group, "cached", false)
	return loaded, nil
}

// cachedManifestService wraps a ManifestService with an in-memory caching layer
// so the underlying service remains completely unaware of caching.
type cachedManifestService struct {
	ManifestService

	places           collectionCache[PlaceDefinition]
	classes          collectionCache[ClassDefinition]
	inventoryBuckets collectionCache[InventoryBucketDefinition]
	races            collectionCache[RaceDefinition]
	itemCategories   collectionCache[ItemCategory]
	damageTypes      collectionCache[DamageType]
	activityModes    collectionCache[ActivityModeDefinition]
	stats            collectionCache[StatDefinition]

	itemsByHash         hashCache[ItemDefinition]
	activitiesByHash    hashCache[ActivityDefinition]
	activityModesByHash hashCache[ActivityModeDefinition]
	recordsByHash       hashCache[RecordDefinition]
	progressionsByHash  hashCache[ProgressionDefinition]
}

// NewCachedManifestService wraps an existing ManifestService with in-memory caching.
func NewCachedManifestService(inner ManifestService) ManifestService {
	return &cachedManifestService{
		ManifestService: inner,
	}
}

func (c *cachedManifestService) GetPlaces(ctx context.Context) (map[string]PlaceDefinition, error) {
	return c.places.getOrLoad(ctx, "manifestService.getPlaces", c.ManifestService.GetPlaces)
}

func (c *cachedManifestService) GetClasses(ctx context.Context) (map[string]ClassDefinition, error) {
	return c.classes.getOrLoad(ctx, "manifestService.getClasses", c.ManifestService.GetClasses)
}

func (c *cachedManifestService) GetInventoryBuckets(ctx context.Context) (map[string]InventoryBucketDefinition, error) {
	return c.inventoryBuckets.getOrLoad(ctx, "manifestService.getInventoryBuckets", c.ManifestService.GetInventoryBuckets)
}

func (c *cachedManifestService) GetRaces(ctx context.Context) (map[string]RaceDefinition, error) {
	return c.races.getOrLoad(ctx, "manifestService.getRaces", c.ManifestService.GetRaces)
}

func (c *cachedManifestService) GetItemCategories(ctx context.Context) (map[string]ItemCategory, error) {
	return c.itemCategories.getOrLoad(ctx, "manifestService.getItemCategories", c.ManifestService.GetItemCategories)
}

func (c *cachedManifestService) GetDamageTypes(ctx context.Context) (map[string]DamageType, error) {
	return c.damageTypes.getOrLoad(ctx, "manifestService.getDamageTypes", c.ManifestService.GetDamageTypes)
}

func (c *cachedManifestService) GetActivityModes(ctx context.Context) (map[string]ActivityModeDefinition, error) {
	return c.activityModes.getOrLoad(ctx, "manifestService.getActivityModes", c.ManifestService.GetActivityModes)
}

func (c *cachedManifestService) GetStats(ctx context.Context) (map[string]StatDefinition, error) {
	return c.stats.getOrLoad(ctx, "manifestService.getStats", c.ManifestService.GetStats)
}

func (c *cachedManifestService) GetItem(ctx context.Context, hash int64) (*ItemDefinition, error) {
	return c.itemsByHash.getOrLoad(ctx, "manifestService.getItem", hash, c.ManifestService.GetItem)
}

func (c *cachedManifestService) GetActivity(ctx context.Context, hash int64) (*ActivityDefinition, error) {
	return c.activitiesByHash.getOrLoad(ctx, "manifestService.getActivity", hash, c.ManifestService.GetActivity)
}

func (c *cachedManifestService) GetActivityMode(ctx context.Context, hash int64) (*ActivityModeDefinition, error) {
	if hash != 0 {
		hashStr := strconv.FormatInt(hash, 10)
		c.activityModes.mu.RLock()
		if c.activityModes.items != nil {
			if def, ok := c.activityModes.items[hashStr]; ok {
				c.activityModes.mu.RUnlock()
				clog.WithGroupFields(ctx, "manifestService.getActivityMode", clog.Fields{
					"hash":   hash,
					"cached": true,
				})
				return &def, nil
			}
		}
		c.activityModes.mu.RUnlock()
	}
	return c.activityModesByHash.getOrLoad(ctx, "manifestService.getActivityMode", hash, c.ManifestService.GetActivityMode)
}

func (c *cachedManifestService) GetRecord(ctx context.Context, hash int64) (*RecordDefinition, error) {
	return c.recordsByHash.getOrLoad(ctx, "manifestService.getRecord", hash, c.ManifestService.GetRecord)
}

func (c *cachedManifestService) GetProgression(ctx context.Context, hash int64) (*ProgressionDefinition, error) {
	return c.progressionsByHash.getOrLoad(ctx, "manifestService.getProgression", hash, c.ManifestService.GetProgression)
}

