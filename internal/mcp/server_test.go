package mcp_test

import (
	"testing"

	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServer_RegistersAllTools(t *testing.T) {
	s := mockwavemcp.NewServer("http://localhost:9090", "0.2.0")
	require.NotNil(t, s)

	// The server must expose all 21 tools
	expectedTools := []string{
		"list_rules", "get_rule", "create_rule", "update_rule", "delete_rule",
		"list_simulations", "get_simulation", "create_simulation", "update_simulation", "delete_simulation",
		"list_event_rules", "get_event_rule", "create_event_rule", "update_event_rule", "delete_event_rule",
		"get_metrics", "list_unmatched", "clear_unmatched", "reload", "health",
		"generate_from_openapi",
	}

	tools := s.ListTools()
	assert.Len(t, tools, 21, "expected 21 tools to be registered")

	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}

	assert.ElementsMatch(t, expectedTools, toolNames, "all expected tools must be registered")
}

func TestNewServer_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		s := mockwavemcp.NewServer("http://localhost:9090", "0.2.0")
		_ = s
	})
}

func TestNewServer_EmptyVersion(t *testing.T) {
	assert.NotPanics(t, func() {
		s := mockwavemcp.NewServer("http://localhost:9090", "")
		_ = s
	})
}

func TestNewServer_EmptyAdminURL(t *testing.T) {
	assert.NotPanics(t, func() {
		s := mockwavemcp.NewServer("", "0.2.0")
		_ = s
	})
}
