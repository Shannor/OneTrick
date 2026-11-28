package destiny

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/core/inventory"
	"strconv"
	"sync"

	clog "github.com/Shannor/canonical-logger"
)

type fetchedItem struct {
	instanceID string
	detail     *bungie.DestinyItem
}

func (f fetchedItem) component() *bungie.ItemComponent {
	if f.detail == nil || f.detail.Item == nil {
		return nil
	}
	return f.detail.Item.ItemComponent
}

func (f fetchedItem) itemHash() (int64, bool) {
	c := f.component()
	if c == nil || c.ItemHash == nil {
		return 0, false
	}
	return int64(*c.ItemHash), true
}

func (f fetchedItem) applyFallbackOverrideStyle(overrideHash *uint32) {
	c := f.component()
	if c != nil && c.OverrideStyleItemHash == nil && overrideHash != nil {
		c.OverrideStyleItemHash = overrideHash
	}
}

func (f fetchedItem) manifestItemIDs() []int64 {
	var ids []int64
	if c := f.component(); c != nil {
		if c.ItemHash != nil {
			ids = append(ids, int64(*c.ItemHash))
		}
		if c.OverrideStyleItemHash != nil && *c.OverrideStyleItemHash != 0 {
			ids = append(ids, int64(*c.OverrideStyleItemHash))
		}
	}
	if f.detail != nil && f.detail.Sockets != nil && f.detail.Sockets.Data != nil && f.detail.Sockets.Data.Sockets != nil {
		for _, s := range *f.detail.Sockets.Data.Sockets {
			if s.PlugHash != nil {
				ids = append(ids, int64(*s.PlugHash))
			}
		}
	}
	return ids
}

func (f fetchedItem) perkIDs() []int64 {
	if f.detail == nil || f.detail.Perks == nil || f.detail.Perks.Data == nil || f.detail.Perks.Data.Perks == nil {
		return nil
	}
	perks := *f.detail.Perks.Data.Perks
	ids := make([]int64, 0, len(perks))
	for _, p := range perks {
		if p.PerkHash != nil {
			ids = append(ids, int64(*p.PerkHash))
		}
	}
	return ids
}

type itemSetIndex struct {
	sets          map[string]EquipableItemSetDefinition
	itemToSetHash map[int64]int64
}

func newItemSetIndex(sets map[string]EquipableItemSetDefinition) itemSetIndex {
	itemToSetHash := make(map[int64]int64)
	for _, setDef := range sets {
		for _, itemHash := range setDef.SetItems {
			itemToSetHash[itemHash] = setDef.Hash
		}
	}
	return itemSetIndex{
		sets:          sets,
		itemToSetHash: itemToSetHash,
	}
}

func (idx itemSetIndex) resolveSetHash(itemHash int64, d2Items map[string]ItemDefinition) (int64, bool) {
	if setHash, ok := idx.itemToSetHash[itemHash]; ok {
		return setHash, true
	}
	if def, ok := d2Items[strconv.FormatInt(itemHash, 10)]; ok && def.EquippingBlock.EquipableItemSetHash != nil {
		setHash := *def.EquippingBlock.EquipableItemSetHash
		return setHash, setHash != 0
	}
	return 0, false
}

func (idx itemSetIndex) perkIDsFor(fetched []fetchedItem, d2Items map[string]ItemDefinition) []int64 {
	if len(idx.sets) == 0 || len(fetched) == 0 {
		return nil
	}
	var perkIDs []int64
	for _, f := range fetched {
		itemHash, ok := f.itemHash()
		if !ok {
			continue
		}
		setHash, found := idx.resolveSetHash(itemHash, d2Items)
		if !found {
			continue
		}
		if setDef, ok := idx.sets[strconv.FormatInt(setHash, 10)]; ok {
			for _, sp := range setDef.SetPerks {
				if sp.SandboxPerkHash != 0 {
					perkIDs = append(perkIDs, sp.SandboxPerkHash)
				}
			}
		}
	}
	return perkIDs
}

