package ports

import "github.com/mockwave/mockwave/internal/domain"

// DataStore persists and retrieves rules and simulations.
type DataStore interface {
	GetRules() ([]domain.Rule, error)
	GetSimulation(id string) (*domain.Simulation, error)
	SaveRule(r domain.Rule) error
	SaveSimulation(s domain.Simulation) error
	DeleteRule(id string) error
	DeleteSimulation(id string) error
}

// ScriptRunner executes a JavaScript string against request/response maps.
type ScriptRunner interface {
	Run(script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error)
}
