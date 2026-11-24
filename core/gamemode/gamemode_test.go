package gamemode

import "testing"

func TestEstimateName(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		activity string
		want     string
	}{
		{name: "iron banner wins over quickplay mode", mode: "Control", activity: "Iron Banner: Eruption", want: "ironBanner"},
		{name: "iron banner fortress", mode: "", activity: "Fortress", want: "ironBanner"},
		{name: "trials of osiris", mode: "Trials of Osiris", activity: "Burnout", want: "trials"},
		{name: "trials survival case insensitive", mode: "  trials survival ", activity: "", want: "trials"},
		{name: "competitive survival", mode: "Survival", activity: "Javelin-4", want: "competitive"},
		{name: "competitive activity match", mode: "Clash", activity: "Competitive: Matchmade", want: "competitive"},
		{name: "quickplay control", mode: "Control", activity: "Midtown", want: "quickplay"},
		{name: "quickplay cutting edge zone control", mode: "", activity: "Cutting Edge: Zone Control: Matchmade", want: "quickplay"},
		{name: "unknown mode returns empty", mode: "Private Matches", activity: "Custom", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateName(tc.mode, tc.activity); got != tc.want {
				t.Fatalf("EstimateName(%q, %q) = %q, want %q", tc.mode, tc.activity, got, tc.want)
			}
		})
	}
}
