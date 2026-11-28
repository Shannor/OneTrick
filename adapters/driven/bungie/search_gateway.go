package bungie

import (
	"context"
	"fmt"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"oneTrick/set"
	"strconv"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

const bungieMembershipTypeAll int32 = -1

type UserSearchGateway struct {
	client bungie.ClientWithResponsesInterface
}

func NewUserSearchGateway(client bungie.ClientWithResponsesInterface) *UserSearchGateway {
	return &UserSearchGateway{client: client}
}

func (g *UserSearchGateway) SearchUsers(
	ctx context.Context,
	prefix string,
	page int32,
) ([]domain.BungieUserSearchResult, bool, error) {
	trimmed := strings.TrimSpace(prefix)
	namePart, codePart, hasDiscriminator := strings.Cut(trimmed, "#")
	namePart = strings.TrimSpace(namePart)
	codePart = strings.TrimSpace(codePart)

	clog.WithGroupFields(ctx, "bungieSearch.searchUsers", clog.Fields{
		"page":             page,
		"queryLen":         len(trimmed),
		"hasDiscriminator": hasDiscriminator,
	})

	if namePart == "" {
		clog.WithGroupFields(ctx, "bungieSearch.searchUsers", clog.Fields{
			"hasMore":       false,
			"returnedCount": 0,
		})
		return []domain.BungieUserSearchResult{}, false, nil
	}

	if hasDiscriminator && len(codePart) == 4 && isASCIIDigits(codePart) {
		if page > 0 {
			clog.WithGroupFields(ctx, "bungieSearch.searchUsers", clog.Fields{
				"hasMore":       false,
				"returnedCount": 0,
			})
			return []domain.BungieUserSearchResult{}, false, nil
		}
		return g.searchExactBungieName(ctx, namePart, codePart)
	}

	body := bungie.UserSearchPrefixRequest{DisplayNamePrefix: new(namePart)}
	resp, err := g.client.UserSearchByGlobalNamePostWithResponse(ctx, page, body)
	if err != nil {
		return nil, false, err
	}
	if resp == nil {
		return nil, false, fmt.Errorf("no response")
	}

	clog.WithGroupField(ctx, "bungieSearch.searchUsers", "statusCode", resp.StatusCode())

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieSearch.searchUsers", resp.StatusCode(), resp.Body, env); err != nil {
		return nil, false, err
	}
	if resp.JSON200 == nil || resp.JSON200.SearchResponse == nil {
		return nil, false, fmt.Errorf("no response")
	}

	hasMore := resp.JSON200.SearchResponse.HasMore != nil && *resp.JSON200.SearchResponse.HasMore
	results := make([]domain.BungieUserSearchResult, 0)
	if resp.JSON200.SearchResponse.SearchResults != nil {
		for _, detail := range *resp.JSON200.SearchResponse.SearchResults {
			if hasDiscriminator && codePart != "" && !matchesDiscriminatorPrefix(detail, codePart) {
				continue
			}
			res, ok := transformUserSearchDetail(detail)
			if !ok {
				continue
			}
			if hasDiscriminator && !strings.EqualFold(res.DisplayName, namePart) {
				continue
			}
			results = append(results, res)
		}
	}

	clog.WithGroupFields(ctx, "bungieSearch.searchUsers", clog.Fields{
		"hasMore":       hasMore,
		"returnedCount": len(results),
	})
	return results, hasMore, nil
}

func (g *UserSearchGateway) searchExactBungieName(
	ctx context.Context,
	namePart string,
	codePart string,
) ([]domain.BungieUserSearchResult, bool, error) {
	codeNum, err := strconv.ParseInt(codePart, 10, 16)
	if err != nil {
		return []domain.BungieUserSearchResult{}, false, nil
	}

	body := bungie.Destiny2SearchDestinyPlayerByBungieNameJSONRequestBody{
		DisplayName:     new(namePart),
		DisplayNameCode: new(int16(codeNum)),
	}
	resp, err := g.client.Destiny2SearchDestinyPlayerByBungieNameWithResponse(ctx, bungieMembershipTypeAll, body)
	if err != nil {
		return nil, false, err
	}
	if resp == nil {
		return nil, false, fmt.Errorf("no response")
	}

	clog.WithGroupField(ctx, "bungieSearch.searchUsers", "statusCode", resp.StatusCode())

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieSearch.searchUsers", resp.StatusCode(), resp.Body, env); err != nil {
		return nil, false, err
	}
	if resp.JSON200 == nil || resp.JSON200.Response == nil {
		return nil, false, fmt.Errorf("no response")
	}

	results := make([]domain.BungieUserSearchResult, 0, 1)
	if len(*resp.JSON200.Response) > 0 {
		if res, ok := transformUserInfoCards(*resp.JSON200.Response); ok {
			results = append(results, res)
		}
	}

	clog.WithGroupFields(ctx, "bungieSearch.searchUsers", clog.Fields{
		"hasMore":       false,
		"returnedCount": len(results),
	})
	return results, false, nil
}

