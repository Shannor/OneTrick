package destiny

import (
	"oneTrick/clients/bungie"
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
