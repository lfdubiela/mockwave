// Package store defines the DataStore interface that all mockwave storage
// backends must implement. Import this package to build a custom adapter.
package store

import "github.com/mockwave/mockwave/domain"

// DataStore persists and retrieves rules and simulations.
// Implement this interface to plug in a custom storage backend.
type DataStore interface {
	GetRules() ([]domain.Rule, error)
	GetSimulation(id string) (*domain.Simulation, error)
	SaveRule(r domain.Rule) error
	SaveSimulation(s domain.Simulation) error
	DeleteRule(id string) error
	DeleteSimulation(id string) error
}
