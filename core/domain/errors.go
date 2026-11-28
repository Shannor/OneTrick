package domain

import "errors"

var (
	ErrDestinyUnavailable = errors.New("destiny 2 servers unavailable")
	ErrPrivateProfile     = errors.New("destiny profile component is private")
	ErrBungieThrottled    = errors.New("bungie api request throttled")
	ErrActivityNotFound   = errors.New("activity not found")
	ErrInvalidActivityID  = errors.New("invalid activity id")
)
