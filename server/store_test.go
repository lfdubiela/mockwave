package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStoreFromEnv_JSONMissingConfig(t *testing.T) {
	t.Setenv("MOCKWAVE_STORE", "json")
	t.Setenv("MOCKWAVE_CONFIG", "")
	_, err := buildStoreFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MOCKWAVE_CONFIG")
}

func TestBuildStoreFromEnv_UnknownBackend(t *testing.T) {
	t.Setenv("MOCKWAVE_STORE", "redis")
	_, err := buildStoreFromEnv()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis")
}
