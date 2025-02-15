package snapshot

import (
	"errors"
	"time"
)

var NotFound = errors.New("not found")

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
