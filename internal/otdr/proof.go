package otdr

import (
	"fmt"
	"sync"
	"time"
)

type Direction string

const (
	DirectionEastToWest Direction = "east-to-west"
	DirectionWestToEast Direction = "west-to-east"
)

type Proof struct {
	ID         string    `json:"id"`
	Direction  Direction `json:"direction"`
	Passed     bool      `json:"passed"`
	LossDB     float64   `json:"loss_db"`
	MeasuredAt time.Time `json:"measured_at"`
	Message    string    `json:"message"`
}

type ProofResult struct {
	Complete bool    `json:"complete"`
	Passed   bool    `json:"passed"`
	Proofs   []Proof `json:"proofs"`
}

type ProofSet struct {
	mu     sync.RWMutex
	proofs map[Direction]Proof
}

func NewProofSet() *ProofSet {
	return &ProofSet{proofs: make(map[Direction]Proof)}
}

func (s *ProofSet) Add(proof Proof) (ProofResult, error) {
	if proof.Direction != DirectionEastToWest && proof.Direction != DirectionWestToEast {
		return ProofResult{}, fmt.Errorf("unsupported proof direction %q", proof.Direction)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proofs[proof.Direction] = proof
	return resultOf(s.proofs), nil
}

// Preview validates proof and reports the result Add would produce without
// recording it, so callers can finish checks and side effects before the
// proof joins the set.
func (s *ProofSet) Preview(proof Proof) (ProofResult, error) {
	if proof.Direction != DirectionEastToWest && proof.Direction != DirectionWestToEast {
		return ProofResult{}, fmt.Errorf("unsupported proof direction %q", proof.Direction)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	merged := make(map[Direction]Proof, len(s.proofs)+1)
	for direction, existing := range s.proofs {
		merged[direction] = existing
	}
	merged[proof.Direction] = proof
	return resultOf(merged), nil
}

func (s *ProofSet) Result() ProofResult {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return resultOf(s.proofs)
}

func resultOf(proofs map[Direction]Proof) ProofResult {
	result := ProofResult{Passed: true}
	for _, direction := range []Direction{DirectionEastToWest, DirectionWestToEast} {
		proof, exists := proofs[direction]
		if !exists {
			result.Passed = false
			continue
		}
		result.Proofs = append(result.Proofs, proof)
		if !proof.Passed {
			result.Passed = false
		}
	}
	result.Complete = len(result.Proofs) == 2
	return result
}
