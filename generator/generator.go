package generator

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// D2Name generates a Destiny 2 themed name by combining randomly selected elements.
func D2Name() string {
	// Funny prefix/suffix collections
	prefixes := []string{
		"Xur'", "Zur'", "Yeet'", "Boop'", "Nyoom'",
		"Zoop'", "Vex'", "Bonk'", "Squish'", "Pew'",
		"Thwap'", "Kzzt'", "Brrr'", "Swoosh'", "Zip'",
		"Fwoop'", "Thunk'", "Blam'", "Zzzt'", "Splorch'",
	}

	suffixes := []string{
		"'thul", "'pok", "'zoop", "'boop", "'yeet",
		"'splat", "'zonk", "'thonk", "'bork", "'derp",
		"'zork", "'blam", "'kthx", "'yoink", "'zoom",
		"'zing", "'zang", "'whoosh", "'bonk", "'zap",
	}

	adjectives := []string{
		"Brave", "Fierce", "Shadowed", "Burning", "Silent",
		"Radiant", "Ascendant", "Taken", "Corrupted", "Awoken",
		"Shattered", "Fallen", "Exiled", "Risen", "Haunted",
		"Luminous", "Transcendent", "Stasis-bound", "Darkness-touched", "Light-forged",
		"Eternal", "Unstoppable", "Celestial", "Vanguard", "Forgotten",
		"Void-marked", "Solar-blessed", "Arc-charged", "Paracausal", "Godslaying",
	}
	nouns := []string{
		"Guardian", "Warlock", "Hunter", "Titan", "Lightbearer",
		"Redjack", "Corsair", "Techeun", "Sentinel", "Dredgen",
		"Harbinger", "Iron Lord", "Wayfarer", "Chronicler", "Savior",
		"Revenant", "Behemoth", "Shadebinder", "Nightstalker", "Sunbreaker",
		"Stormcaller", "Voidwalker", "Gunslinger", "Defender", "Seraph",
		"Witness", "Disciple", "Warden", "Champion", "Arbalest",
	}

	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	// Randomly decide if we should add a prefix or suffix to either the adjective or noun
	usePrefix := r.Float64() < 0.3        // 30% chance for prefix
	useSuffix := r.Float64() < 0.3        // 30% chance for suffix
	applyToAdjective := r.Float64() < 0.5 // 50% chance for either word

	adj := adjectives[r.Intn(len(adjectives))]
	noun := nouns[r.Intn(len(nouns))]
	// Apply prefix/suffix modifications
	if usePrefix {
		prefix := prefixes[r.Intn(len(prefixes))]
		if applyToAdjective {
			adj = prefix + adj
		} else {
			noun = prefix + noun
		}
	}

	if useSuffix {
		suffix := suffixes[r.Intn(len(suffixes))]
		if applyToAdjective {
			adj = adj + suffix
		} else {
			noun = noun + suffix
		}
	}

	return fmt.Sprintf("%s %s", adj, noun)
}

// PVPName generates a Destiny 2 PvP loadout name with meme flair
func PVPName() string {
	// PvP build types and playstyles
	buildTypes := []string{
		"Aggressive", "Defensive", "Rush", "Anchor", "Precision",
		"Flanking", "Support", "Slayer", "Lockdown", "Roaming",
		"Shutdown", "Denial", "Counter", "Zone", "Reactive",
		"Passive", "Punish", "Pressure", "Control", "Tempo",
	}

	// PvP playstyle descriptors
	playstyles := []string{
		"Silent", "Swift", "Methodical", "Calculated", "Ruthless",
		"Tactical", "Coordinated", "Disciplined", "Unpredictable", "Patient",
		"Aggressive", "Precise", "Rapid", "Strategic", "Dominant",
		"Disruptive", "Elusive", "Mobile", "Defensive", "Persistent",
	}

	// Destiny 2 PvP meme terms
	memeTerms := []string{
		"Main Character", "Touch Grass", "Crayon Eater", "Monkey Brain", "W Key",
		"Skill Issue", "Tilt Proof", "Keyboard Warrior", "Chair Camper", "Sweat Lord",
		"Tryhard Andy", "No Life", "Dad Build", "Bot Lobby", "Grief Master",
		"Rage Quit", "Skill Gap", "Gamer Mode", "Touched Solar", "Zero Chill",
	}

	// PvP-focused prefixes
	prefixes := []string{
		"Sweat'", "Flawless'", "Focus'", "Speed'", "Clutch'",
		"React'", "Sharp'", "Quick'", "Tactical'", "Primed'",
		"Elite'", "Pro'", "Peak'", "Optimal'", "Perfect'",
	}

	// Create a new random number generator
	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Random chance modifiers
	usePrefix := r.Float64() < 0.4    // 40% chance
	usePlaystyle := r.Float64() < 0.7 // 70% chance
	useMeme := r.Float64() < 0.6      // 60% chance

	// Build the name
	var parts []string

	if usePrefix {
		prefix := prefixes[r.Intn(len(prefixes))]
		parts = append(parts, prefix)
	}

	// Always include a build type
	buildType := buildTypes[r.Intn(len(buildTypes))]
	parts = append(parts, buildType)

	if usePlaystyle {
		playstyle := playstyles[r.Intn(len(playstyles))]
		parts = append(parts, playstyle)
	}

	if useMeme {
		meme := memeTerms[r.Intn(len(memeTerms))]
		parts = append(parts, meme)
	}

	result := strings.Join(parts, " ")

	// Clean up double spaces
	result = strings.ReplaceAll(result, "  ", " ")
	return result
}
