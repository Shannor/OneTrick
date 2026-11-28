package bungie

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBungiePlatformURL = "https://www.bungie.net/Platform"
	bungieAssetBaseURL       = "https://www.bungie.net"
	defaultMaxThrottleRetry  = 2
	defaultMaxBackoff        = 2 * time.Second
	defaultRetryBackoff      = 250 * time.Millisecond
)

const (
	componentCharacters            int32 = 200
	componentCharacterProgressions int32 = 202
	componentCharacterEquipment    int32 = 205
	componentItemInstances         int32 = 300
	componentItemPerks             int32 = 302
	componentItemStats             int32 = 304
	componentItemSockets           int32 = 305
	componentItemCommonData        int32 = 307
	componentTransitory            int32 = 1000
	componentMetrics               int32 = 1100
)

type OAuthRefresher interface {
	RefreshAccessToken(ctx context.Context, refreshToken string) (domain.OAuthToken, error)
}

type AuthCredentials struct {
	AccessToken  string
	RefreshToken string
	OnRefresh    func(domain.OAuthToken)
}

type authCredentialsKey struct{}

func WithAuthCredentials(ctx context.Context, creds AuthCredentials) context.Context {
	return context.WithValue(ctx, authCredentialsKey{}, creds)
}

func authCredentialsFromContext(ctx context.Context) (AuthCredentials, bool) {
	creds, ok := ctx.Value(authCredentialsKey{}).(AuthCredentials)
	return creds, ok
}

type clientConfig struct {
	baseURL         string
	refresher       OAuthRefresher
	maxRetries      int
	maxBackoff      time.Duration
	fallbackBackoff time.Duration
}

type ClientOption func(*clientConfig)

func WithBaseURL(baseURL string) ClientOption {
	return func(cfg *clientConfig) {
		if trimmed := strings.TrimSpace(baseURL); trimmed != "" {
			cfg.baseURL = trimmed
		}
	}
}

func WithOAuthRefresher(refresher OAuthRefresher) ClientOption {
	return func(cfg *clientConfig) {
		cfg.refresher = refresher
	}
}

func WithThrottleRetry(maxRetries int, maxBackoff time.Duration) ClientOption {
	return func(cfg *clientConfig) {
		cfg.maxRetries = max(0, maxRetries)
		if maxBackoff >= 0 {
			cfg.maxBackoff = maxBackoff
			if cfg.maxBackoff < cfg.fallbackBackoff {
				cfg.fallbackBackoff = cfg.maxBackoff
			}
		}
	}
}

func NewClient(apiKey string, httpClient *http.Client, opts ...ClientOption) (*bungie.ClientWithResponses, error) {
	cfg := clientConfig{
		baseURL:         defaultBungiePlatformURL,
		maxRetries:      defaultMaxThrottleRetry,
		maxBackoff:      defaultMaxBackoff,
		fallbackBackoff: defaultRetryBackoff,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	baseHC := http.Client{}
	if httpClient != nil {
		baseHC = *httpClient
	}
	transport := baseHC.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	baseHC.Transport = &bungieRoundTripper{
		base:            transport,
		refresher:       cfg.refresher,
		maxRetries:      cfg.maxRetries,
		maxBackoff:      cfg.maxBackoff,
		fallbackBackoff: cfg.fallbackBackoff,
	}

	return bungie.NewClientWithResponses(
		cfg.baseURL,
		bungie.WithHTTPClient(&baseHC),
		bungie.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("X-API-KEY", apiKey)
			req.Header.Set("Accept", "application/json")
			if req.Header.Get("Content-Type") == "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("User-Agent", "oneTrick-backend")
			return nil
		}),
	)
}

type bungieRoundTripper struct {
	base            http.RoundTripper
	refresher       OAuthRefresher
	maxRetries      int
	maxBackoff      time.Duration
	fallbackBackoff time.Duration
}

