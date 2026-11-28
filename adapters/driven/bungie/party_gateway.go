package bungie

import (
	"context"
	"fmt"
	"oneTrick/clients/bungie"
	"oneTrick/core/fireteam"
	"oneTrick/ptr"

	clog "github.com/Shannor/canonical-logger"
)

var _ fireteam.PartyReader = (*PartyGateway)(nil)

type PartyGateway struct {
	client bungie.ClientWithResponsesInterface
}

func NewPartyGateway(client bungie.ClientWithResponsesInterface) *PartyGateway {
	return &PartyGateway{client: client}
}

func (g *PartyGateway) GetPartyMembershipIDs(
	ctx context.Context,
	primaryMembershipID int64,
	membershipType int64,
) ([]string, error) {
	clog.WithGroupFields(ctx, "bungieParty.getPartyMembers", clog.Fields{
		"primaryMembershipId": primaryMembershipID,
		"membershipType":      membershipType,
	})

	components := []int32{componentTransitory}
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
	if err := checkBungieResponse(ctx, "bungieParty.getPartyMembers", resp.StatusCode(), resp.Body, env); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Response == nil {
		return nil, fmt.Errorf("no response found")
	}

	transitory := resp.JSON200.Response.ProfileTransitoryData
	if transitory == nil || transitory.Data == nil || transitory.Data.PartyMembers == nil {
		clog.WithGroupField(ctx, "bungieParty.getPartyMembers", "partySize", 0)
		return []string{}, nil
	}

	members := *transitory.Data.PartyMembers
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, ptr.Deref(m.MembershipId))
	}
	clog.WithGroupField(ctx, "bungieParty.getPartyMembers", "partySize", len(ids))
	return ids, nil
}
