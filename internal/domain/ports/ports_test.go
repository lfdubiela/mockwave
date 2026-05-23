package ports_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/mockwave/mockwave/internal/domain/ports"
)

var _ ports.DataStore = (*mockStore)(nil)
var _ ports.ScriptRunner = (*mockRunner)(nil)

type mockStore struct{}

func (m *mockStore) GetRules() ([]domain.Rule, error)                    { return nil, nil }
func (m *mockStore) GetSimulation(id string) (*domain.Simulation, error) { return nil, nil }
func (m *mockStore) SaveRule(r domain.Rule) error                        { return nil }
func (m *mockStore) SaveSimulation(s domain.Simulation) error            { return nil }
func (m *mockStore) DeleteRule(id string) error                          { return nil }
func (m *mockStore) DeleteSimulation(id string) error                    { return nil }

type mockRunner struct{}

func (m *mockRunner) Run(script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error) {
	return resp, nil
}

func TestInterfaces(t *testing.T) {}
