package bungie

import (
	"cmp"
	"context"
	"fmt"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"oneTrick/core/inventory"
	"oneTrick/ptr"
	"slices"
	"strconv"
	"sync"

	clog "github.com/Shannor/canonical-logger"
)

const maxConcurrentItemFetches = 5

type LoadoutManifestReader interface {
	GetStats(ctx context.Context) (map[int64]domain.StatManifest, error)
	GetDamageTypes(ctx context.Context) (map[int64]domain.DamageTypeManifest, error)
	GetEquipableItemSets(ctx context.Context) (map[int64]domain.EquipableItemSetManifest, error)
	GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]domain.ItemManifest, error)
	GetPerksByIDs(ctx context.Context, ids []int64) (map[int64]domain.PerkManifest, error)
}

type LiveLoadoutGateway struct {
	client   bungie.ClientWithResponsesInterface
	manifest LoadoutManifestReader
}

func NewLiveLoadoutGateway(
	client bungie.ClientWithResponsesInterface,
	manifest LoadoutManifestReader,
) *LiveLoadoutGateway {
	return &LiveLoadoutGateway{
		client:   client,
		manifest: manifest,
	}
}

func (g *LiveLoadoutGateway) GetLiveLoadout(
	ctx context.Context,
	membershipID int64,
	membershipType int64,
	characterID string,
) (domain.LiveLoadout, error) {
	components := []int32{componentCharacterEquipment, componentCharacters}
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	resp, err := g.client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), membershipID, params)
	if err != nil {
		return domain.LiveLoadout{}, err
	}
	if resp == nil {
		return domain.LiveLoadout{}, fmt.Errorf("no response found")
	}

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieLoadout.getLiveLoadout", resp.StatusCode(), resp.Body, env); err != nil {
		return domain.LiveLoadout{}, err
	}
	if resp.JSON200 == nil || resp.JSON200.Response == nil {
		return domain.LiveLoadout{}, fmt.Errorf("no response found")
	}

	profile := resp.JSON200.Response
	var equipped []bungie.ItemComponent
	if profile.CharacterEquipment != nil && profile.CharacterEquipment.Data != nil {
		if charEquip, ok := (*profile.CharacterEquipment.Data)[characterID]; ok && charEquip.Items != nil {
			for _, item := range *charEquip.Items {
				if item.BucketHash != nil && inventory.IsEquippedLoadoutBucket(*item.BucketHash) {
					equipped = append(equipped, item)
				}
			}
		}
	}

	clog.WithGroupFields(ctx, "bungieLoadout.getLiveLoadout", clog.Fields{
		"membershipId":   membershipID,
		"membershipType": membershipType,
		"characterId":    characterID,
	})

	statDefs, err := g.manifest.GetStats(ctx)
	if err != nil {
		clog.WithGroupField(ctx, "bungieLoadout.getLiveLoadout", "warning", "failed to get stat definitions")
	}

	stats := make(map[string]domain.ClassStat)
	if profile.Characters != nil && profile.Characters.Data != nil {
		if charComp, ok := (*profile.Characters.Data)[characterID]; ok && charComp.Stats != nil {
			stats = generateClassStats(statDefs, *charComp.Stats)
		}
	}

	loadout, armorSetBonuses, err := g.buildLoadout(ctx, membershipID, membershipType, equipped, statDefs)
	if err != nil {
		return domain.LiveLoadout{}, err
	}

	clog.WithGroupFields(ctx, "bungieLoadout.getLiveLoadout", clog.Fields{
		"itemCount":          len(loadout),
		"armorSetBonusCount": len(armorSetBonuses),
	})

	return domain.LiveLoadout{
		Loadout:         loadout,
		Stats:           stats,
		ArmorSetBonuses: armorSetBonuses,
		MintedAt:        profile.ResponseMintedTimestamp,
	}, nil
}

