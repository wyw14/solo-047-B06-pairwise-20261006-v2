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

func validateProof(proof Proof) error {
	if proof.Direction != DirectionEastToWest && proof.Direction != DirectionWestToEast {
		return fmt.Errorf("unsupported proof direction %q", proof.Direction)
	}
	return nil
}

// Preview returns the result the set would have if proof were committed,
// without storing it. Callers must only Commit once the surrounding
// transaction (route restore, span transition) has succeeded.
func (s *ProofSet) Preview(proof Proof) (ProofResult, error) {
	if err := validateProof(proof); err != nil {
		return ProofResult{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resultWith(proof), nil
}

// Commit stores a proof that was already accepted via Preview and returns the
// resulting set state.
func (s *ProofSet) Commit(proof Proof) ProofResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proofs[proof.Direction] = proof
	return resultOf(s.proofs)
}

// Reset discards every stored proof so a rejected splice can start a fresh
// proving round.
func (s *ProofSet) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.proofs = make(map[Direction]Proof)
}

func (s *ProofSet) resultWith(overlay Proof) ProofResult {
	merged := make(map[Direction]Proof, len(s.proofs)+1)
	for direction, proof := range s.proofs {
		merged[direction] = proof
	}
	merged[overlay.Direction] = overlay
	return resultOf(merged)
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
