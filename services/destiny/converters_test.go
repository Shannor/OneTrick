package destiny

import (
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"testing"
)

func TestTransformCharacter_NoTitle(t *testing.T) {
	charID := "2305843009265000000"
	classHash := uint32(671618624)
	raceHash := uint32(898834093)
	light := int32(2000)

	alpha := 255
	red := 100
	green := 150
	blue := 200

	bgPath := "/common/destiny2_content/icons/bg.jpg"
	emPath := "/common/destiny2_content/icons/em.jpg"

	component := &bungie.CharacterComponent{
		CharacterId:          &charID,
		ClassHash:            &classHash,
		RaceHash:             &raceHash,
		Light:                &light,
		TitleRecordHash:      nil, // No title equipped
		EmblemBackgroundPath: &bgPath,
		EmblemPath:           &emPath,
		EmblemColor: &bungie.DestinyMiscDestinyColor{
			Alpha: &alpha,
			Red:   &red,
			Green: &green,
			Blue:  &blue,
		},
	}

	classes := map[string]ClassDefinition{
		"671618624": {
			DisplayProperties: ClassDisplayProperties{Name: "Titan"},
		},
	}

	races := map[string]RaceDefinition{
		"898834093": {
			DisplayProperties: RaceDisplayProperties{Name: "Human"},
		},
	}

	records := map[string]RecordDefinition{}

	char := TransformCharacter(component, classes, races, records)

	if char.Id != charID {
		t.Errorf("expected char.Id %s, got %s", charID, char.Id)
	}
	if char.Class != "Titan" {
		t.Errorf("expected Class Titan, got %s", char.Class)
	}
	if char.Race != "Human" {
		t.Errorf("expected Race Human, got %s", char.Race)
	}
	if char.Light != 2000 {
		t.Errorf("expected Light 2000, got %d", char.Light)
	}
	if char.CurrentTitle != "" {
		t.Errorf("expected empty CurrentTitle, got %s", char.CurrentTitle)
	}
	if char.EmblemColor.Alpha != 255 || char.EmblemColor.Red != 100 {
		t.Errorf("expected emblem color Alpha 255, Red 100, got %+v", char.EmblemColor)
	}
}

func TestTransformCharacter_WithTitle(t *testing.T) {
	charID := "2305843009265000001"
	classHash := uint32(671618624)
	raceHash := uint32(898834093)
	light := int32(2000)
	titleHash := uint32(123456)

	component := &bungie.CharacterComponent{
		CharacterId:     &charID,
		ClassHash:       &classHash,
		RaceHash:        &raceHash,
		Light:           &light,
		TitleRecordHash: &titleHash,
	}

	classes := map[string]ClassDefinition{
		"671618624": {
			DisplayProperties: ClassDisplayProperties{Name: "Titan"},
		},
	}
	races := map[string]RaceDefinition{
		"898834093": {
			DisplayProperties: RaceDisplayProperties{Name: "Human"},
		},
	}

	record := RecordDefinition{}
	record.TitleInfo.TitlesByGender.Male = "Godslayer"
	records := map[string]RecordDefinition{
		"123456": record,
	}

	char := TransformCharacter(component, classes, races, records)

	if char.CurrentTitle != "Godslayer" {
		t.Errorf("expected CurrentTitle 'Godslayer', got %s", char.CurrentTitle)
	}
}

