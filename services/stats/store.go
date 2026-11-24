package stats

import "context"

type CharacterMatchStat struct {
	SnapshotID string
	Kills      int
	Deaths     int
	Assists    int
	Won        bool
}

type Store interface {
	ListCharacterMatchStats(ctx context.Context, characterID string, gameMode string) ([]CharacterMatchStat, error)
}
