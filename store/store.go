// Package store defines the DataStore interface that all mockwave storage
// backends must implement. Import this package to build a custom adapter.
package store

import "github.com/mockwave/mockwave/domain"

// DataStore persists and retrieves rules and simulations.
// Implement this interface to plug in a custom storage backend.
//
// Contract for GetSimulation: returns (nil, nil) when the simulation does not
// exist — this is NOT an error. Callers must check for a nil pointer to detect
// not-found before dereferencing the result.
type DataStore interface {
	GetRules() ([]domain.Rule, error)

	// GetSimulation returns the simulation with the given id, or (nil, nil) if
	// no simulation with that id exists. A non-nil error indicates a storage
	// failure, not a missing record.
	GetSimulation(id string) (*domain.Simulation, error)

	// ListSimulations returns all simulations in the store.
	ListSimulations() ([]domain.Simulation, error)

	SaveRule(r domain.Rule) error
	SaveSimulation(s domain.Simulation) error
	DeleteRule(id string) error
	DeleteSimulation(id string) error
}

// VersionedStore is an optional capability. Stores that can report a monotonic
// config version enable the periodic version-poll reloader: the reloader reads
// ConfigVersion cheaply each tick and triggers a full in-memory reload only when
// the value changed. Stores that do not implement it (e.g. the JSON store) are
// never polled.
type VersionedStore interface {
	// ConfigVersion returns a monotonically increasing marker that changes on
	// every rule/simulation write. Returns 0 when no marker exists yet.
	ConfigVersion() (int64, error)
}
