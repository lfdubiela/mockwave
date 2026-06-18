package mcp

import (
	"context"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mockwave/mockwave/domain"
)

// --- Fault-profile tools ---

func handleListFaults(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		fps, err := c.ListFaultProfiles()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(fps)
	}
}

func handleGetFault(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		fp, err := c.GetFaultProfile(id)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(fp)
	}
}

func handleCreateFault(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var fp domain.FaultProfile
		if err := jsonParam(req, "fault_profile", &fp); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.CreateFaultProfile(fp)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleUpdateFault(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		var fp domain.FaultProfile
		if err := jsonParam(req, "fault_profile", &fp); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.UpdateFaultProfile(id, fp)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleDeleteFault(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.DeleteFaultProfile(id); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("fault profile " + id + " deleted"), nil
	}
}

// registerFaultTools adds all five fault-profile CRUD tools to s.
func registerFaultTools(s *server.MCPServer, c *Client) {
	s.AddTool(
		mcpsdk.NewTool("list_faults",
			mcpsdk.WithDescription("List all fault profiles in the Mockwave instance"),
		),
		handleListFaults(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_fault",
			mcpsdk.WithDescription("Get a single fault profile by ID (uses GET /api/faults/{id} directly)"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Fault profile ID")),
		),
		handleGetFault(c),
	)
	s.AddTool(
		mcpsdk.NewTool("create_fault",
			mcpsdk.WithDescription("Create a new fault profile. The 'fault_profile' parameter is a JSON object with fields: id, name, description, enabled (bool), faults (array). Each fault has: type (jitter/error/hang/reset/halfResponse/slowBody/retryStorm), probability [0,1], params (type-specific)."),
			mcpsdk.WithObject("fault_profile", mcpsdk.Required(), mcpsdk.Description("FaultProfile object")),
		),
		handleCreateFault(c),
	)
	s.AddTool(
		mcpsdk.NewTool("update_fault",
			mcpsdk.WithDescription("Replace an existing fault profile. Overwrites the fault profile with the given ID."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Fault profile ID to update")),
			mcpsdk.WithObject("fault_profile", mcpsdk.Required(), mcpsdk.Description("Updated FaultProfile object")),
		),
		handleUpdateFault(c),
	)
	s.AddTool(
		mcpsdk.NewTool("delete_fault",
			mcpsdk.WithDescription("Delete a fault profile by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Fault profile ID to delete")),
		),
		handleDeleteFault(c),
	)
}
