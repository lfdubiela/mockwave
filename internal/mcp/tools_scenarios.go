package mcp

import (
	"context"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mockwave/mockwave/domain"
)

// --- Scenario tools ---

func handleListScenarios(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		ss, err := c.ListScenarios()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(ss)
	}
}

func handleGetScenario(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		s, err := c.GetScenario(id)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(s)
	}
}

func handleCreateScenario(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var s domain.Scenario
		if err := jsonParam(req, "scenario", &s); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.CreateScenario(s)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleUpdateScenario(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		var s domain.Scenario
		if err := jsonParam(req, "scenario", &s); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.UpdateScenario(id, s)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleDeleteScenario(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.DeleteScenario(id); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("scenario " + id + " deleted"), nil
	}
}

func handleStartScenario(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.StartScenario(id); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("scenario " + id + " started"), nil
	}
}

func handleStopScenario(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.StopScenario(id); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("scenario " + id + " stopped"), nil
	}
}

// registerScenarioTools adds all 7 scenario tools to s.
func registerScenarioTools(s *server.MCPServer, c *Client) {
	s.AddTool(
		mcpsdk.NewTool("list_scenarios",
			mcpsdk.WithDescription("List all chaos scenarios in the Mockwave instance"),
		),
		handleListScenarios(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_scenario",
			mcpsdk.WithDescription("Get a single chaos scenario by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Scenario ID")),
		),
		handleGetScenario(c),
	)
	s.AddTool(
		mcpsdk.NewTool("create_scenario",
			mcpsdk.WithDescription("Create a new chaos scenario. The 'scenario' parameter is a JSON object with fields: id, name, rule_ids (array of strings), phases (array of {duration_sec, fault_profile_id})."),
			mcpsdk.WithObject("scenario", mcpsdk.Required(), mcpsdk.Description("Scenario object")),
		),
		handleCreateScenario(c),
	)
	s.AddTool(
		mcpsdk.NewTool("update_scenario",
			mcpsdk.WithDescription("Replace an existing chaos scenario. Overwrites the scenario with the given ID."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Scenario ID to update")),
			mcpsdk.WithObject("scenario", mcpsdk.Required(), mcpsdk.Description("Updated Scenario object")),
		),
		handleUpdateScenario(c),
	)
	s.AddTool(
		mcpsdk.NewTool("delete_scenario",
			mcpsdk.WithDescription("Delete a chaos scenario by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Scenario ID to delete")),
		),
		handleDeleteScenario(c),
	)
	s.AddTool(
		mcpsdk.NewTool("start_scenario",
			mcpsdk.WithDescription("Start a chaos scenario by ID (POST /api/scenarios/{id}/start, no body). Begins applying the scenario's fault phases to the targeted rules."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Scenario ID to start")),
		),
		handleStartScenario(c),
	)
	s.AddTool(
		mcpsdk.NewTool("stop_scenario",
			mcpsdk.WithDescription("Stop a running chaos scenario by ID (POST /api/scenarios/{id}/stop, no body). Restores normal rule behavior."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Scenario ID to stop")),
		),
		handleStopScenario(c),
	)
}