func TestMatchesGameMode(t *testing.T) {
	allModes := []api.GameMode{
		api.GameModeCompetitive,
		api.GameModeQuickPlay,
		api.GameModeIronBanner,
		api.GameModeTrials,
	}

	tests := []struct {
		name         string
		mode         string
		activity     string
		expectedMode api.GameMode
	}{
		// Competitive combinations
		{
			name:         "Competitive Clash Quickplay Matchmade",
			mode:         "Clash Quickplay",
			activity:     "Competitive: Matchmade",
			expectedMode: api.GameModeCompetitive,
		},
		{
			name:         "Competitive Zone Control Matchmade",
			mode:         "Zone Control",
			activity:     "Competitive: Matchmade",
			expectedMode: api.GameModeCompetitive,
		},
		{
			name:         "Competitive Clash Quickplay",
			mode:         "Clash Quickplay",
			activity:     "Competitive",
			expectedMode: api.GameModeCompetitive,
		},
		{
			name:         "Competitive Rift",
			mode:         "Rift",
			activity:     "Competitive",
			expectedMode: api.GameModeCompetitive,
		},
		{
			name:         "Competitive Survival",
			mode:         "Survival",
			activity:     "Competitive",
			expectedMode: api.GameModeCompetitive,
		},
		{
			name:         "Competitive Control Competitive",
			mode:         "Control Competitive",
			activity:     "Competitive: Matchmade",
			expectedMode: api.GameModeCompetitive,
		},
		{
			name:         "Competitive Clash Competitive",
			mode:         "Clash Competitive",
			activity:     "Competitive: Matchmade",
			expectedMode: api.GameModeCompetitive,
		},

		// Quickplay combinations
		{
			name:         "Quickplay Control Matchmade",
			mode:         "Control Quickplay",
			activity:     "Control: Matchmade",
			expectedMode: api.GameModeQuickPlay,
		},
		{
			name:         "Quickplay Control",
			mode:         "Control Quickplay",
			activity:     "Control",
			expectedMode: api.GameModeQuickPlay,
		},
		{
			name:         "Quickplay Clash 3v3 Matchmade",
			mode:         "Clash Quickplay",
			activity:     "Clash 3v3: Matchmade",
			expectedMode: api.GameModeQuickPlay,
		},
		{
			name:         "Quickplay Zone Control Matchmade",
			mode:         "Zone Control",
			activity:     "Zone Control: Matchmade",
			expectedMode: api.GameModeQuickPlay,
		},
		{
			name:         "Quickplay Cutting Edge Zone Control Matchmade",
			mode:         "Zone Control",
			activity:     "Cutting Edge: Zone Control: Matchmade",
			expectedMode: api.GameModeQuickPlay,
		},

		// Iron Banner combinations
		{
			name:         "Iron Banner Control with Control Matchmade activity",
			mode:         "Iron Banner Control",
			activity:     "Control: Matchmade",
			expectedMode: api.GameModeIronBanner,
		},
		{
			name:         "Iron Banner Control",
			mode:         "Iron Banner Control",
			activity:     "Iron Banner: Control",
			expectedMode: api.GameModeIronBanner,
		},
		{
			name:         "Iron Banner Supremacy Tribute",
			mode:         "Iron Banner Supremacy",
			activity:     "Iron Banner: Tribute",
			expectedMode: api.GameModeIronBanner,
		},
		{
			name:         "Iron Banner Clash Eruption Matchmade",
			mode:         "Iron Banner Clash",
			activity:     "Eruption: Matchmade",
			expectedMode: api.GameModeIronBanner,
		},
		{
			name:         "Iron Banner Clash Iron Banner Eruption",
			mode:         "Iron Banner Clash",
			activity:     "Iron Banner: Eruption",
			expectedMode: api.GameModeIronBanner,
		},

		// Trials of Osiris combinations
		{
			name:         "Trials of Osiris Matchmade",
			mode:         "Trials of Osiris",
			activity:     "Trials of Osiris: Matchmade",
			expectedMode: api.GameModeTrials,
		},
		{
			name:         "Trials of Osiris",
			mode:         "Trials of Osiris",
			activity:     "Trials of Osiris",
			expectedMode: api.GameModeTrials,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			est := EstimateGameMode(ptr.Of(tt.mode), tt.activity)
			if est == nil || *est != tt.expectedMode {
				t.Errorf("EstimateGameMode(%q, %q) = %v, want %q", tt.mode, tt.activity, est, tt.expectedMode)
			}

			history := api.ActivityHistory{
				Mode:              ptr.Of(tt.mode),
				Activity:          tt.activity,
				EstimatedGameMode: est,
			}

			// Should always match GameModeAll
			if !MatchesGameMode(history, api.GameModeAll) {
				t.Errorf("expected (%q, %q) to match GameModeAll", tt.mode, tt.activity)
			}

			// Should match only the expected GameMode and none of the others
			for _, gm := range allModes {
				got := MatchesGameMode(history, gm)
				want := gm == tt.expectedMode
				if got != want {
					t.Errorf("MatchesGameMode((mode=%q, activity=%q), %q) = %v, want %v", tt.mode, tt.activity, gm, got, want)
				}
			}
		})
	}
}