func (rt *bungieRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	creds, hasCreds := authCredentialsFromContext(ctx)
	accessToken := creds.AccessToken
	refreshed := false

	for attempt := 0; ; attempt++ {
		cloned, ok := cloneReplayableRequest(req, attempt > 0)
		if !ok {
			return rt.base.RoundTrip(req)
		}
		if hasCreds && accessToken != "" && (cloned.Header.Get("Authorization") == "" || refreshed) {
			cloned.Header.Set("Authorization", "Bearer "+accessToken)
		}

		resp, err := rt.base.RoundTrip(cloned)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusUnauthorized &&
			!refreshed &&
			hasCreds &&
			creds.RefreshToken != "" &&
			rt.refresher != nil &&
			canReplayRequest(req) {
			refreshed = true
			newTok, refreshErr := rt.refresher.RefreshAccessToken(ctx, creds.RefreshToken)
			if refreshErr == nil && newTok.AccessToken != "" {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
				accessToken = newTok.AccessToken
				if creds.OnRefresh != nil {
					creds.OnRefresh(newTok)
				}
				attempt--
				continue
			}
		}

		throttled, backoff, inspectErr := rt.inspectThrottle(resp)
		if inspectErr != nil {
			return resp, nil
		}
		if !throttled || attempt >= rt.maxRetries || !canReplayRequest(req) {
			return resp, nil
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		if err := sleepWithContext(ctx, backoff); err != nil {
			return nil, err
		}
	}
}

func canReplayRequest(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

func cloneReplayableRequest(req *http.Request, rewindBody bool) (*http.Request, bool) {
	cloned := req.Clone(req.Context())
	if !rewindBody || req.Body == nil || req.Body == http.NoBody {
		return cloned, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	cloned.Body = body
	return cloned, true
}

func (rt *bungieRoundTripper) inspectThrottle(resp *http.Response) (bool, time.Duration, error) {
	var retryAfter time.Duration
	if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			retryAfter = time.Duration(secs) * time.Second
		}
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return true, rt.clampBackoff(retryAfter), nil
	}

	if resp.Body == nil {
		return false, 0, nil
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(strings.ToLower(ct), "json") {
		return false, 0, nil
	}

	raw, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil || len(raw) == 0 {
		return false, 0, err
	}

	var env bungieEnvelope
	if unmarshalErr := json.Unmarshal(raw, &env); unmarshalErr != nil {
		return false, 0, nil
	}
	if !isThrottleEnvelope(env) {
		return false, 0, nil
	}
	if env.ThrottleSeconds != nil && *env.ThrottleSeconds > 0 {
		retryAfter = time.Duration(*env.ThrottleSeconds) * time.Second
	}
	return true, rt.clampBackoff(retryAfter), nil
}

func (rt *bungieRoundTripper) clampBackoff(requested time.Duration) time.Duration {
	d := requested
	if d <= 0 {
		d = rt.fallbackBackoff
	}
	if d > rt.maxBackoff {
		d = rt.maxBackoff
	}
	return d
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func formatBungieURL(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "/") {
		return bungieAssetBaseURL + trimmed
	}
	return bungieAssetBaseURL + "/" + trimmed
}

func formatBungieOptionalURL(path *string) string {
	if path == nil {
		return ""
	}
	return formatBungieURL(*path)
}

func activityModeTypeToString(modeType *bungie.CurrentActivityModeType) string {
	if modeType == nil {
		return "Unknown"
	}
	switch *modeType {
	case bungie.CurrentActivityModeTypeControl:
		return "Control"
	case bungie.CurrentActivityModeTypeIronBannerZoneControl:
		return "Iron Banner Zone Control"
	case bungie.CurrentActivityModeTypeIronBannerControl:
		return "Iron Banner Control"
	case bungie.CurrentActivityModeTypeZoneControl:
		return "Zone Control"
	case bungie.CurrentActivityModeTypeControlCompetitive:
		return "Control Competitive"
	case bungie.CurrentActivityModeTypeControlQuickplay:
		return "Control Quickplay"
	case bungie.CurrentActivityModeTypePrivateMatchesControl:
		return "Private Matches Control"
	case bungie.CurrentActivityModeTypeAllDoubles:
		return "Doubles"
	case bungie.CurrentActivityModeTypeAllPvE:
		return "PvE"
	case bungie.CurrentActivityModeTypeAllPvP:
		return "PvP"
	case bungie.CurrentActivityModeTypeClash:
		return "Clash"
	case bungie.CurrentActivityModeTypeClashQuickplay:
		return "Clash Quickplay"
	case bungie.CurrentActivityModeTypeClashCompetitive:
		return "Clash Competitive"
	case bungie.CurrentActivityModeTypeIronBannerRift:
		return "Iron Banner Rift"
	case bungie.CurrentActivityModeTypeRift:
		return "Rift"
	case bungie.CurrentActivityModeTypeIronBannerClash:
		return "Iron Banner Clash"
	case bungie.CurrentActivityModeTypeIronBannerSupremacy:
		return "Iron Banner Tribute / Supremacy"
	case bungie.CurrentActivityModeTypePrivateMatchesSurvival:
		return "Private Matches Survival"
	case bungie.CurrentActivityModeTypeTrialsSurvival:
		return "Trials Survival"
	case bungie.CurrentActivityModeTypeTrialsCountdown:
		return "Trials Countdown"
	case bungie.CurrentActivityModeTypeRaid:
		return "Raid"
	case bungie.CurrentActivityModeTypeNightfall:
		return "Nightfall"
	case bungie.CurrentActivityModeTypeGambit:
		return "Gambit"
	case bungie.CurrentActivityModeTypeIronBanner:
		return "Iron Banner"
	case bungie.CurrentActivityModeTypeTrialsOfOsiris:
		return "Trials of Osiris"
	case bungie.CurrentActivityModeTypeSurvival:
		return "Survival"
	default:
		return "Unknown"
	}
}

func transformTeams(teams *[]bungie.TeamEntry) []domain.Team {
	if teams == nil || *teams == nil {
		return nil
	}
	result := make([]domain.Team, 0, len(*teams))
	for _, team := range *teams {
		if team.TeamID == nil {
			continue
		}
		t := domain.Team{
			ID: strconv.Itoa(int(*team.TeamID)),
		}
		if team.TeamName != nil {
			t.TeamName = *team.TeamName
		}
		if team.Score != nil && team.Score.Basic != nil && team.Score.Basic.DisplayValue != nil {
			t.Score = *team.Score.Basic.DisplayValue
		}
		if team.Standing != nil && team.Standing.Basic != nil && team.Standing.Basic.DisplayValue != nil {
			t.Standing = *team.Standing.Basic.DisplayValue
		}
		result = append(result, t)
	}
	return result
}

func parseOptionalInt64(value *string) *int64 {
	if value == nil || *value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(*value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

func toDomainStatPair(pair *bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair) domain.StatValuePair {
	if pair == nil {
		return domain.StatValuePair{}
	}
	var val float64
	if pair.Value != nil {
		val = *pair.Value
	}
	var disp string
	if pair.DisplayValue != nil {
		disp = *pair.DisplayValue
	}
	return domain.StatValuePair{
		Value:        val,
		DisplayValue: disp,
		Present:      true,
	}
}

func toDomainPlayerStats(vals map[string]bungie.HistoricalStatsValue) domain.PlayerStats {
	var ps domain.PlayerStats
	for key, val := range vals {
		if val.Basic == nil {
			continue
		}
		pair := toDomainStatPair(val.Basic)
		switch key {
		case "kills":
			ps.Kills = pair
		case "assists":
			ps.Assists = pair
		case "deaths":
			ps.Deaths = pair
		case "killsDeathsRatio":
			ps.KD = pair
		case "killsDeathsAssists":
			ps.KDA = pair
		case "standing":
			ps.Standing = pair
		case "team":
			ps.Team = pair
		case "fireteamId":
			ps.FireteamID = pair
		case "timePlayedSeconds":
			ps.TimePlayed = pair
		}
	}
	return ps
}

func toDomainUniqueStats(vals map[string]bungie.HistoricalStatsValue) map[string]domain.UniqueStatValue {
	out := make(map[string]domain.UniqueStatValue, len(vals))
	for key, val := range vals {
		if val.Basic == nil {
			continue
		}
		var name string
		if val.StatId != nil {
			name = *val.StatId
		}
		out[key] = domain.UniqueStatValue{
			Name:       name,
			Basic:      toDomainStatPair(val.Basic),
			PGA:        toDomainStatPair(val.Pga),
			Weighted:   toDomainStatPair(val.Weighted),
			ActivityID: parseOptionalInt64(val.ActivityId),
		}
	}
	return out
}
