package gamemode

import "strings"

var quickplayModesAndActivities = []string{
	"Control Quickplay",
	"Control",
	"Zone Control",
	"Control: Matchmade",
	"Zone Control: Matchmade",
	"Cutting Edge: Zone Control: Matchmade",
	"Clash",
	"Clash Quickplay",
	"Clash 3v3: Matchmade",
}

// EstimateName classifies an activity into a canonical game mode string using the priority order:
// ironBanner -> trials -> competitive -> quickplay, returning "" if unclassifiable.
func EstimateName(mode, activity string) string {
	m := strings.TrimSpace(mode)
	act := strings.TrimSpace(activity)

	if isIronBannerActivity(m, act) {
		return "ironBanner"
	}
	if isTrialsActivity(m, act) {
		return "trials"
	}
	if isCompetitiveActivity(m, act) {
		return "competitive"
	}
	if isQuickplayActivity(m, act) {
		return "quickplay"
	}
	return ""
}

func isIronBannerActivity(mode, activity string) bool {
	for _, s := range []string{mode, activity} {
		lower := strings.ToLower(s)
		if strings.Contains(lower, "iron banner") ||
			strings.Contains(lower, "eruption") ||
			strings.Contains(lower, "fortress") ||
			strings.Contains(lower, "tribute") {
			return true
		}
	}
	return false
}

func isCompetitiveActivity(mode, activity string) bool {
	if strings.Contains(strings.ToLower(activity), "competitive") ||
		strings.Contains(strings.ToLower(mode), "competitive") {
		return true
	}
	switch strings.ToLower(mode) {
	case "survival", "control competitive", "clash competitive", "competitive", "competitive: matchmade":
		return true
	}
	switch strings.ToLower(activity) {
	case "survival", "control competitive", "clash competitive", "competitive", "competitive: matchmade":
		return true
	}
	return false
}

func isTrialsActivity(mode, activity string) bool {
	for _, s := range []string{mode, activity} {
		lower := strings.ToLower(s)
		if strings.Contains(lower, "trials of osiris") ||
			strings.EqualFold(s, "Trials Survival") ||
			strings.EqualFold(s, "Trials Countdown") {
			return true
		}
	}
	return false
}

func isQuickplayActivity(mode, activity string) bool {
	for _, qp := range quickplayModesAndActivities {
		if strings.EqualFold(mode, qp) || strings.EqualFold(activity, qp) {
			return true
		}
	}
	return false
}