func (a *service) buildLoadout(ctx context.Context, membershipID int64, membershipType int64, items []bungie.ItemComponent, stats map[string]StatDefinition) (api.Loadout, []api.ArmorSetBonus, error) {
	damageTypes, err := a.ManifestService.GetDamageTypes(ctx)
	if err != nil {
		return nil, nil, err
	}

	itemSets := a.getEquipableItemSetsBestEffort(ctx)

	fetched, err := a.fetchEquippedItems(ctx, membershipID, membershipType, items)
	if err != nil {
		return nil, nil, err
	}

	itemIDs, perkIDs := collectLoadoutManifestIDs(fetched)

	d2Items, err := a.ManifestService.GetItemsByIDs(ctx, itemIDs)
	if err != nil {
		return nil, nil, err
	}

	setIdx := newItemSetIndex(itemSets)
	perkIDs = append(perkIDs, setIdx.perkIDsFor(fetched, d2Items)...)

	perks, err := a.ManifestService.GetPerksByIDs(ctx, perkIDs)
	if err != nil {
		return nil, nil, err
	}

	loadout := assembleLoadout(fetched, d2Items, damageTypes, perks, stats)
	armorSetBonuses := generateArmorSetBonuses(ctx, loadout, itemSets, perks)

	return loadout, armorSetBonuses, nil
}

func (a *service) getEquipableItemSetsBestEffort(ctx context.Context) map[string]EquipableItemSetDefinition {
	itemSets, err := a.ManifestService.GetEquipableItemSets(ctx)
	if err != nil {
		clog.WithGroupFields(ctx, "destinyService.buildLoadout", clog.Fields{
			"warning":       "failed to get equipable item sets",
			"itemSetsError": err.Error(),
		})
		return nil
	}
	return itemSets
}

const maxConcurrentItemFetches = 5

func (a *service) fetchEquippedItems(ctx context.Context, membershipID, membershipType int64, items []bungie.ItemComponent) ([]fetchedItem, error) {
	targets := make([]bungie.ItemComponent, 0, len(items))
	for _, item := range items {
		if item.ItemInstanceId != nil {
			targets = append(targets, item)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}

	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	fetched := make([]fetchedItem, len(targets))
	sem := make(chan struct{}, maxConcurrentItemFetches)

	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	recordErr := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancel()
		})
	}

	for i, item := range targets {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-fetchCtx.Done():
				return
			}

			instanceID := *item.ItemInstanceId
			detail, err := a.GetItemDetails(fetchCtx, membershipID, membershipType, instanceID)
			if err != nil {
				recordErr(fmt.Errorf("failed to fetch item details for instance %s: %w", instanceID, err))
				return
			}
			if detail == nil {
				recordErr(fmt.Errorf("empty item details for instance %s", instanceID))
				return
			}
			f := fetchedItem{
				instanceID: instanceID,
				detail:     detail,
			}
			f.applyFallbackOverrideStyle(item.OverrideStyleItemHash)
			fetched[i] = f
		})
	}
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return fetched, nil
}

func collectLoadoutManifestIDs(fetched []fetchedItem) (itemIDs, perkIDs []int64) {
	itemIDs = make([]int64, 0, len(fetched)*10)
	perkIDs = make([]int64, 0, len(fetched)*4)
	for _, f := range fetched {
		itemIDs = append(itemIDs, f.manifestItemIDs()...)
		perkIDs = append(perkIDs, f.perkIDs()...)
	}
	return itemIDs, perkIDs
}

func assembleLoadout(
	fetched []fetchedItem,
	d2Items map[string]ItemDefinition,
	damageTypes map[string]DamageType,
	perks map[string]PerkDefinition,
	stats map[string]StatDefinition,
) api.Loadout {
	loadout := make(api.Loadout, len(fetched))
	for _, f := range fetched {
		details := TransformItemToDetails(f.detail, d2Items, damageTypes, perks, stats)
		if details == nil {
			continue
		}
		snap := api.ItemSnapshot{
			InstanceID:     f.instanceID,
			Name:           details.BaseInfo.Name,
			ItemHash:       details.BaseInfo.ItemHash,
			ItemProperties: *details,
			BucketHash:     &details.BaseInfo.BucketHash,
		}
		loadout[inventory.BucketKey(snap.ItemProperties.BaseInfo.BucketHash)] = snap
	}
	return loadout
}
