package domain

import "testing"

func testCompProgressionManifest() ProgressionManifest {
	return ProgressionManifest{
		Hash: 3696598664,
		Name: "Competitive Division",
		Steps: []ProgressionStep{
			{StepName: "Untested", ProgressTotal: 1, Icon: "/icons/untested.png"},
			{StepName: "Copper III", ProgressTotal: 374, Icon: "/icons/copper.png"},
			{StepName: "Copper II", ProgressTotal: 375, Icon: "/icons/copper.png"},
			{StepName: "Copper I", ProgressTotal: 500, Icon: "/icons/copper.png"},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: "/icons/copper.png"},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: "/icons/bronze.png"},
			{StepName: "Bronze III", ProgressTotal: 500, Icon: "/icons/bronze.png"},
			{StepName: "Bronze II", ProgressTotal: 500, Icon: "/icons/bronze.png"},
			{StepName: "Bronze I", ProgressTotal: 500, Icon: "/icons/bronze.png"},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: "/icons/bronze.png"},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: "/icons/silver.png"},
			{StepName: "Silver III", ProgressTotal: 500, Icon: "/icons/silver.png"},
			{StepName: "Silver II", ProgressTotal: 500, Icon: "/icons/silver.png"},
			{StepName: "Silver I", ProgressTotal: 500, Icon: "/icons/silver.png"},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: "/icons/silver.png"},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: "/icons/gold.png"},
			{StepName: "Gold III", ProgressTotal: 500, Icon: "/icons/gold.png"},
			{StepName: "Gold II", ProgressTotal: 500, Icon: "/icons/gold.png"},
			{StepName: "Gold I", ProgressTotal: 500, Icon: "/icons/gold.png"},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: "/icons/gold.png"},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: "/icons/platinum.png"},
			{StepName: "Platinum III", ProgressTotal: 500, Icon: "/icons/platinum.png"},
			{StepName: "Platinum II", ProgressTotal: 500, Icon: "/icons/platinum.png"},
			{StepName: "Platinum I", ProgressTotal: 499, Icon: "/icons/platinum.png"},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: "/icons/platinum.png"},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: "/icons/adept.png"},
			{StepName: "Adept III", ProgressTotal: 499, Icon: "/icons/adept.png"},
			{StepName: "Adept II", ProgressTotal: 500, Icon: "/icons/adept.png"},
			{StepName: "Adept I", ProgressTotal: 499, Icon: "/icons/adept.png"},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: "/icons/adept.png"},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: "/icons/ascendant.png"},
			{StepName: "Ascendant III", ProgressTotal: 499, Icon: "/icons/ascendant.png"},
			{StepName: "Ascendant II", ProgressTotal: 375, Icon: "/icons/ascendant.png"},
			{StepName: "Ascendant I", ProgressTotal: 375, Icon: "/icons/ascendant.png"},
			{StepName: "Ascendant I", ProgressTotal: 1, Icon: "/icons/ascendant.png"},
			{StepName: "Ascendant 0", ProgressTotal: 4999, Icon: "/icons/ascendant.png"},
		},
	}
}

func TestProgressionManifest_StepForPoints(t *testing.T) {
	compDef := testCompProgressionManifest()

	compCases := []struct {
		points   int
		wantName string
		wantIcon string
	}{
		{points: -50, wantName: "Untested", wantIcon: "/icons/untested.png"},
		{points: 0, wantName: "Untested", wantIcon: "/icons/untested.png"},
		{points: 1, wantName: "Copper III", wantIcon: "/icons/copper.png"},
		{points: 374, wantName: "Copper III", wantIcon: "/icons/copper.png"},
		{points: 375, wantName: "Copper II", wantIcon: "/icons/copper.png"},
		{points: 5820, wantName: "Platinum III", wantIcon: "/icons/platinum.png"},
		{points: 7249, wantName: "Platinum I", wantIcon: "/icons/platinum.png"},
		{points: 7250, wantName: "Adept III", wantIcon: "/icons/adept.png"},
		{points: 10000, wantName: "Ascendant I", wantIcon: "/icons/ascendant.png"},
		{points: 10001, wantName: "Ascendant 0", wantIcon: "/icons/ascendant.png"},
		{points: 16000, wantName: "Ascendant 0", wantIcon: "/icons/ascendant.png"},
	}

	for _, tc := range compCases {
		step, ok := compDef.StepForPoints(tc.points)
		if !ok || step.StepName != tc.wantName || step.Icon != tc.wantIcon {
			t.Fatalf("points=%d: expected (%q, %q), got %+v (ok=%v)", tc.points, tc.wantName, tc.wantIcon, step, ok)
		}
	}

	crucibleDef := ProgressionManifest{
		Hash: 2083746873,
		Steps: []ProgressionStep{
			{StepName: "GUARDIAN I", ProgressTotal: 50, Icon: "/icons/g1.png"},
			{StepName: "GUARDIAN II", ProgressTotal: 75, Icon: "/icons/g2.png"},
			{StepName: "LEGEND", ProgressTotal: 1000, Icon: "/icons/legend.png"},
		},
	}
	if s, ok := crucibleDef.StepForPoints(25); !ok || s.StepName != "GUARDIAN I" {
		t.Fatalf("expected GUARDIAN I at 25 points, got %+v", s)
	}
	if s, ok := crucibleDef.StepForPoints(60); !ok || s.StepName != "GUARDIAN II" {
		t.Fatalf("expected GUARDIAN II at 60 points, got %+v", s)
	}
	if s, ok := crucibleDef.StepForPoints(5000); !ok || s.StepName != "LEGEND" {
		t.Fatalf("expected LEGEND at overflow points, got %+v", s)
	}

	var empty ProgressionManifest
	if _, ok := empty.StepForPoints(100); ok {
		t.Fatal("expected ok=false for empty progression")
	}
}

func TestRecordManifest_ResolvedTitle(t *testing.T) {
	if got := (RecordManifest{Name: "Fallback", MaleTitle: "Flawless", FemaleTitle: "FlawlessF"}).ResolvedTitle(); got != "Flawless" {
		t.Fatalf("got %q, want Flawless", got)
	}
	if got := (RecordManifest{Name: "Fallback", FemaleTitle: "Glorious"}).ResolvedTitle(); got != "Glorious" {
		t.Fatalf("got %q, want Glorious", got)
	}
	if got := (RecordManifest{Name: "Iron Lord"}).ResolvedTitle(); got != "Iron Lord" {
		t.Fatalf("got %q, want Iron Lord", got)
	}
}
