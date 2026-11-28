package firestore

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func TestManifestDocConversions(t *testing.T) {
	setHash := int64(500001)
	item := itemDefinitionDoc{
		DisplayProperties: manifestDisplayDoc{
			Name:        "Igneous Hammer (Adept)",
			Description: "Hand Cannon",
			Icon:        "/common/destiny2_content/icons/igneous.jpg",
			HasIcon:     true,
		},
		Inventory: itemInventoryDoc{
			TierTypeName: "Legendary",
			TierType:     5,
		},
		EquippingBlock: itemEquippingBlockDoc{
			EquipmentSlotTypeHash: 2465295065,
			EquipableItemSetHash:  &setHash,
		},
		ItemTypeDisplayName:        "Hand Cannon",
		ItemTypeAndTierDisplayName: "Legendary Hand Cannon",
	}.toDomain(12345)

	if item.Hash != 12345 {
		t.Fatalf("expected fallback hash 12345, got %d", item.Hash)
	}
	if item.BucketHash != 2465295065 {
		t.Fatalf("expected EquipmentSlotTypeHash fallback 2465295065, got %d", item.BucketHash)
	}
	if item.IconURL != "https://www.bungie.net/common/destiny2_content/icons/igneous.jpg" {
		t.Fatalf("unexpected IconURL %q", item.IconURL)
	}
	if item.EquipableItemSetHash == nil || *item.EquipableItemSetHash != setHash {
		t.Fatalf("expected EquipableItemSetHash %d, got %v", setHash, item.EquipableItemSetHash)
	}

	race := raceDefinitionDoc{
		Hash:              3887404748,
		RaceType:          2,
		DisplayProperties: manifestDisplayDoc{Name: "Exo"},
	}.toDomain(0)
	if race.Hash != 3887404748 || race.Name != "Exo" {
		t.Fatalf("unexpected race: %+v", race)
	}

	dmg := damageTypeDoc{
		Hash:                3373582085,
		DisplayProperties:   manifestDisplayDoc{Name: "Kinetic", Icon: "/icons/kinetic.png"},
		TransparentIconPath: "/icons/kinetic_trans.png",
		Color:               damageColorDoc{Red: 255, Green: 255, Blue: 255, Alpha: 255},
	}.toDomain(0)
	if dmg.Icon != "/icons/kinetic.png" || dmg.TransparentIconPath != "/icons/kinetic_trans.png" {
		t.Fatalf("expected raw damage icon paths preserved, got %+v", dmg)
	}
}

func TestFirestoreEmulatorManifestStore(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator integration test")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, fmt.Sprintf("manifest-store-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("create emulator firestore client: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewManifestStore(db)

	// Seed 35 items to exercise batched chunking (> 30) plus NotFound handling.
	itemIDs := make([]int64, 0, 36)
	for i := int64(1); i <= 35; i++ {
		hash := 900000 + i
		itemIDs = append(itemIDs, hash)
		_, err := db.Collection(itemDefinitionsCollection).Doc(strconv.FormatInt(hash, 10)).Set(ctx, map[string]any{
			"hash": hash,
			"displayProperties": map[string]any{
				"name":    fmt.Sprintf("Weapon %d", i),
				"icon":    fmt.Sprintf("/icons/w%d.png", i),
				"hasIcon": true,
			},
			"inventory": map[string]any{
				"bucketTypeHash": int64(1498876634),
				"tierTypeName":   "Exotic",
				"tierType":       6,
			},
		})
		if err != nil {
			t.Fatalf("seed item %d: %v", hash, err)
		}
	}
	itemIDs = append(itemIDs, 999999) // missing hash

	gotItems, err := store.GetItemsByIDs(ctx, itemIDs)
	if err != nil {
		t.Fatalf("GetItemsByIDs failed: %v", err)
	}
	if len(gotItems) != 35 {
		t.Fatalf("expected 35 items, got %d", len(gotItems))
	}
	if gotItems[900001].IconURL != "https://www.bungie.net/icons/w1.png" {
		t.Fatalf("unexpected IconURL: %q", gotItems[900001].IconURL)
	}

	_, ok, err := store.GetItem(ctx, 999999)
	if err != nil || ok {
		t.Fatalf("expected NotFound -> (ok=false, err=nil), got ok=%v err=%v", ok, err)
	}

	_, err = db.Collection(activityModesCollection).Doc("84").Set(ctx, map[string]any{
		"hash":     int64(84),
		"modeType": 84,
		"displayProperties": map[string]any{
			"name": "Trials of Osiris",
			"icon": "/icons/trials.png",
		},
	})
	if err != nil {
		t.Fatalf("seed mode: %v", err)
	}

	mode, ok, err := store.GetActivityMode(ctx, 9999999, 84)
	if err != nil || !ok || mode.Name != "Trials of Osiris" {
		t.Fatalf("expected modeType=84 fallback to resolve Trials of Osiris, got %+v ok=%v err=%v", mode, ok, err)
	}
}
