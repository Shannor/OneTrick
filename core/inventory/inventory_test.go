package inventory

import (
	"math"
	"slices"
	"strconv"
	"testing"
)

func TestBucketConstantsReconcileUint32AndInt64(t *testing.T) {
	if HelmetArmor <= math.MaxInt32 || GauntletsArmor <= math.MaxInt32 {
		t.Fatalf("expected HelmetArmor and GauntletsArmor to exceed math.MaxInt32")
	}

	var u32Helmet uint32 = HelmetArmor
	var u32Gauntlets uint32 = GauntletsArmor
	var i64Helmet int64 = HelmetArmor
	var i64Gauntlets int64 = GauntletsArmor

	if int64(u32Helmet) != i64Helmet || int64(u32Gauntlets) != i64Gauntlets {
		t.Fatalf("uint32 and int64 representations diverged: helmet=(%d,%d) gauntlets=(%d,%d)",
			u32Helmet, i64Helmet, u32Gauntlets, i64Gauntlets)
	}
}

func TestBucketKeyAcrossIntegerTypes(t *testing.T) {
	cases := []struct {
		name string
		u32  uint32
		i64  int64
		want string
	}{
		{name: "kinetic", u32: Kinetic, i64: Kinetic, want: KineticKey},
		{name: "energy", u32: Energy, i64: Energy, want: EnergyKey},
		{name: "power", u32: Power, i64: Power, want: PowerKey},
		{name: "subclass", u32: SubClass, i64: SubClass, want: SubClassKey},
		{name: "helmet", u32: HelmetArmor, i64: HelmetArmor, want: HelmetArmorKey},
		{name: "gauntlets", u32: GauntletsArmor, i64: GauntletsArmor, want: GauntletsArmorKey},
		{name: "chest", u32: ChestArmor, i64: ChestArmor, want: ChestArmorKey},
		{name: "leg", u32: LegArmor, i64: LegArmor, want: LegArmorKey},
		{name: "class", u32: ClassArmor, i64: ClassArmor, want: ClassArmorKey},
		{name: "untracked_ghost_bucket", u32: 4023194814, i64: 4023194814, want: "4023194814"},
	}

	for _, tc := range cases {
		if got := BucketKey(tc.u32); got != tc.want {
			t.Errorf("%s uint32: BucketKey(%d) = %q, want %q", tc.name, tc.u32, got, tc.want)
		}
		if got := BucketKey(tc.i64); got != tc.want {
			t.Errorf("%s int64: BucketKey(%d) = %q, want %q", tc.name, tc.i64, got, tc.want)
		}
		if got := strconv.FormatUint(uint64(tc.u32), 10); got != tc.want {
			t.Errorf("%s key constant mismatch: got %q, want %q", tc.name, tc.want, got)
		}
	}

	if got := BucketKey(int64(-1)); got != "-1" {
		t.Errorf("negative int64: BucketKey(-1) = %q, want \"-1\"", got)
	}
}

func TestArmorBucketKeysOrderAndImmutability(t *testing.T) {
	want := []string{
		HelmetArmorKey,
		GauntletsArmorKey,
		ChestArmorKey,
		LegArmorKey,
		ClassArmorKey,
	}
	got := ArmorBucketKeys()
	if !slices.Equal(got, want) {
		t.Fatalf("ArmorBucketKeys() = %v, want %v", got, want)
	}

	got[0] = "mutated"
	if fresh := ArmorBucketKeys(); !slices.Equal(fresh, want) {
		t.Fatalf("mutating returned slice corrupted subsequent ArmorBucketKeys(): %v", fresh)
	}
}

func TestSlotClassificationHelpers(t *testing.T) {
	weapons := []uint32{Kinetic, Energy, Power}
	for _, b := range weapons {
		if !IsWeaponBucket(b) || !IsWeaponBucket(int64(b)) {
			t.Errorf("expected %d to be weapon bucket", b)
		}
		if IsArmorBucket(b) {
			t.Errorf("did not expect weapon bucket %d to be armor bucket", b)
		}
		if !IsEquippedLoadoutBucket(b) {
			t.Errorf("expected weapon bucket %d to be equipped loadout bucket", b)
		}
	}

	armors := []uint32{HelmetArmor, GauntletsArmor, ChestArmor, LegArmor, ClassArmor}
	for _, b := range armors {
		if !IsArmorBucket(b) || !IsArmorBucket(int64(b)) {
			t.Errorf("expected %d to be armor bucket", b)
		}
		if IsWeaponBucket(b) {
			t.Errorf("did not expect armor bucket %d to be weapon bucket", b)
		}
		if !IsEquippedLoadoutBucket(b) {
			t.Errorf("expected armor bucket %d to be equipped loadout bucket", b)
		}
	}

	if IsWeaponBucket(SubClass) || IsArmorBucket(SubClass) || !IsEquippedLoadoutBucket(SubClass) {
		t.Errorf("unexpected classification for SubClass (%d)", SubClass)
	}

	const ghostBucket uint32 = 4023194814
	if IsWeaponBucket(ghostBucket) || IsArmorBucket(ghostBucket) || IsEquippedLoadoutBucket(ghostBucket) {
		t.Errorf("did not expect Ghost bucket (%d) to match tracked loadout buckets", ghostBucket)
	}

	if IsWeaponBucket(-1) || IsArmorBucket(-1) || IsEquippedLoadoutBucket(-1) {
		t.Errorf("negative bucket hash should never match")
	}
}
