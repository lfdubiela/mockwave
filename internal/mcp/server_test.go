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

	// The server must expose all 46 tools
	expectedTools := []string{
		"list_rules", "get_rule", "create_rule", "update_rule", "delete_rule",
		"list_simulations", "get_simulation", "create_simulation", "update_simulation", "delete_simulation",
		"list_event_rules", "get_event_rule", "create_event_rule", "update_event_rule", "delete_event_rule",
		"list_event_captures", "get_event_capture", "clear_event_captures",
		"list_matched", "get_matched", "clear_matched",
		"get_metrics", "list_unmatched", "clear_unmatched", "reload", "health",
		"generate_from_openapi",
		"list_faults", "get_fault", "create_fault", "update_fault", "delete_fault",
		"halt_chaos", "resume_chaos", "get_chaos_status",
		"list_scenarios", "get_scenario", "create_scenario", "update_scenario", "delete_scenario",
		"start_scenario", "stop_scenario",
		"export_config", "import_config",
		"eval_script", "get_metrics_history",
	}

	tools := s.ListTools()
	assert.Len(t, tools, 46, "expected 46 tools to be registered")

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