func (g *LiveLoadoutGateway) getItemDetails(
	ctx context.Context,
	membershipID int64,
	membershipType int64,
	itemInstanceID string,
) (*bungie.DestinyItem, error) {
	clog.WithGroupFields(ctx, "bungieLoadout.getItemDetails", clog.Fields{
		"membershipId":     membershipID,
		"membershipType":   membershipType,
		"weaponInstanceId": itemInstanceID,
	})
	instanceInt64, err := strconv.ParseInt(itemInstanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid item instance id %q: %w", itemInstanceID, err)
	}
	components := []int32{
		componentItemPerks,
		componentItemStats,
		componentItemSockets,
		componentItemCommonData,
		componentItemInstances,
	}
	params := &bungie.Destiny2GetItemParams{Components: &components}
	resp, err := g.client.Destiny2GetItemWithResponse(ctx, int32(membershipType), membershipID, instanceInt64, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieLoadout.getItemDetails", resp.StatusCode(), resp.Body, env); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.DestinyItem == nil {
		return nil, nil
	}
	return resp.JSON200.DestinyItem, nil
}

type fetchedItem struct {
	instanceID                string
	detail                    *bungie.DestinyItem
	fallbackOverrideStyleHash uint32
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

// effectiveOverrideStyleHash resolves the active ornament/style hash from the detailed
// item component or falls back to the character-equipment summary hash without mutating
// the underlying Bungie response pointer.
func (f fetchedItem) effectiveOverrideStyleHash() (int64, bool) {
	if c := f.component(); c != nil && c.OverrideStyleItemHash != nil && *c.OverrideStyleItemHash != 0 {
		return int64(*c.OverrideStyleItemHash), true
	}
	if f.fallbackOverrideStyleHash != 0 {
		return int64(f.fallbackOverrideStyleHash), true
	}
	return 0, false
}

func (f fetchedItem) manifestItemIDs() []int64 {
	var ids []int64
	if hash, ok := f.itemHash(); ok {
		ids = append(ids, hash)
	}
	if styleHash, ok := f.effectiveOverrideStyleHash(); ok {
		ids = append(ids, styleHash)
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

func (g *LiveLoadoutGateway) fetchEquippedItems(
	ctx context.Context,
	membershipID int64,
	membershipType int64,
	items []bungie.ItemComponent,
) ([]fetchedItem, error) {
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
			detail, err := g.getItemDetails(fetchCtx, membershipID, membershipType, instanceID)
			if err != nil {
				recordErr(fmt.Errorf("failed to fetch item details for instance %s: %w", instanceID, err))
				return
			}
			if detail == nil {
				recordErr(fmt.Errorf("empty item details for instance %s", instanceID))
				return
			}
			var fallbackStyle uint32
			if item.OverrideStyleItemHash != nil {
				fallbackStyle = *item.OverrideStyleItemHash
			}
			fetched[i] = fetchedItem{
				instanceID:                instanceID,
				detail:                    detail,
				fallbackOverrideStyleHash: fallbackStyle,
			}
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

type itemSetIndex struct {
	sets          map[int64]domain.EquipableItemSetManifest
	itemToSetHash map[int64]int64
}

func newItemSetIndex(sets map[int64]domain.EquipableItemSetManifest) itemSetIndex {
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

func (idx itemSetIndex) resolveSetHash(itemHash int64, d2Items map[int64]domain.ItemManifest) (int64, bool) {
	if setHash, ok := idx.itemToSetHash[itemHash]; ok {
		return setHash, true
	}
	if def, ok := d2Items[itemHash]; ok && def.EquipableItemSetHash != nil {
		setHash := *def.EquipableItemSetHash
		return setHash, setHash != 0
	}
	return 0, false
}

func (idx itemSetIndex) perkIDsFor(fetched []fetchedItem, d2Items map[int64]domain.ItemManifest) []int64 {
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
		if setDef, ok := idx.sets[setHash]; ok {
			for _, sp := range setDef.SetPerks {
				if sp.SandboxPerkHash != 0 {
					perkIDs = append(perkIDs, sp.SandboxPerkHash)
				}
			}
		}
	}
	return perkIDs
}

func (g *LiveLoadoutGateway) buildLoadout(
	ctx context.Context,
	membershipID int64,
	membershipType int64,
	items []bungie.ItemComponent,
	stats map[int64]domain.StatManifest,
) (domain.Loadout, []domain.ArmorSetBonus, error) {
	damageTypes, err := g.manifest.GetDamageTypes(ctx)
	if err != nil {
		return nil, nil, err
	}

	itemSets := g.getEquipableItemSetsBestEffort(ctx)

	fetched, err := g.fetchEquippedItems(ctx, membershipID, membershipType, items)
	if err != nil {
		return nil, nil, err
	}

	itemIDs, perkIDs := collectLoadoutManifestIDs(fetched)

	d2Items, err := g.manifest.GetItemsByIDs(ctx, itemIDs)
	if err != nil {
		return nil, nil, err
	}

	setIdx := newItemSetIndex(itemSets)
	perkIDs = append(perkIDs, setIdx.perkIDsFor(fetched, d2Items)...)

	perks, err := g.manifest.GetPerksByIDs(ctx, perkIDs)
	if err != nil {
		return nil, nil, err
	}

	loadout := assembleLoadout(fetched, d2Items, damageTypes, perks, stats)
	armorSetBonuses := generateArmorSetBonuses(ctx, loadout, setIdx, perks)
	return loadout, armorSetBonuses, nil
}

func (g *LiveLoadoutGateway) getEquipableItemSetsBestEffort(ctx context.Context) map[int64]domain.EquipableItemSetManifest {
	itemSets, err := g.manifest.GetEquipableItemSets(ctx)
	if err != nil {
		clog.WithGroupFields(ctx, "bungieLoadout.buildLoadout", clog.Fields{
			"warning":       "failed to get equipable item sets",
			"itemSetsError": err.Error(),
		})
		return nil
	}
	return itemSets
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
	d2Items map[int64]domain.ItemManifest,
	damageTypes map[int64]domain.DamageTypeManifest,
	perks map[int64]domain.PerkManifest,
	stats map[int64]domain.StatManifest,
) domain.Loadout {
	loadout := make(domain.Loadout, len(fetched))
	for _, f := range fetched {
		details, ok := transformItemToDetails(f, d2Items, damageTypes, perks, stats)
		if !ok {
			continue
		}
		bucketHash := details.BaseInfo.BucketHash
		snap := domain.ItemSnapshot{
			InstanceID: f.instanceID,
			Name:       details.BaseInfo.Name,
			ItemHash:   details.BaseInfo.ItemHash,
			BucketHash: &bucketHash,
			Details:    details,
		}
		loadout[inventory.BucketKey(bucketHash)] = snap
	}
	return loadout
}

func transformItemToDetails(
	f fetchedItem,
	items map[int64]domain.ItemManifest,
	damages map[int64]domain.DamageTypeManifest,
	perks map[int64]domain.PerkManifest,
	stats map[int64]domain.StatManifest,
) (domain.ItemProperties, bool) {
	item := f.detail
	if item == nil {
		return domain.ItemProperties{}, false
	}
	result := domain.ItemProperties{
		CharacterID: ptr.Deref(item.CharacterId),
	}
	if item.Item != nil {
		result.BaseInfo = generateBaseInfo(f, items, damages)
	}
	if item.Perks != nil && item.Perks.Data != nil {
		result.Perks = generatePerks(item, perks)
	}
	if item.Sockets != nil && item.Sockets.Data != nil {
		result.HasSockets = true
		result.Sockets = generateSockets(item, items)
	}
	if item.Stats != nil && item.Stats.Data != nil {
		result.Stats = generateGunStats(item, stats)
	}
	return result, true
}

func generateBaseInfo(
	f fetchedItem,
	items map[int64]domain.ItemManifest,
	damages map[int64]domain.DamageTypeManifest,
) domain.BaseItemInfo {
	c := f.component()
	if c == nil || c.ItemHash == nil || c.BucketHash == nil || c.ItemInstanceId == nil {
		return domain.BaseItemInfo{}
	}
	hash := int64(*c.ItemHash)
	def := items[hash]

	base := domain.BaseItemInfo{
		BucketHash:                 int64(*c.BucketHash),
		InstanceID:                 *c.ItemInstanceId,
		ItemHash:                   hash,
		Name:                       def.Name,
		Icon:                       formatBungieURL(def.IconURL),
		ItemTypeAndTierDisplayName: def.ItemTypeAndTierDisplayName,
		ItemTypeDisplayName:        def.ItemTypeDisplayName,
		TierTypeName:               def.TierTypeName,
		TierType:                   def.TierType,
		EquipableItemSetHash:       def.EquipableItemSetHash,
	}

	if styleHash, ok := f.effectiveOverrideStyleHash(); ok {
		if styleDef, found := items[styleHash]; found {
			base.StyleBasicInfo = &domain.BaseItemInfo{
				BucketHash:                 int64(*c.BucketHash),
				InstanceID:                 *c.ItemInstanceId,
				ItemHash:                   styleDef.Hash,
				Name:                       styleDef.Name,
				Icon:                       formatBungieURL(styleDef.IconURL),
				ItemTypeAndTierDisplayName: styleDef.ItemTypeAndTierDisplayName,
				ItemTypeDisplayName:        styleDef.ItemTypeDisplayName,
				TierTypeName:               styleDef.TierTypeName,
				TierType:                   styleDef.TierType,
			}
		}
	}

	if f.detail != nil && f.detail.Instance != nil && f.detail.Instance.ItemInstanceComponent != nil {
		instance := f.detail.Instance.ItemInstanceComponent
		if instance.DamageTypeHash != nil {
			if dmgDef, ok := damages[int64(*instance.DamageTypeHash)]; ok {
				base.Damage = &domain.DamageInfo{
					DamageType:      dmgDef.Name,
					DamageIcon:      dmgDef.Icon,
					TransparentIcon: dmgDef.TransparentIconPath,
					Color:           dmgDef.Color,
				}
			}
		}
	}
	return base
}

func generatePerks(item *bungie.DestinyItem, perks map[int64]domain.PerkManifest) []domain.Perk {
	if item == nil || item.Perks == nil || item.Perks.Data == nil || item.Perks.Data.Perks == nil {
		return nil
	}
	raw := *item.Perks.Data.Perks
	results := make([]domain.Perk, 0, len(raw))
	for _, p := range raw {
		if p.PerkHash == nil {
			continue
		}
		perkDef, ok := perks[int64(*p.PerkHash)]
		if !ok || !perkDef.IsDisplayable {
			continue
		}
		results = append(results, domain.Perk{
			Hash:        int64(*p.PerkHash),
			IconPath:    formatBungieOptionalURL(p.IconPath),
			Name:        perkDef.Name,
			Description: perkDef.Description,
		})
	}
	return results
}

func generateSockets(item *bungie.DestinyItem, items map[int64]domain.ItemManifest) []domain.Socket {
	if item == nil || item.Sockets == nil || item.Sockets.Data == nil || item.Sockets.Data.Sockets == nil {
		return nil
	}
	raw := *item.Sockets.Data.Sockets
	sockets := make([]domain.Socket, 0, len(raw))
	for _, s := range raw {
		if s.PlugHash == nil {
			continue
		}
		plugDef, ok := items[int64(*s.PlugHash)]
		if !ok {
			continue
		}
		sockets = append(sockets, domain.Socket{
			PlugHash:                  int(*s.PlugHash),
			IsEnabled:                 s.IsEnabled,
			IsVisible:                 s.IsVisible,
			Name:                      plugDef.Name,
			Description:               plugDef.Description,
			Icon:                      formatBungieURL(plugDef.IconURL),
			ItemTypeDisplayName:       plugDef.ItemTypeDisplayName,
			ItemTypeTieredDisplayName: plugDef.ItemTypeAndTierDisplayName,
		})
	}
	return sockets
}

func generateGunStats(item *bungie.DestinyItem, statDefs map[int64]domain.StatManifest) map[string]domain.GunStat {
	stats := make(map[string]domain.GunStat)
	if item == nil || item.Stats == nil || item.Stats.Data == nil || item.Stats.Data.Stats == nil {
		return stats
	}
	for key, s := range *item.Stats.Data.Stats {
		if s.StatHash == nil || s.Value == nil {
			continue
		}
		statDef, ok := statDefs[int64(*s.StatHash)]
		if !ok {
			continue
		}
		stats[key] = domain.GunStat{
			Hash:        statDef.Hash,
			Value:       int64(*s.Value),
			Name:        statDef.Name,
			Description: statDef.Description,
		}
	}
	return stats
}

func generateClassStats(statDefs map[int64]domain.StatManifest, rawStats map[string]int32) map[string]domain.ClassStat {
	if len(statDefs) == 0 {
		return nil
	}
	results := make(map[string]domain.ClassStat, len(rawStats))
	for key, value := range rawStats {
		hash, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			continue
		}
		info, ok := statDefs[hash]
		if !ok {
			continue
		}
		results[key] = domain.ClassStat{
			Name:            info.Name,
			Description:     info.Description,
			Icon:            formatBungieURL(info.IconURL),
			HasIcon:         info.HasIcon,
			StatCategory:    info.StatCategory,
			AggregationType: info.AggregationType,
			Value:           value,
		}
	}
	return results
}

func generateArmorSetBonuses(
	ctx context.Context,
	loadout domain.Loadout,
	setIdx itemSetIndex,
	perks map[int64]domain.PerkManifest,
) []domain.ArmorSetBonus {
	if len(loadout) == 0 || len(setIdx.sets) == 0 {
		return nil
	}

	setCounts := make(map[int64]int)
	for _, bucketKey := range inventory.ArmorBucketKeys() {
		itemSnap, ok := loadout[bucketKey]
		if !ok {
			continue
		}
		setHash, found := setIdx.itemToSetHash[itemSnap.ItemHash]
		if !found && itemSnap.Details.BaseInfo.EquipableItemSetHash != nil {
			setHash = *itemSnap.Details.BaseInfo.EquipableItemSetHash
			found = setHash != 0
		}
		if found {
			setCounts[setHash]++
		}
	}

	if len(setCounts) == 0 {
		return nil
	}

	var results []domain.ArmorSetBonus
	for setHash, equippedCount := range setCounts {
		setDef, ok := setIdx.sets[setHash]
		if !ok {
			continue
		}
		var activePerks []domain.ArmorSetBonusPerk
		for _, setPerk := range setDef.SetPerks {
			if setPerk.RequiredSetCount <= 0 || equippedCount < setPerk.RequiredSetCount {
				continue
			}
			perkDef, ok := perks[setPerk.SandboxPerkHash]
			if !ok {
				clog.WithGroupFields(ctx, "bungieLoadout.generateArmorSetBonuses", clog.Fields{
					"warning":  "missing set bonus perk in manifest",
					"setHash":  setHash,
					"perkHash": setPerk.SandboxPerkHash,
				})
				continue
			}
			activePerks = append(activePerks, domain.ArmorSetBonusPerk{
				RequiredSetCount: setPerk.RequiredSetCount,
				Hash:             setPerk.SandboxPerkHash,
				Name:             perkDef.Name,
				Description:      perkDef.Description,
				IconPath:         formatBungieURL(perkDef.IconURL),
			})
		}
		if len(activePerks) == 0 {
			continue
		}
		slices.SortFunc(activePerks, func(a, b domain.ArmorSetBonusPerk) int {
			if c := cmp.Compare(a.RequiredSetCount, b.RequiredSetCount); c != 0 {
				return c
			}
			return cmp.Compare(a.Hash, b.Hash)
		})
		results = append(results, domain.ArmorSetBonus{
			SetHash:     setDef.Hash,
			SetName:     setDef.Name,
			ActiveCount: equippedCount,
			ActivePerks: activePerks,
		})
	}

	slices.SortFunc(results, func(a, b domain.ArmorSetBonus) int {
		return cmp.Compare(a.SetHash, b.SetHash)
	})
	return results
}
