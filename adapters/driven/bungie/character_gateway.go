package bungie

import (
	"cmp"
	"context"
	"fmt"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"slices"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

type CharacterManifestReader interface {
	GetClasses(ctx context.Context) (map[int64]domain.ClassManifest, error)
	GetRaces(ctx context.Context) (map[int64]domain.RaceManifest, error)
	GetRecord(ctx context.Context, hash int64) (domain.RecordManifest, bool, error)
}

type CharacterGateway struct {
	client   bungie.ClientWithResponsesInterface
	manifest CharacterManifestReader
}

func NewCharacterGateway(
	client bungie.ClientWithResponsesInterface,
	manifest CharacterManifestReader,
) *CharacterGateway {
	return &CharacterGateway{
		client:   client,
		manifest: manifest,
	}
}

func (g *CharacterGateway) GetCharacters(
	ctx context.Context,
	primaryMembershipID int64,
	membershipType int64,
) ([]domain.Character, error) {
	clog.WithGroupFields(ctx, "bungieCharacter.getCharacters", clog.Fields{
		"primaryMembershipId": primaryMembershipID,
		"membershipType":      membershipType,
	})

	components := []int32{componentCharacters}
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	resp, err := g.client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), primaryMembershipID, params)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("no response found")
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
	if err := checkBungieResponse(ctx, "bungieCharacter.getCharacters", resp.StatusCode(), resp.Body, env); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Response == nil || resp.JSON200.Response.Characters == nil || resp.JSON200.Response.Characters.Data == nil {
		clog.WithGroupField(ctx, "bungieCharacter.getCharacters", "statusCode", resp.StatusCode())
		return nil, fmt.Errorf("no response found")
	}

	classes, err := g.manifest.GetClasses(ctx)
	if err != nil {
		return nil, fmt.Errorf("manifest required for characters: %w", err)
	}
	races, err := g.manifest.GetRaces(ctx)
	if err != nil {
		return nil, err
	}

	data := *resp.JSON200.Response.Characters.Data
	records := make(map[int64]domain.RecordManifest)
	for _, d := range data {
		if d.TitleRecordHash != nil {
			hash := int64(*d.TitleRecordHash)
			if _, seen := records[hash]; seen {
				continue
			}
			record, found, recErr := g.manifest.GetRecord(ctx, hash)
			if recErr == nil && found {
				records[hash] = record
			}
		}
	}

	results := make([]domain.Character, 0, len(data))
	for _, c := range data {
		results = append(results, transformCharacter(c, classes, races, records))
	}
	slices.SortFunc(results, func(a, b domain.Character) int {
		if a.Light != b.Light {
			return cmp.Compare(b.Light, a.Light)
		}
		return strings.Compare(a.Class, b.Class)
	})

	clog.WithGroupField(ctx, "bungieCharacter.getCharacters", "characterCount", len(results))
	return results, nil
}

func transformCharacter(
	item bungie.CharacterComponent,
	classes map[int64]domain.ClassManifest,
	races map[int64]domain.RaceManifest,
	records map[int64]domain.RecordManifest,
) domain.Character {
	var c domain.Character
	if item.CharacterId != nil {
		c.ID = *item.CharacterId
	}
	if item.Light != nil {
		c.Light = int64(*item.Light)
	}
	if item.ClassHash != nil {
		if classDef, ok := classes[int64(*item.ClassHash)]; ok {
			c.Class = classDef.Name
		}
	}
	if item.RaceHash != nil {
		if raceDef, ok := races[int64(*item.RaceHash)]; ok {
			c.Race = raceDef.Name
		}
	}
	if item.TitleRecordHash != nil {
		if recordDef, ok := records[int64(*item.TitleRecordHash)]; ok {
			c.CurrentTitle = recordDef.ResolvedTitle()
		}
	}

	c.EmblemBackgroundURL = formatBungieOptionalURL(item.EmblemBackgroundPath)
	c.EmblemURL = formatBungieOptionalURL(item.EmblemPath)

	if item.EmblemColor != nil {
		ec := item.EmblemColor
		if ec.Alpha != nil {
			c.EmblemColor.Alpha = *ec.Alpha
		}
		if ec.Blue != nil {
			c.EmblemColor.Blue = *ec.Blue
		}
		if ec.Green != nil {
			c.EmblemColor.Green = *ec.Green
		}
		if ec.Red != nil {
			c.EmblemColor.Red = *ec.Red
		}
	}
	return c
}
