package destiny

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"oneTrick/clients/bungie"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

func TestFetchEquippedItems_ConcurrentPreservesOrderAndBoundsParallelism(t *testing.T) {
	var inFlight atomic.Int32
	var maxSeen atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			prev := maxSeen.Load()
			if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer inFlight.Add(-1)

		time.Sleep(15 * time.Millisecond)

		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		instanceID := parts[len(parts)-1]
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"Response":{"item":{"data":{"itemHash":1001,"itemInstanceId":"%s"}}}}`, instanceID)
	}))
	defer srv.Close()

	client, err := bungie.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses failed: %v", err)
	}
	svc := &service{Client: client}

	items := make([]bungie.ItemComponent, 0, 10)
	// Include one item without an instance ID to ensure it is skipped cleanly.
	items = append(items, bungie.ItemComponent{})
	for i := 1; i <= 9; i++ {
		id := fmt.Sprintf("%d", 1000+i)
		override := uint32(9000 + i)
		items = append(items, bungie.ItemComponent{
			ItemInstanceId:        &id,
			OverrideStyleItemHash: &override,
		})
	}

	fetched, err := svc.fetchEquippedItems(clog.NewContext(context.Background()), 12345, 3, items)
	if err != nil {
		t.Fatalf("fetchEquippedItems failed: %v", err)
	}
	if len(fetched) != 9 {
		t.Fatalf("expected 9 fetched items, got %d", len(fetched))
	}
	for i, f := range fetched {
		wantID := fmt.Sprintf("%d", 1001+i)
		if f.instanceID != wantID {
			t.Fatalf("index %d: expected instanceID=%s, got %s", i, wantID, f.instanceID)
		}
		wantOverride := uint32(9001 + i)
		if got := f.component().OverrideStyleItemHash; got == nil || *got != wantOverride {
			t.Fatalf("index %d: expected fallback override=%d, got %v", i, wantOverride, got)
		}
	}
	if maxSeen.Load() > maxConcurrentItemFetches {
		t.Fatalf("expected concurrency <= %d, observed %d", maxConcurrentItemFetches, maxSeen.Load())
	}
	if maxSeen.Load() < 2 {
		t.Fatalf("expected concurrent execution (>1 in-flight), observed max %d", maxSeen.Load())
	}
}

func TestFetchEquippedItems_ReturnsErrorOnItemFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/2002/") || strings.HasSuffix(r.URL.Path, "/2002") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"Response":{"item":{"data":{"itemHash":1001}}}}`)
	}))
	defer srv.Close()

	client, err := bungie.NewClientWithResponses(srv.URL)
	if err != nil {
		t.Fatalf("NewClientWithResponses failed: %v", err)
	}
	svc := &service{Client: client}

	items := []bungie.ItemComponent{
		{ItemInstanceId: new("2001")},
		{ItemInstanceId: new("2002")},
		{ItemInstanceId: new("2003")},
	}

	_, err = svc.fetchEquippedItems(clog.NewContext(context.Background()), 12345, 3, items)
	if err == nil {
		t.Fatal("expected error when one item fetch fails")
	}
	if !strings.Contains(err.Error(), "2002") {
		t.Fatalf("expected error to identify failing instance 2002, got: %v", err)
	}
}
