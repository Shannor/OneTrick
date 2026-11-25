package snapshot

import (
	"errors"
	"time"
)

var (
	NotFound     = errors.New("not found")
	Unauthorized = errors.New("unauthorized")
	InvalidMerge = errors.New("invalid merge")
)

type History struct {
	ID          string    `json:"id" firestore:"id"`
	UserID      string    `json:"userId" firestore:"userId"`
	CharacterID string    `json:"characterId" firestore:"characterId"`
	ParentID    string    `json:"parentId" firestore:"parentId"`
	Timestamp   time.Time `json:"timestamp" firestore:"timestamp"`
	Meta        MetaData  `json:"meta" firestore:"meta"`
}

type MetaData struct {
	KineticID string `json:"kineticId" firestore:"kineticId"`
	EnergyID  string `json:"energyId" firestore:"energyId"`
	PowerID   string `json:"powerId" firestore:"powerId"`
}

type SessionCheckInOptions struct {
	MembershipType         int64
	LastSnapshotID         string
	LastSnapshotHash       string
	LastSnapshotRecordedAt time.Time
	HeartbeatInterval      time.Duration
}

type SessionCheckInResult struct {
	SnapshotID      string
	SnapshotHash    string
	HistoryRecorded bool
	RecordedAt      time.Time
}
