package bungie

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"oneTrick/core/domain"
	"oneTrick/ptr"

	clog "github.com/Shannor/canonical-logger"
)

const (
	bungieErrorCodeSuccess                            int32 = 1
	bungieErrorCodeSystemDisabled                     int32 = 5
	bungieErrorCodeThrottleLimitExceeded              int32 = 35
	bungieErrorCodeThrottleLimitExceededMinutes       int32 = 36
	bungieErrorCodeThrottleLimitExceededMomentarily   int32 = 37
	bungieErrorCodeThrottleLimitExceededSeconds       int32 = 38
	bungieErrorCodePerEndpointRequestThrottleExceeded int32 = 39
	bungieErrorCodePGCRNotFound                       int32 = 1653
	bungieErrorCodePrivacyRestriction                 int32 = 1665
)

type bungieEnvelope struct {
	ErrorCode       *int32  `json:"ErrorCode"`
	ThrottleSeconds *int32  `json:"ThrottleSeconds"`
	ErrorStatus     *string `json:"ErrorStatus"`
	Message         *string `json:"Message"`
}

func isThrottleErrorCode(code int32) bool {
	return code >= bungieErrorCodeThrottleLimitExceeded &&
		code <= bungieErrorCodePerEndpointRequestThrottleExceeded
}

func isThrottleEnvelope(env bungieEnvelope) bool {
	if env.ErrorCode != nil && isThrottleErrorCode(*env.ErrorCode) {
		return true
	}
	return env.ThrottleSeconds != nil && *env.ThrottleSeconds > 0
}

// checkBungieResponse inspects both Bungie's JSON envelope (which may carry specific
// ErrorCode values on HTTP 200 or non-200 responses) and the HTTP status code,
// translating Bungie wire errors into domain sentinel errors.
func checkBungieResponse(
	ctx context.Context,
	logGroup string,
	statusCode int,
	body []byte,
	env bungieEnvelope,
) error {
	if env.ErrorCode == nil && len(body) > 0 {
		_ = json.Unmarshal(body, &env)
	}

	if env.ErrorCode != nil && *env.ErrorCode != 0 && *env.ErrorCode != bungieErrorCodeSuccess {
		clog.WithGroupField(ctx, logGroup, "bungieErrorCode", *env.ErrorCode)
		switch *env.ErrorCode {
		case bungieErrorCodeSystemDisabled:
			return domain.ErrDestinyUnavailable
		case bungieErrorCodePrivacyRestriction:
			return domain.ErrPrivateProfile
		case bungieErrorCodePGCRNotFound:
			return domain.ErrActivityNotFound
		}
		if isThrottleErrorCode(*env.ErrorCode) {
			return domain.ErrBungieThrottled
		}
	}
	if env.ThrottleSeconds != nil && *env.ThrottleSeconds > 0 {
		return domain.ErrBungieThrottled
	}

	if statusCode != 0 && statusCode != http.StatusOK {
		clog.WithGroupField(ctx, logGroup, "statusCode", statusCode)
		switch statusCode {
		case http.StatusServiceUnavailable:
			return domain.ErrDestinyUnavailable
		case http.StatusTooManyRequests:
			return domain.ErrBungieThrottled
		case http.StatusNotFound:
			return domain.ErrActivityNotFound
		}
		if env.ErrorCode != nil && *env.ErrorCode != 0 && *env.ErrorCode != bungieErrorCodeSuccess {
			return fmt.Errorf("bungie api error %d (%s): %s", *env.ErrorCode, ptr.Deref(env.ErrorStatus), ptr.Deref(env.Message))
		}
		return fmt.Errorf("bungie api request failed with status %d", statusCode)
	}

	if env.ErrorCode != nil && *env.ErrorCode != 0 && *env.ErrorCode != bungieErrorCodeSuccess {
		return fmt.Errorf("bungie api error %d (%s): %s", *env.ErrorCode, ptr.Deref(env.ErrorStatus), ptr.Deref(env.Message))
	}

	return nil
}
