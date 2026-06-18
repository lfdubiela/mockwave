package mcp

import (
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewServer builds an MCP server wired to the Mockwave admin API at adminURL.
func NewServer(adminURL, version string) *server.MCPServer {
	c := NewClient(adminURL)

	s := server.NewMCPServer("mockwave", version,
		server.WithToolCapabilities(true),
	)

	// Rules
	s.AddTool(
		mcpsdk.NewTool("list_rules",
			mcpsdk.WithDescription("List all rules in the Mockwave instance"),
		),
		handleListRules(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_rule",
			mcpsdk.WithDescription("Get a single rule by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Rule ID")),
		),
		handleGetRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("create_rule",
			mcpsdk.WithDescription("Create a new rule. The 'rule' parameter is a JSON object matching the Rule schema (id, name, match, buckets). Forward buckets carry their own forward_url."),
			mcpsdk.WithObject("rule", mcpsdk.Required(), mcpsdk.Description("Rule object")),
		),
		handleCreateRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("update_rule",
			mcpsdk.WithDescription("Replace an existing rule. Overwrites the rule with the given ID."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Rule ID to update")),
			mcpsdk.WithObject("rule", mcpsdk.Required(), mcpsdk.Description("Updated rule object")),
		),
		handleUpdateRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("delete_rule",
			mcpsdk.WithDescription("Delete a rule by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Rule ID to delete")),
		),
		handleDeleteRule(c),
	)

	// Simulations
	s.AddTool(
		mcpsdk.NewTool("list_simulations",
			mcpsdk.WithDescription("List all simulations in the Mockwave instance"),
		),
		handleListSimulations(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_simulation",
			mcpsdk.WithDescription("Get a single simulation by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Simulation ID")),
		),
		handleGetSimulation(c),
	)
	s.AddTool(
		mcpsdk.NewTool("create_simulation",
			mcpsdk.WithDescription("Create a new simulation. The 'simulation' parameter is a JSON object with fields: id, protocol, response (status/headers/body/delay_ms), script, soap_envelope, grpc_message, grpc_status."),
			mcpsdk.WithObject("simulation", mcpsdk.Required(), mcpsdk.Description("Simulation object")),
		),
		handleCreateSimulation(c),
	)
	s.AddTool(
		mcpsdk.NewTool("update_simulation",
			mcpsdk.WithDescription("Replace an existing simulation. Overwrites the simulation with the given ID."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Simulation ID to update")),
			mcpsdk.WithObject("simulation", mcpsdk.Required(), mcpsdk.Description("Updated simulation object")),
		),
		handleUpdateSimulation(c),
	)
	s.AddTool(
		mcpsdk.NewTool("delete_simulation",
			mcpsdk.WithDescription("Delete a simulation by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Simulation ID to delete")),
		),
		handleDeleteSimulation(c),
	)

	// Observability
	s.AddTool(
		mcpsdk.NewTool("get_metrics",
			mcpsdk.WithDescription("Get current metrics snapshot: total requests, per-rule hits, p95 latency"),
		),
		handleGetMetrics(c),
	)
	s.AddTool(
		mcpsdk.NewTool("list_unmatched",
			mcpsdk.WithDescription("List requests that matched no rule — useful for discovering what to mock next"),
		),
		handleListUnmatched(c),
	)
	s.AddTool(
		mcpsdk.NewTool("clear_unmatched",
			mcpsdk.WithDescription("Clear the unmatched request buffer"),
		),
		handleClearUnmatched(c),
	)
	s.AddTool(
		mcpsdk.NewTool("reload",
			mcpsdk.WithDescription("Trigger hot-reload from the store (picks up externally modified rules/simulations)"),
		),
		handleReload(c),
	)
	s.AddTool(
		mcpsdk.NewTool("health",
			mcpsdk.WithDescription("Check if the Mockwave admin API is reachable"),
		),
		handleHealth(c),
	)

	// Event rules
	registerEventRuleTools(s, c)

	// Event captures
	registerEventCaptureTools(s, c)

	// Matched HTTP captures
	registerMatchedTools(s, c)

	// Fault profiles (Chaos)
	registerFaultTools(s, c)

	// Chaos control (halt/resume/status)
	registerChaosControlTools(s, c)

	// Scenarios (chaos test orchestration)
	registerScenarioTools(s, c)

	// Config import/export
	registerTransferTools(s, c)

	// OpenAPI import
	s.AddTool(
		mcpsdk.NewTool("generate_from_openapi",
			mcpsdk.WithDescription("Generate Mockwave rules and simulations from an OpenAPI 2.0 or 3.0 spec (URL or file path). Skips endpoints with no 2xx response. Use overwrite:true to replace existing rules/simulations. Note: the operation is not atomic — on error, re-run with overwrite:true to complete."),
			mcpsdk.WithString("source", mcpsdk.Required(), mcpsdk.Description("URL (https://...) or local file path (/path/to/spec.yaml) of the OpenAPI spec")),
			mcpsdk.WithString("path_prefix", mcpsdk.Description("Only generate for paths starting with this prefix (e.g. /api/v1)")),
			mcpsdk.WithString("tags", mcpsdk.Description("Comma-separated OpenAPI tags to include (e.g. Orders,Users)")),
			mcpsdk.WithBoolean("overwrite", mcpsdk.Description("Replace existing rules/simulations with the same ID")),
		),
		handleGenerateFromOpenAPI(c),
	)

	return s
}
