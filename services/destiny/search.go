package destiny

import (
	"context"
	"fmt"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"strconv"
	"strings"

	clog "github.com/Shannor/canonical-logger"
)

const bungieMembershipTypeAll int32 = -1

func (a *service) Search(ctx context.Context, prefix string, page int32) ([]api.SearchUserResult, bool, error) {
	trimmed := strings.TrimSpace(prefix)
	namePart, codePart, hasDiscriminator := strings.Cut(trimmed, "#")
	namePart = strings.TrimSpace(namePart)
	codePart = strings.TrimSpace(codePart)

	clog.WithGroupFields(ctx, "destinyService.search", clog.Fields{
		"page":             page,
		"queryLen":         len(trimmed),
		"hasDiscriminator": hasDiscriminator,
	})

	if namePart == "" {
		clog.WithGroupFields(ctx, "destinyService.search", clog.Fields{
			"hasMore":       false,
			"returnedCount": 0,
		})
		return []api.SearchUserResult{}, false, nil
	}

	if page == 0 && hasDiscriminator && len(codePart) == 4 && isASCIIDigits(codePart) {
		return a.searchExactBungieName(ctx, namePart, codePart)
	}

	body := bungie.UserSearchPrefixRequest{DisplayNamePrefix: ptr.Of(namePart)}
	resp, err := a.Client.UserSearchByGlobalNamePostWithResponse(ctx, page, body)
	if err != nil {
		return nil, false, err
	}

	clog.WithGroupField(ctx, "destinyService.search", "statusCode", resp.StatusCode())
	if resp.StatusCode() == http.StatusServiceUnavailable {
		return nil, false, ErrDestinyServerDown
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, false, fmt.Errorf("bungie search failed with status %d", resp.StatusCode())
	}
	if resp.JSON200 == nil || resp.JSON200.SearchResponse == nil {
		return nil, false, fmt.Errorf("no response")
	}

	hasMore := resp.JSON200.SearchResponse.HasMore != nil && *resp.JSON200.SearchResponse.HasMore
	results := make([]api.SearchUserResult, 0)
	if resp.JSON200.SearchResponse.SearchResults != nil {
		for _, detail := range *resp.JSON200.SearchResponse.SearchResults {
			if hasDiscriminator && codePart != "" && !matchesDiscriminatorPrefix(detail, codePart) {
				continue
			}
			res, ok := transformUserSearchDetail(detail)
			if !ok {
				continue
			}
			results = append(results, res)
		}
	}

	clog.WithGroupFields(ctx, "destinyService.search", clog.Fields{
		"hasMore":       hasMore,
		"returnedCount": len(results),
	})
	return results, hasMore, nil
}

func (a *service) searchExactBungieName(ctx context.Context, namePart string, codePart string) ([]api.SearchUserResult, bool, error) {
	codeNum, err := strconv.ParseInt(codePart, 10, 16)
	if err != nil {
		return []api.SearchUserResult{}, false, nil
	}

	body := bungie.Destiny2SearchDestinyPlayerByBungieNameJSONRequestBody{
		DisplayName:     ptr.Of(namePart),
		DisplayNameCode: ptr.Of(int16(codeNum)),
	}
	resp, err := a.Client.Destiny2SearchDestinyPlayerByBungieNameWithResponse(ctx, bungieMembershipTypeAll, body)
	if err != nil {
		return nil, false, err
	}

	clog.WithGroupField(ctx, "destinyService.search", "statusCode", resp.StatusCode())
	if resp.StatusCode() == http.StatusServiceUnavailable {
		return nil, false, ErrDestinyServerDown
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, false, fmt.Errorf("bungie exact search failed with status %d", resp.StatusCode())
	}
	if resp.JSON200 == nil {
		return nil, false, fmt.Errorf("no response")
	}

	results := make([]api.SearchUserResult, 0)
	if resp.JSON200.Response != nil && len(*resp.JSON200.Response) > 0 {
		if res, ok := transformUserInfoCards(*resp.JSON200.Response); ok {
			results = append(results, res)
		}
	}

	clog.WithGroupFields(ctx, "destinyService.search", clog.Fields{
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

func transformUserSearchDetail(detail bungie.UserSearchDetail) (api.SearchUserResult, bool) {
	if detail.DestinyMemberships == nil || len(*detail.DestinyMemberships) == 0 {
		return api.SearchUserResult{}, false
	}

	primary := selectPrimaryMembership(*detail.DestinyMemberships)
	if primary.MembershipId == nil || *primary.MembershipId == "" {
		return api.SearchUserResult{}, false
	}

	displayName := firstNonEmpty(
		derefString(detail.BungieGlobalDisplayName),
		derefString(primary.BungieGlobalDisplayName),
		derefString(primary.DisplayName),
	)

	uniqueName := formatUniqueName(detail.BungieGlobalDisplayName, detail.BungieGlobalDisplayNameCode)
	if uniqueName == "" {
		uniqueName = formatUniqueName(primary.BungieGlobalDisplayName, primary.BungieGlobalDisplayNameCode)
	}
	if uniqueName == "" {
		uniqueName = displayName
	}

	result := api.SearchUserResult{
		IsRecorded:          false,
		PrimaryMembershipID: *primary.MembershipId,
		DisplayName:         displayName,
		UniqueName:          uniqueName,
		AlternateNames:      collectAlternateNames(*detail.DestinyMemberships),
	}
	if primary.MembershipType != nil {
		result.PrimaryMembershipType = ptr.Of(int64(*primary.MembershipType))
	}
	if detail.BungieNetMembershipId != nil && *detail.BungieNetMembershipId != "" {
		result.BungieNetMembershipID = ptr.Of(*detail.BungieNetMembershipId)
	}
	if primary.IconPath != nil && *primary.IconPath != "" {
		result.IconPath = ptr.Of(*primary.IconPath)
	}

	return result, true
}

func transformUserInfoCards(cards []bungie.UserUserInfoCard) (api.SearchUserResult, bool) {
	if len(cards) == 0 {
		return api.SearchUserResult{}, false
	}

	primary := selectPrimaryMembership(cards)
	if primary.MembershipId == nil || *primary.MembershipId == "" {
		return api.SearchUserResult{}, false
	}

	displayName := firstNonEmpty(
		derefString(primary.BungieGlobalDisplayName),
		derefString(primary.DisplayName),
	)
	uniqueName := formatUniqueName(primary.BungieGlobalDisplayName, primary.BungieGlobalDisplayNameCode)
	if uniqueName == "" {
		uniqueName = displayName
	}

	result := api.SearchUserResult{
		IsRecorded:          false,
		PrimaryMembershipID: *primary.MembershipId,
		DisplayName:         displayName,
		UniqueName:          uniqueName,
		AlternateNames:      collectAlternateNames(cards),
	}
	if primary.MembershipType != nil {
		result.PrimaryMembershipType = ptr.Of(int64(*primary.MembershipType))
	}
	if primary.IconPath != nil && *primary.IconPath != "" {
		result.IconPath = ptr.Of(*primary.IconPath)
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
	seen := make(map[string]struct{}, len(memberships))
	names := make([]string, 0, len(memberships))
	for _, m := range memberships {
		if m.DisplayName == nil {
			continue
		}
		name := strings.TrimSpace(*m.DisplayName)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func formatUniqueName(displayName *string, code *int16) string {
	if displayName == nil || *displayName == "" || code == nil {
		return ""
	}
	return fmt.Sprintf("%s#%04d", *displayName, *code)
}

func derefString(s *string) string {
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
