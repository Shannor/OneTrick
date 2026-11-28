package httpadapter

import (
	"oneTrick/api"
	"oneTrick/core/domain"
)

func ToAPICharacters(chars []domain.Character) []api.Character {
	out := make([]api.Character, 0, len(chars))
	for _, c := range chars {
		out = append(out, ToAPICharacter(c))
	}
	return out
}

func ToActivityQueryMode(mode api.ActivityMode) domain.ActivityQueryMode {
	switch mode {
	case api.Competitive:
		return domain.ActivityQueryCompetitive
	case api.Quickplay:
		return domain.ActivityQueryQuickplay
	case api.IronBanner:
		return domain.ActivityQueryIronBanner
	default:
		return domain.ActivityQueryAllPvP
	}
}

func ToAPIActivityHistory(info domain.ActivityInfo) api.ActivityHistory {
	return toAPIActivityHistory(info)
}

func ToAPIActivityHistories(infos []domain.ActivityInfo) []api.ActivityHistory {
	out := make([]api.ActivityHistory, 0, len(infos))
	for _, info := range infos {
		out = append(out, toAPIActivityHistory(info))
	}
	return out
}

func MaskOAuthCode(code string) string {
	if len(code) == 0 {
		return "<empty>"
	}
	if len(code) <= 8 {
		return "***"
	}
	return code[:4] + "..." + code[len(code)-4:]
}
