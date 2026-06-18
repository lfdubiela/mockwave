package mcp

import (
	"context"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/mockwave/mockwave/domain"
)

// --- Event-rule tools ---

func handleListEventRules(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rules, err := c.ListEventRules()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(rules)
	}
}

func handleGetEventRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		rule, err := c.GetEventRule(id)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(rule)
	}
}

func handleCreateEventRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var rule domain.EventRule
		if err := jsonParam(req, "event_rule", &rule); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.CreateEventRule(rule)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleUpdateEventRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		var rule domain.EventRule
		if err := jsonParam(req, "event_rule", &rule); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.UpdateEventRule(id, rule)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleDeleteEventRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.DeleteEventRule(id); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("event rule " + id + " deleted"), nil
	}
}

// registerEventRuleTools adds all five event-rule CRUD tools to s.
func registerEventRuleTools(s *server.MCPServer, c *Client) {
	s.AddTool(
		mcpsdk.NewTool("list_event_rules",
			mcpsdk.WithDescription("List all event rules in the Mockwave instance"),
		),
		handleListEventRules(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_event_rule",
			mcpsdk.WithDescription("Get a single event rule by ID (filters the list client-side; no single-get endpoint exists)"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Event rule ID")),
		),
		handleGetEventRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("create_event_rule",
			mcpsdk.WithDescription("Create a new event rule. The 'event_rule' parameter is a JSON object with fields: id, name, disabled, match (service, target, source, detail_type, attributes, message), optional forward (endpoint, region, credential, delay_ms)."),
			mcpsdk.WithObject("event_rule", mcpsdk.Required(), mcpsdk.Description("EventRule object")),
		),
		handleCreateEventRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("update_event_rule",
			mcpsdk.WithDescription("Replace an existing event rule. Overwrites the event rule with the given ID."),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Event rule ID to update")),
			mcpsdk.WithObject("event_rule", mcpsdk.Required(), mcpsdk.Description("Updated EventRule object")),
		),
		handleUpdateEventRule(c),
	)
	s.AddTool(
		mcpsdk.NewTool("delete_event_rule",
			mcpsdk.WithDescription("Delete an event rule by ID"),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Event rule ID to delete")),
		),
		handleDeleteEventRule(c),
	)
}
