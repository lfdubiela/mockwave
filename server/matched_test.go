package server

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResolveMatchedConfig_Defaults(t *testing.T) {
	c := resolveMatchedConfig(MatchedConfig{Enabled: true})
	assert.True(t, c.Enabled)
	assert.Equal(t, time.Hour, c.TTL)
	assert.Equal(t, 10000, c.BufferSize)
	assert.Equal(t, 30*time.Second, c.SyncInterval)
}

func TestResolveMatchedConfig_RespectsExplicit(t *testing.T) {
	in := MatchedConfig{Enabled: true, TTL: 5 * time.Minute, BufferSize: 50, SyncInterval: 2 * time.Second}
	c := resolveMatchedConfig(in)
	assert.Equal(t, 5*time.Minute, c.TTL)
	assert.Equal(t, 50, c.BufferSize)
	assert.Equal(t, 2*time.Second, c.SyncInterval)
}

func TestResolveMatchedConfig_EnvFallback(t *testing.T) {
	t.Setenv("MOCKWAVE_MATCHED_CAPTURE", "true")
	t.Setenv("MOCKWAVE_MATCHED_TTL", "120")
	t.Setenv("MOCKWAVE_MATCHED_BUFFER_SIZE", "7")
	t.Setenv("MOCKWAVE_MATCHED_SYNC_INTERVAL", "3")
	c := resolveMatchedConfig(MatchedConfig{})
	assert.True(t, c.Enabled)
	assert.Equal(t, 120*time.Second, c.TTL)
	assert.Equal(t, 7, c.BufferSize)
	assert.Equal(t, 3*time.Second, c.SyncInterval)
}
