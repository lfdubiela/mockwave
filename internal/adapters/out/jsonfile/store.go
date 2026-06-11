package jsonfile

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/mockwave/mockwave/domain"
)

type Store struct {
	mu     sync.RWMutex
	path   string
	config domain.Config
}

// NewMemStore creates an in-memory store pre-loaded with cfg. No file I/O.
// Intended for tests — write operations (SaveRule, SaveSimulation, etc.) will
// fail because path is empty, but read operations work fine.
func NewMemStore(cfg domain.Config) *Store {
	return &Store{config: cfg}
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("jsonfile: open %s: %w", s.path, err)
	}
	defer f.Close()
	var cfg domain.Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return fmt.Errorf("jsonfile: decode %s: %w", s.path, err)
	}
	s.config = cfg
	return nil
}

func (s *Store) flush() error {
	f, err := os.Create(s.path)
	if err != nil {
		return fmt.Errorf("jsonfile: write %s: %w", s.path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s.config)
}

func (s *Store) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) GetRules() ([]domain.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Rule, len(s.config.Rules))
	copy(out, s.config.Rules)
	return out, nil
}

func (s *Store) GetSimulation(id string) (*domain.Simulation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.config.Simulations {
		if s.config.Simulations[i].ID == id {
			sim := s.config.Simulations[i]
			return &sim, nil
		}
	}
	return nil, fmt.Errorf("jsonfile: simulation %q not found", id)
}

func (s *Store) ListSimulations() ([]domain.Simulation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Simulation, len(s.config.Simulations))
	copy(out, s.config.Simulations)
	return out, nil
}

func (s *Store) SaveRule(r domain.Rule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.config.Rules {
		if existing.ID == r.ID {
			s.config.Rules[i] = r
			return s.flush()
		}
	}
	s.config.Rules = append(s.config.Rules, r)
	return s.flush()
}

func (s *Store) SaveSimulation(sim domain.Simulation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.config.Simulations {
		if existing.ID == sim.ID {
			s.config.Simulations[i] = sim
			return s.flush()
		}
	}
	s.config.Simulations = append(s.config.Simulations, sim)
	return s.flush()
}

func (s *Store) DeleteRule(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.config.Rules {
		if r.ID == id {
			s.config.Rules = append(s.config.Rules[:i], s.config.Rules[i+1:]...)
			return s.flush()
		}
	}
	return fmt.Errorf("jsonfile: rule %q not found", id)
}

func (s *Store) DeleteSimulation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sim := range s.config.Simulations {
		if sim.ID == id {
			s.config.Simulations = append(s.config.Simulations[:i], s.config.Simulations[i+1:]...)
			return s.flush()
		}
	}
	return fmt.Errorf("jsonfile: simulation %q not found", id)
}

func (s *Store) ListFaultProfiles() ([]domain.FaultProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.FaultProfile, len(s.config.FaultProfiles))
	copy(out, s.config.FaultProfiles)
	return out, nil
}

// GetFaultProfile returns (nil, nil) when no profile with the given id exists.
func (s *Store) GetFaultProfile(id string) (*domain.FaultProfile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.config.FaultProfiles {
		if s.config.FaultProfiles[i].ID == id {
			p := s.config.FaultProfiles[i]
			return &p, nil
		}
	}
	return nil, nil
}

func (s *Store) SaveFaultProfile(p domain.FaultProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.config.FaultProfiles {
		if existing.ID == p.ID {
			s.config.FaultProfiles[i] = p
			return s.flush()
		}
	}
	s.config.FaultProfiles = append(s.config.FaultProfiles, p)
	return s.flush()
}

func (s *Store) DeleteFaultProfile(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.config.FaultProfiles {
		if p.ID == id {
			s.config.FaultProfiles = append(s.config.FaultProfiles[:i], s.config.FaultProfiles[i+1:]...)
			return s.flush()
		}
	}
	return fmt.Errorf("jsonfile: fault profile %q not found", id)
}
