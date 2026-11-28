package inventory

import "strconv"

// Hash constrains integer types used to represent Destiny 2 inventory bucket hashes
// across Bungie wire payloads (uint32) and domain/manifest models (int64, int).
// Note that HelmetArmor (3448274439) and GauntletsArmor (3551918588) exceed math.MaxInt32.
type Hash interface {
	~int | ~int64 | ~uint32 | ~uint64
}

const (
	Kinetic  = 1498876634
	Energy   = 2465295065
	Power    = 953998645
	SubClass = 3284755031

	HelmetArmor    = 3448274439
	GauntletsArmor = 3551918588
	ChestArmor     = 14239492
	LegArmor       = 20886954
	ClassArmor     = 1585787867
)

const (
	KineticKey  = "1498876634"
	EnergyKey   = "2465295065"
	PowerKey    = "953998645"
	SubClassKey = "3284755031"

	HelmetArmorKey    = "3448274439"
	GauntletsArmorKey = "3551918588"
	ChestArmorKey     = "14239492"
	LegArmorKey       = "20886954"
	ClassArmorKey     = "1585787867"
)

// BucketKey formats a Destiny inventory bucket hash as its canonical decimal loadout map key.
func BucketKey[T Hash](bucket T) string {
	if bucket < 0 {
		return strconv.FormatInt(int64(bucket), 10)
	}
	switch uint64(bucket) {
	case Kinetic:
		return KineticKey
	case Energy:
		return EnergyKey
	case Power:
		return PowerKey
	case SubClass:
		return SubClassKey
	case HelmetArmor:
		return HelmetArmorKey
	case GauntletsArmor:
		return GauntletsArmorKey
	case ChestArmor:
		return ChestArmorKey
	case LegArmor:
		return LegArmorKey
	case ClassArmor:
		return ClassArmorKey
	default:
		return strconv.FormatUint(uint64(bucket), 10)
	}
}

// ArmorBucketKeys returns the five equipped armor bucket map keys in canonical slot order.
func ArmorBucketKeys() []string {
	return []string{
		HelmetArmorKey,
		GauntletsArmorKey,
		ChestArmorKey,
		LegArmorKey,
		ClassArmorKey,
	}
}

func IsWeaponBucket[T Hash](bucket T) bool {
	if bucket < 0 {
		return false
	}
	switch uint64(bucket) {
	case Kinetic, Energy, Power:
		return true
	default:
		return false
	}
}

func IsArmorBucket[T Hash](bucket T) bool {
	if bucket < 0 {
		return false
	}
	switch uint64(bucket) {
	case HelmetArmor, GauntletsArmor, ChestArmor, LegArmor, ClassArmor:
		return true
	default:
		return false
	}
}

func IsEquippedLoadoutBucket[T Hash](bucket T) bool {
	if bucket < 0 {
		return false
	}
	switch uint64(bucket) {
	case Kinetic, Energy, Power, SubClass,
		HelmetArmor, GauntletsArmor, ChestArmor, LegArmor, ClassArmor:
		return true
	default:
		return false
	}
}
