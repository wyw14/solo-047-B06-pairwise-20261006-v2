package repair

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/cry-103/internal/otdr"
	"github.com/wyw14/cry-103/internal/platform"
	"github.com/wyw14/cry-103/internal/route"
	"github.com/wyw14/cry-103/internal/span"
	"github.com/wyw14/cry-103/internal/switchover"
	"github.com/wyw14/cry-103/internal/topology"
)

func acceptanceFixture(t *testing.T, registerTopology bool) (*AcceptanceService, *span.Span, *topology.Registry) {
	t.Helper()
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	registry := topology.NewRegistry()
	target, err := span.New("S1", "S1", 120, now)
	if err != nil {
		t.Fatal(err)
	}
	path, err := route.New("R-S1", "S1", now)
	if err != nil {
		t.Fatal(err)
	}
	if registerTopology {
		if _, err := registry.Register("S1", topology.PathProtection, now); err != nil {
			t.Fatal(err)
		}
	}
	restorer := switchover.NewRestorer(route.NewDrainer(0), route.NewActivator(registry), registry, platform.UUIDGenerator{}, clock)
	service := NewAcceptanceService(restorer, clock)
	service.Register("S1", target, path)
	return service, target, registry
}

func provingSpan(t *testing.T, target *span.Span, now time.Time) {
	t.Helper()
	if _, err := target.MarkCut(now); err != nil {
		t.Fatal(err)
	}
	if _, err := target.StartRepair(now); err != nil {
		t.Fatal(err)
	}
	if _, err := target.BeginProof(now); err != nil {
		t.Fatal(err)
	}
}

func TestAddProofRejectsUnregisteredSpanWithoutStoringProof(t *testing.T) {
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	service := NewAcceptanceService(nil, func() time.Time { return now })
	proof := otdr.Proof{ID: "p1", Direction: otdr.DirectionEastToWest, Passed: true, MeasuredAt: now}
	if _, err := service.AddProof(context.Background(), "A1", "S-unknown", proof); err == nil {
		t.Fatal("unregistered span must be rejected")
	}
	if _, exists := service.Get("A1"); exists {
		t.Fatal("rejected submission must not record an acceptance")
	}
}

func TestAddProofKeepsProofSetWhenRestoreFails(t *testing.T) {
	service, target, registry := acceptanceFixture(t, false)
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	provingSpan(t, target, now)

	east := otdr.Proof{ID: "p1", Direction: otdr.DirectionEastToWest, Passed: true, MeasuredAt: now}
	if _, err := service.AddProof(context.Background(), "A1", "S1", east); err != nil {
		t.Fatal(err)
	}
	west := otdr.Proof{ID: "p2", Direction: otdr.DirectionWestToEast, Passed: true, MeasuredAt: now}
	if _, err := service.AddProof(context.Background(), "A1", "S1", west); err == nil {
		t.Fatal("restore without topology must fail")
	}
	if state := target.Snapshot().State; state != span.StateProving {
		t.Fatalf("failed restore must not advance span state, got %s", state)
	}
	acceptance, exists := service.Get("A1")
	if !exists {
		t.Fatal("first proof acceptance should be recorded")
	}
	if acceptance.Proofs.Complete {
		t.Fatal("failed completion must not commit the second proof to the set")
	}
	// A polluted set would still hold the west proof and complete here.
	again, err := service.AddProof(context.Background(), "A1", "S1", east)
	if err != nil {
		t.Fatalf("resubmitting the first direction must not trigger restore: %v", err)
	}
	if again.Proofs.Complete {
		t.Fatal("failed completion left a half-written proof in the set")
	}

	if _, err := registry.Register("S1", topology.PathProtection, now); err != nil {
		t.Fatal(err)
	}
	retry, err := service.AddProof(context.Background(), "A1", "S1", west)
	if err != nil {
		t.Fatalf("retry after fixing topology must succeed: %v", err)
	}
	if !retry.Restored || target.Snapshot().State != span.StateRecovered {
		t.Fatalf("expected restored acceptance, got %+v state=%s", retry, target.Snapshot().State)
	}
}

func TestAddProofRestoresWhenProofsPass(t *testing.T) {
	service, target, _ := acceptanceFixture(t, true)
	now := time.Date(2026, 8, 24, 3, 0, 0, 0, time.UTC)
	provingSpan(t, target, now)

	east := otdr.Proof{ID: "p1", Direction: otdr.DirectionEastToWest, Passed: true, MeasuredAt: now}
	if _, err := service.AddProof(context.Background(), "A1", "S1", east); err != nil {
		t.Fatal(err)
	}
	west := otdr.Proof{ID: "p2", Direction: otdr.DirectionWestToEast, Passed: true, MeasuredAt: now}
	acceptance, err := service.AddProof(context.Background(), "A1", "S1", west)
	if err != nil {
		t.Fatal(err)
	}
	if !acceptance.Restored || !acceptance.Proofs.Complete || !acceptance.Proofs.Passed {
		t.Fatalf("unexpected acceptance: %+v", acceptance)
	}
	if state := target.Snapshot().State; state != span.StateRecovered {
		t.Fatalf("span should be recovered, got %s", state)
	}
}
