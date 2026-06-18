package mcp

import (
	"context"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// --- Event-capture tools ---

func handleListEventCaptures(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rule, err := stringParam(req, "rule")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		q := captureFilterArgs(req)
		page, err := c.ListEventCaptures(rule, q)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(page)
	}
}

func handleGetEventCapture(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rule, err := stringParam(req, "rule")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		full, err := c.GetEventCapture(rule, id)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(full)
	}
}

func handleClearEventCaptures(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rule, err := stringParam(req, "rule")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.ClearEventCaptures(rule); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("event captures for rule " + rule + " cleared"), nil
	}
}

// registerEventCaptureTools adds the three event-capture inspection tools to s.
func registerEventCaptureTools(s *server.MCPServer, c *Client) {
	s.AddTool(
		mcpsdk.NewTool("list_event_captures",
			mcpsdk.WithDescription("List captured events for an event rule, newest first. Supports filtering by body JSONPath (e.g. {\"$.correlation_id\":\"abc\"}), message attributes, source/detail_type query fields, method, path, and HTTP status. Paginated via cursor."),
			mcpsdk.WithString("rule", mcpsdk.Required(), mcpsdk.Description("Event rule ID")),
			mcpsdk.WithObject("body", mcpsdk.Description("JSONPath→value filter on the published message body, e.g. {\"$.correlation_id\":\"abc\"}")),
			mcpsdk.WithObject("attr", mcpsdk.Description("Message attribute filters, e.g. {\"env\":\"prod\"}")),
			mcpsdk.WithObject("query", mcpsdk.Description("Source/detail_type/etc filters, e.g. {\"source\":\"com.example\"}")),
			mcpsdk.WithString("method", mcpsdk.Description("Filter by HTTP method (e.g. POST)")),
			mcpsdk.WithString("path", mcpsdk.Description("Filter by request path glob (e.g. /foo/*)")),
			mcpsdk.WithNumber("status", mcpsdk.Description("Filter by HTTP response status code (e.g. 200)")),
			mcpsdk.WithNumber("limit", mcpsdk.Description("Maximum number of items to return (default 20, max 100)")),
			mcpsdk.WithString("cursor", mcpsdk.Description("Pagination cursor from a previous response's next_cursor")),
		),
		handleListEventCaptures(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_event_capture",
			mcpsdk.WithDescription("Get the full detail of a single captured event (includes request and response bodies)"),
			mcpsdk.WithString("rule", mcpsdk.Required(), mcpsdk.Description("Event rule ID")),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Capture ID")),
		),
		handleGetEventCapture(c),
	)
	s.AddTool(
		mcpsdk.NewTool("clear_event_captures",
			mcpsdk.WithDescription("Delete all captured events for an event rule"),
			mcpsdk.WithString("rule", mcpsdk.Required(), mcpsdk.Description("Event rule ID whose captures will be cleared")),
		),
		handleClearEventCaptures(c),
	)
}