func matchesDiscriminatorPrefix(detail bungie.UserSearchDetail, codePrefix string) bool {
	if detail.BungieGlobalDisplayNameCode != nil {
		return strings.HasPrefix(fmt.Sprintf("%04d", *detail.BungieGlobalDisplayNameCode), codePrefix)
	}
	if detail.DestinyMemberships != nil {
		for _, m := range *detail.DestinyMemberships {
			if m.BungieGlobalDisplayNameCode != nil {
				return strings.HasPrefix(fmt.Sprintf("%04d", *m.BungieGlobalDisplayNameCode), codePrefix)
			}
		}
	}
	return false
}

func transformUserSearchDetail(detail bungie.UserSearchDetail) (domain.BungieUserSearchResult, bool) {
	if detail.DestinyMemberships == nil || len(*detail.DestinyMemberships) == 0 {
		return domain.BungieUserSearchResult{}, false
	}

	primary := selectPrimaryMembership(*detail.DestinyMemberships)
	if primary.MembershipId == nil || *primary.MembershipId == "" {
		return domain.BungieUserSearchResult{}, false
	}

	displayName := firstNonEmpty(
		derefTrimmed(detail.BungieGlobalDisplayName),
		derefTrimmed(primary.BungieGlobalDisplayName),
		derefTrimmed(primary.DisplayName),
	)

	uniqueName := formatUniqueName(detail.BungieGlobalDisplayName, detail.BungieGlobalDisplayNameCode)
	if uniqueName == "" {
		uniqueName = formatUniqueName(primary.BungieGlobalDisplayName, primary.BungieGlobalDisplayNameCode)
	}
	if uniqueName == "" {
		uniqueName = displayName
	}

	result := domain.BungieUserSearchResult{
		PrimaryMembershipID: *primary.MembershipId,
		DisplayName:         displayName,
		UniqueName:          uniqueName,
		AlternateNames:      collectAlternateNames(*detail.DestinyMemberships),
	}
	if primary.MembershipType != nil {
		result.PrimaryMembershipType = int64(*primary.MembershipType)
	}
	if detail.BungieNetMembershipId != nil {
		result.BungieNetMembershipID = strings.TrimSpace(*detail.BungieNetMembershipId)
	}
	if primary.IconPath != nil {
		result.IconPath = strings.TrimSpace(*primary.IconPath)
	}
	return result, true
}

func transformUserInfoCards(cards []bungie.UserUserInfoCard) (domain.BungieUserSearchResult, bool) {
	if len(cards) == 0 {
		return domain.BungieUserSearchResult{}, false
	}

	primary := selectPrimaryMembership(cards)
	if primary.MembershipId == nil || *primary.MembershipId == "" {
		return domain.BungieUserSearchResult{}, false
	}

	displayName := firstNonEmpty(
		derefTrimmed(primary.BungieGlobalDisplayName),
		derefTrimmed(primary.DisplayName),
	)
	uniqueName := formatUniqueName(primary.BungieGlobalDisplayName, primary.BungieGlobalDisplayNameCode)
	if uniqueName == "" {
		uniqueName = displayName
	}

	result := domain.BungieUserSearchResult{
		PrimaryMembershipID: *primary.MembershipId,
		DisplayName:         displayName,
		UniqueName:          uniqueName,
		AlternateNames:      collectAlternateNames(cards),
	}
	if primary.MembershipType != nil {
		result.PrimaryMembershipType = int64(*primary.MembershipType)
	}
	if primary.IconPath != nil {
		result.IconPath = strings.TrimSpace(*primary.IconPath)
	}
	return result, true
}

func selectPrimaryMembership(memberships []bungie.UserUserInfoCard) bungie.UserUserInfoCard {
	for _, m := range memberships {
		noOverride := m.CrossSaveOverride == nil || *m.CrossSaveOverride == 0
		hasApplicable := m.ApplicableMembershipTypes != nil && len(*m.ApplicableMembershipTypes) > 0
		if noOverride && hasApplicable {
			return m
		}
	}
	for _, m := range memberships {
		if m.CrossSaveOverride != nil && *m.CrossSaveOverride != 0 && m.MembershipType != nil && *m.CrossSaveOverride == *m.MembershipType {
			return m
		}
	}
	return memberships[0]
}

func collectAlternateNames(memberships []bungie.UserUserInfoCard) []string {
	ordered := set.NewOrdered[string]()
	for _, m := range memberships {
		if m.DisplayName == nil {
			continue
		}
		if name := strings.TrimSpace(*m.DisplayName); name != "" {
			ordered.Add(name)
		}
	}
	return ordered.ToSlice()
}

func formatUniqueName(displayName *string, code *int16) string {
	if displayName == nil || *displayName == "" || code == nil {
		return ""
	}
	return fmt.Sprintf("%s#%04d", *displayName, *code)
}

func derefTrimmed(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
