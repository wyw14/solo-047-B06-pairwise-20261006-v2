package repair

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wyw14/cry-103/internal/otdr"
	"github.com/wyw14/cry-103/internal/route"
	"github.com/wyw14/cry-103/internal/span"
	"github.com/wyw14/cry-103/internal/switchover"
)

type Acceptance struct {
	ID        string           `json:"id"`
	SpanID    string           `json:"span_id"`
	Proofs    otdr.ProofResult `json:"proofs"`
	State     span.State       `json:"state"`
	Restored  bool             `json:"restored"`
	UpdatedAt time.Time        `json:"updated_at"`
}

type AcceptanceService struct {
	mu          sync.Mutex
	proofSets   map[string]*otdr.ProofSet
	acceptances map[string]Acceptance
	spans       map[string]*span.Span
	routes      map[string]*route.Route
	restorer    *switchover.Restorer
	now         func() time.Time
}

func NewAcceptanceService(restorer *switchover.Restorer, now func() time.Time) *AcceptanceService {
	return &AcceptanceService{
		proofSets:   make(map[string]*otdr.ProofSet),
		acceptances: make(map[string]Acceptance),
		spans:       make(map[string]*span.Span),
		routes:      make(map[string]*route.Route),
		restorer:    restorer,
		now:         now,
	}
}

func (s *AcceptanceService) Register(spanID string, target *span.Span, route *route.Route) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spans[spanID] = target
	s.routes[spanID] = route
}

func (s *AcceptanceService) AddProof(ctx context.Context, acceptanceID, spanID string, proof otdr.Proof) (Acceptance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Phase 1: validate preconditions. Nothing may be mutated before the
	// span and route are known to be registered.
	target := s.spans[spanID]
	path := s.routes[spanID]
	if target == nil || path == nil {
		return Acceptance{}, fmt.Errorf("acceptance span %s is not registered", spanID)
	}

	// Phase 2: preview the proof result without touching the set. The proof
	// is only committed after every side effect below has succeeded, so a
	// failed restore cannot leave a half-written proof for the next attempt.
	set := s.proofSets[acceptanceID]
	if set == nil {
		set = otdr.NewProofSet()
		s.proofSets[acceptanceID] = set
	}
	result, err := set.Preview(proof)
	if err != nil {
		return Acceptance{}, err
	}

	if !result.Complete {
		// Intermediate proof: no external action can fail here, commit it.
		result = set.Commit(proof)
		acceptance := Acceptance{ID: acceptanceID, SpanID: spanID, Proofs: result, State: target.Snapshot().State, UpdatedAt: s.now().UTC()}
		s.acceptances[acceptanceID] = acceptance
		return acceptance, nil
	}

	// Both directions are in: the span must currently be proving, otherwise
	// neither rejection nor recovery is a legal transition.
	if target.Snapshot().State != span.StateProving {
		return Acceptance{}, fmt.Errorf("span %s is not in proving state", spanID)
	}

	if !result.Passed {
		// Optical failure: push the span back to repair and discard the
		// failed round so the next splice starts from an empty proof set.
		if _, err := target.RejectProof(s.now()); err != nil {
			return Acceptance{}, err
		}
		set.Reset()
		acceptance := Acceptance{ID: acceptanceID, SpanID: spanID, Proofs: result, State: target.Snapshot().State, UpdatedAt: s.now().UTC()}
		s.acceptances[acceptanceID] = acceptance
		return acceptance, fmt.Errorf("splice optical proof failed")
	}

	// Phase 3: perform the recovery side effects. The span state is advanced
	// only after the route restore succeeds, and the proof is committed only
	// once both have succeeded — a failure anywhere rolls the proof set back
	// to just the previously accepted intermediate proofs.
	if _, err := s.restorer.Restore(ctx, path); err != nil {
		return Acceptance{}, err
	}
	if _, err := target.Recover(s.now()); err != nil {
		return Acceptance{}, err
	}

	result = set.Commit(proof)
	acceptance := Acceptance{ID: acceptanceID, SpanID: spanID, Proofs: result, State: target.Snapshot().State, Restored: true, UpdatedAt: s.now().UTC()}
	s.acceptances[acceptanceID] = acceptance
	return acceptance, nil
}

func (s *AcceptanceService) Get(id string) (Acceptance, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, exists := s.acceptances[id]
	return value, exists
}
