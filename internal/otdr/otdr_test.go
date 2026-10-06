package otdr

import (
	"testing"
	"time"
)

func TestProofSetWaitsForConfiguredDirections(t *testing.T) {
	set := NewProofSet()
	east := Proof{ID: "east", Direction: DirectionEastToWest, Passed: true, MeasuredAt: time.Now()}
	result, err := set.Preview(east)
	if err != nil {
		t.Fatal(err)
	}
	if result.Complete {
		t.Fatal("one proof must leave the set incomplete")
	}
	// Preview must not mutate the set.
	if set.Result().Complete || len(set.Result().Proofs) != 0 {
		t.Fatal("preview must not commit a proof")
	}
	result = set.Commit(east)
	if result.Complete {
		t.Fatal("one committed proof must leave the set incomplete")
	}
	west := Proof{ID: "west", Direction: DirectionWestToEast, Passed: true, MeasuredAt: time.Now()}
	if _, err := set.Preview(west); err != nil {
		t.Fatal(err)
	}
	result = set.Commit(west)
	if !result.Complete || !result.Passed {
		t.Fatalf("unexpected proof result: %+v", result)
	}
}

func TestProofSetRejectsUnknownDirection(t *testing.T) {
	set := NewProofSet()
	if _, err := set.Preview(Proof{Direction: "northward"}); err == nil {
		t.Fatal("expected unsupported direction error")
	}
	if len(set.Result().Proofs) != 0 {
		t.Fatal("invalid proof must not be stored")
	}
}

func TestProofSetResetClearsProofs(t *testing.T) {
	set := NewProofSet()
	set.Commit(Proof{ID: "east", Direction: DirectionEastToWest, Passed: true, MeasuredAt: time.Now()})
	set.Reset()
	if result := set.Result(); result.Complete || result.Passed || len(result.Proofs) != 0 {
		t.Fatalf("reset must clear proofs, got %+v", result)
	}
}
