package server_test

import (
	"fmt"
	"testing"

	"github.com/mockwave/mockwave/internal/domain"
	"github.com/mockwave/mockwave/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubStore struct{}

func (s *stubStore) GetRules() ([]domain.Rule, error)                    { return nil, nil }
func (s *stubStore) GetSimulation(id string) (*domain.Simulation, error) { return nil, fmt.Errorf("nope") }
func (s *stubStore) SaveRule(r domain.Rule) error                        { return nil }
func (s *stubStore) SaveSimulation(sim domain.Simulation) error          { return nil }
func (s *stubStore) DeleteRule(id string) error                          { return nil }
func (s *stubStore) DeleteSimulation(id string) error                    { return nil }

func TestServer_BuildsPipeline(t *testing.T) {
	srv, err := server.New(server.Config{Store: &stubStore{}})
	require.NoError(t, err)
	assert.NotNil(t, srv)
}

func TestServer_NilStoreReturnsError(t *testing.T) {
	_, err := server.New(server.Config{Store: nil})
	assert.Error(t, err)
}
