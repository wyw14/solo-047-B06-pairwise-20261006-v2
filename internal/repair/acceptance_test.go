package repair

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/cry-103/internal/platform"
	"github.com/wyw14/cry-103/internal/otdr"
	"github.com/wyw14/cry-103/internal/route"
	"github.com/wyw14/cry-103/internal/span"
	"github.com/wyw14/cry-103/internal/switchover"
	"github.com/wyw14/cry-103/internal/topology"
)

type fixedIDs struct{}

func (fixedIDs) New(prefix string) string { return prefix + "-fixed" }

var testNow = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)

func newAcceptanceService(registry *topology.Registry) (*AcceptanceService, *span.Span, *route.Route) {
	target, _ := span.New("S9", "S9", 42, testNow)
	target.MarkCut(testNow)
	target.StartRepair(testNow)
	target.BeginProof(testNow)
	path, _ := route.New("R-S9", "S9", testNow)
	restorer := switchover.NewRestorer(route.NewDrainer(0), route.NewActivator(registry), registry, fixedIDs{}, func() time.Time { return testNow })
	service := NewAcceptanceService(restorer, func() time.Time { return testNow })
	return service, target, path
}

func passingProof(direction otdr.Direction) otdr.Proof {
	return otdr.Proof{ID: "p-" + string(direction), Direction: direction, Passed: true, LossDB: 0.1, MeasuredAt: testNow}
}

// A failed restore must not commit the second proof or advance the span; the
// next submission retries from a clean, single-proof state.
func TestAddProofRestoreFailureDoesNotPolluteSetOrAdvanceSpan(t *testing.T) {
	registry := topology.NewRegistry() // topology for S9 deliberately missing
	service, target, path := newAcceptanceService(registry)
	service.Register("S9", target, path)

	first, err := service.AddProof(context.Background(), "acc-1", "S9", passingProof(otdr.DirectionEastToWest))
	if err != nil {
		t.Fatal(err)
	}
	if first.Proofs.Complete || len(first.Proofs.Proofs) != 1 {
		t.Fatalf("intermediate proof state wrong: %+v", first.Proofs)
	}

	if _, err := service.AddProof(context.Background(), "acc-1", "S9", passingProof(otdr.DirectionWestToEast)); err == nil {
		t.Fatal("restore must fail without registered topology")
	}
	if state := target.Snapshot().State; state != span.StateProving {
		t.Fatalf("span must stay proving after failed restore, got %s", state)
	}

	// Retrying the same direction must not stack onto a half-committed proof.
	if _, err := service.AddProof(context.Background(), "acc-1", "S9", passingProof(otdr.DirectionWestToEast)); err == nil {
		t.Fatal("restore must still fail")
	}

	// Once the route is restorable, one final submission completes acceptance.
	if _, err := registry.Register("S9", topology.PathPrimary, testNow); err != nil {
		t.Fatal(err)
	}
	accepted, err := service.AddProof(context.Background(), "acc-1", "S9", passingProof(otdr.DirectionWestToEast))
	if err != nil {
		t.Fatalf("retry after topology registered: %v", err)
	}
	if !accepted.Restored || !accepted.Proofs.Complete || !accepted.Proofs.Passed {
		t.Fatalf("acceptance not completed: %+v", accepted)
	}
	if len(accepted.Proofs.Proofs) != 2 {
		t.Fatalf("expected exactly two proofs, got %d", len(accepted.Proofs.Proofs))
	}
	if state := target.Snapshot().State; state != span.StateRecovered {
		t.Fatalf("span must be recovered, got %s", state)
	}
}

// An unregistered span must reject the proof without leaving any residue: no
// acceptance record and no proof set entry.
func TestAddProofUnregisteredSpanLeavesNoResidue(t *testing.T) {
	registry := topology.NewRegistry()
	service, target, path := newAcceptanceService(registry)
	// Intentionally do not Register("S9", ...).

	if _, err := service.AddProof(context.Background(), "acc-2", "S9", passingProof(otdr.DirectionEastToWest)); err == nil {
		t.Fatal("unregistered span must be rejected")
	}
	if _, exists := service.Get("acc-2"); exists {
		t.Fatal("failed submission must not create an acceptance record")
	}
	if _, exists := service.proofSets["acc-2"]; exists {
		t.Fatal("failed submission must not create a proof set")
	}

	// Register and retry: exactly one proof, not a duplicated leftover.
	service.Register("S9", target, path)
	registry.Register("S9", topology.PathPrimary, testNow)
	accepted, err := service.AddProof(context.Background(), "acc-2", "S9", passingProof(otdr.DirectionEastToWest))
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.Proofs.Proofs) != 1 {
		t.Fatalf("expected one clean proof after registration, got %d", len(accepted.Proofs.Proofs))
	}
}

// An optical failure drives the span back to repairing and wipes the proof
// round so a re-splice starts from an empty set.
func TestAddProofOpticalFailureResetsProofRound(t *testing.T) {
	registry := topology.NewRegistry()
	service, target, path := newAcceptanceService(registry)
	service.Register("S9", target, path)

	if _, err := service.AddProof(context.Background(), "acc-3", "S9", passingProof(otdr.DirectionEastToWest)); err != nil {
		t.Fatal(err)
	}
	failing := passingProof(otdr.DirectionWestToEast)
	failing.Passed = false
	if _, err := service.AddProof(context.Background(), "acc-3", "S9", failing); err == nil {
		t.Fatal("failed optical proof must return an error")
	}
	if state := target.Snapshot().State; state != span.StateRepairing {
		t.Fatalf("span must return to repairing, got %s", state)
	}
	if result := service.proofSets["acc-3"].Result(); len(result.Proofs) != 0 || result.Complete {
		t.Fatalf("failed proof round must be discarded, got %+v", result)
	}

	// Re-splice and re-prove from scratch: a single proof is incomplete again.
	if _, err := target.BeginProof(testNow); err != nil {
		t.Fatal(err)
	}
	registry.Register("S9", topology.PathPrimary, testNow)
	restarted, err := service.AddProof(context.Background(), "acc-3", "S9", passingProof(otdr.DirectionEastToWest))
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Proofs.Complete || len(restarted.Proofs.Proofs) != 1 {
		t.Fatalf("new proving round must start with one proof, got %+v", restarted.Proofs)
	}
	finished, err := service.AddProof(context.Background(), "acc-3", "S9", passingProof(otdr.DirectionWestToEast))
	if err != nil {
		t.Fatal(err)
	}
	if !finished.Restored || target.Snapshot().State != span.StateRecovered {
		t.Fatalf("re-proving did not recover the span: %+v state=%s", finished, target.Snapshot().State)
	}
}

// An invalid direction is rejected before any state changes.
func TestAddProofInvalidDirectionRejected(t *testing.T) {
	registry := topology.NewRegistry()
	service, target, path := newAcceptanceService(registry)
	service.Register("S9", target, path)

	if _, err := service.AddProof(context.Background(), "acc-4", "S9", otdr.Proof{Direction: "northward"}); err == nil {
		t.Fatal("invalid direction must be rejected")
	}
	if _, exists := service.Get("acc-4"); exists {
		t.Fatal("invalid direction must not create an acceptance record")
	}
	if state := target.Snapshot().State; state != span.StateProving {
		t.Fatalf("span state must not change, got %s", state)
	}
}

var _ platform.IDGenerator = fixedIDs{}
