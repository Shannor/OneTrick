package destiny

import (
	"context"
	"sync/atomic"
	"testing"
)

type spyManifestService struct {
	ManifestService
	classesCalls      atomic.Int32
	racesCalls        atomic.Int32
	modesCalls        atomic.Int32
	recordCalls       atomic.Int32
	itemCalls         atomic.Int32
	activityCalls     atomic.Int32
	activityModeCalls atomic.Int32
	progressionsCalls atomic.Int32
	progressionCalls  atomic.Int32
}

func (s *spyManifestService) GetClasses(_ context.Context) (map[string]ClassDefinition, error) {
	s.classesCalls.Add(1)
	return map[string]ClassDefinition{"671679327": {Hash: 671679327}}, nil
}

func (s *spyManifestService) GetRaces(_ context.Context) (map[string]RaceDefinition, error) {
	s.racesCalls.Add(1)
	return map[string]RaceDefinition{"3887404748": {Hash: 3887404748}}, nil
}

func (s *spyManifestService) GetActivityModes(_ context.Context) (map[string]ActivityModeDefinition, error) {
	s.modesCalls.Add(1)
	return map[string]ActivityModeDefinition{"84": {Hash: 84}}, nil
}

func (s *spyManifestService) GetProgressions(_ context.Context) (map[string]ProgressionDefinition, error) {
	s.progressionsCalls.Add(1)
	return map[string]ProgressionDefinition{
		"2083746873": {Hash: 2083746873, Steps: []ProgressionStepDefinition{{StepName: "GUARDIAN I", ProgressTotal: 50}}},
	}, nil
}

func (s *spyManifestService) GetRecord(_ context.Context, hash int64) (*RecordDefinition, error) {
	s.recordCalls.Add(1)
	return &RecordDefinition{Hash: int(hash)}, nil
}

func (s *spyManifestService) GetItem(_ context.Context, hash int64) (*ItemDefinition, error) {
	s.itemCalls.Add(1)
	return &ItemDefinition{Hash: hash}, nil
}

func (s *spyManifestService) GetActivity(_ context.Context, hash int64) (*ActivityDefinition, error) {
	s.activityCalls.Add(1)
	return &ActivityDefinition{Hash: int(hash)}, nil
}

func (s *spyManifestService) GetActivityMode(_ context.Context, hash int64) (*ActivityModeDefinition, error) {
	s.activityModeCalls.Add(1)
	return &ActivityModeDefinition{Hash: hash}, nil
}

func (s *spyManifestService) GetProgression(_ context.Context, hash int64) (*ProgressionDefinition, error) {
	s.progressionCalls.Add(1)
	return &ProgressionDefinition{Hash: hash, Steps: []ProgressionStepDefinition{{StepName: "Copper III", ProgressTotal: 374}}}, nil
}

func TestCachedManifestServiceWrapper(t *testing.T) {
	ctx := context.Background()
	spy := &spyManifestService{}
	svc := NewCachedManifestService(spy)

	// 1. Collection methods should call the inner service on first miss and serve from cache on subsequent calls
	for i := 0; i < 3; i++ {
		classes, err := svc.GetClasses(ctx)
		if err != nil || len(classes) != 1 {
			t.Fatalf("expected 1 cached class, got len=%d err=%v", len(classes), err)
		}
		races, err := svc.GetRaces(ctx)
		if err != nil || len(races) != 1 {
			t.Fatalf("expected 1 cached race, got len=%d err=%v", len(races), err)
		}
	}
	if spy.classesCalls.Load() != 1 {
		t.Fatalf("expected inner GetClasses called once, got %d", spy.classesCalls.Load())
	}
	if spy.racesCalls.Load() != 1 {
		t.Fatalf("expected inner GetRaces called once, got %d", spy.racesCalls.Load())
	}

	// 2. Hash lookup methods should cache per hash and only call the inner service once per unique hash
	for i := 0; i < 3; i++ {
		rec, err := svc.GetRecord(ctx, 3464275895)
		if err != nil || rec == nil || rec.Hash != 3464275895 {
			t.Fatalf("expected record 3464275895, got %+v err=%v", rec, err)
		}
		item, err := svc.GetItem(ctx, 1001)
		if err != nil || item == nil || item.Hash != 1001 {
			t.Fatalf("expected item 1001, got %+v err=%v", item, err)
		}
		act, err := svc.GetActivity(ctx, 2002)
		if err != nil || act == nil || act.Hash != 2002 {
			t.Fatalf("expected activity 2002, got %+v err=%v", act, err)
		}
		prog, err := svc.GetProgression(ctx, CompProgressionHashInt64)
		if err != nil || prog == nil || prog.Hash != CompProgressionHashInt64 {
			t.Fatalf("expected progression %d, got %+v err=%v", CompProgressionHashInt64, prog, err)
		}
	}
	if spy.recordCalls.Load() != 1 {
		t.Fatalf("expected inner GetRecord called once for hash 3464275895, got %d", spy.recordCalls.Load())
	}
	if spy.itemCalls.Load() != 1 {
		t.Fatalf("expected inner GetItem called once for hash 1001, got %d", spy.itemCalls.Load())
	}
	if spy.activityCalls.Load() != 1 {
		t.Fatalf("expected inner GetActivity called once for hash 2002, got %d", spy.activityCalls.Load())
	}
	if spy.progressionCalls.Load() != 1 {
		t.Fatalf("expected inner GetProgression called once for hash %d, got %d", CompProgressionHashInt64, spy.progressionCalls.Load())
	}

	// 3. Once GetActivityModes / GetProgressions are cached, hash lookups resolve from the collection cache without calling inner single-doc methods
	if _, err := svc.GetActivityModes(ctx); err != nil {
		t.Fatalf("GetActivityModes failed: %v", err)
	}
	mode, err := svc.GetActivityMode(ctx, 84)
	if err != nil || mode == nil || mode.Hash != 84 {
		t.Fatalf("expected activity mode 84, got %+v err=%v", mode, err)
	}
	if spy.activityModeCalls.Load() != 0 {
		t.Fatalf("expected GetActivityMode(84) to resolve from cached activityModes without calling inner GetActivityMode, got %d calls", spy.activityModeCalls.Load())
	}

	if _, err := svc.GetProgressions(ctx); err != nil {
		t.Fatalf("GetProgressions failed: %v", err)
	}
	crucibleProg, err := svc.GetProgression(ctx, 2083746873)
	if err != nil || crucibleProg == nil || crucibleProg.Hash != 2083746873 {
		t.Fatalf("expected progression 2083746873, got %+v err=%v", crucibleProg, err)
	}
	if spy.progressionCalls.Load() != 1 {
		t.Fatalf("expected GetProgression(2083746873) to resolve from cached progressions without additional inner GetProgression call, got %d calls", spy.progressionCalls.Load())
	}
}
