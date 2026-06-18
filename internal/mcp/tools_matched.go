package mcp

import (
	"context"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// --- Matched HTTP-capture tools ---

func handleListMatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rule, err := stringParam(req, "rule")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		q := captureFilterArgs(req)
		page, err := c.ListMatched(rule, q)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(page)
	}
}

func handleGetMatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rule, err := stringParam(req, "rule")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		full, err := c.GetMatched(rule, id)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(full)
	}
}

func handleClearMatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rule, err := stringParam(req, "rule")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.ClearMatched(rule); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("matched requests for rule " + rule + " cleared"), nil
	}
}

// registerMatchedTools adds the three matched HTTP-capture inspection tools to s.
func registerMatchedTools(s *server.MCPServer, c *Client) {
	s.AddTool(
		mcpsdk.NewTool("list_matched",
			mcpsdk.WithDescription("List matched HTTP requests for a rule, newest first. Supports filtering by body JSONPath (e.g. {\"$.email\":\"ada@x.com\"}), URL query-param filters, method, path, and HTTP status. Paginated via cursor."),
			mcpsdk.WithString("rule", mcpsdk.Required(), mcpsdk.Description("Rule ID")),
			mcpsdk.WithObject("body", mcpsdk.Description("JSONPath→value filter on the captured request body, e.g. {\"$.email\":\"ada@x.com\"}")),
			mcpsdk.WithObject("query", mcpsdk.Description("URL query-param filters, e.g. {\"env\":\"prod\"}")),
			mcpsdk.WithString("method", mcpsdk.Description("Filter by HTTP method (e.g. GET)")),
			mcpsdk.WithString("path", mcpsdk.Description("Filter by request path glob (e.g. /api/*)")),
			mcpsdk.WithNumber("status", mcpsdk.Description("Filter by HTTP response status code (e.g. 200)")),
			mcpsdk.WithNumber("limit", mcpsdk.Description("Maximum number of items to return (default 20, max 100)")),
			mcpsdk.WithString("cursor", mcpsdk.Description("Pagination cursor from a previous response's next_cursor")),
		),
		handleListMatched(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_matched",
			mcpsdk.WithDescription("Get the full detail of a single matched HTTP request (includes request and response bodies)"),
			mcpsdk.WithString("rule", mcpsdk.Required(), mcpsdk.Description("Rule ID")),
			mcpsdk.WithString("id", mcpsdk.Required(), mcpsdk.Description("Matched request ID")),
		),
		handleGetMatched(c),
	)
	s.AddTool(
		mcpsdk.NewTool("clear_matched",
			mcpsdk.WithDescription("Delete all matched HTTP requests for a rule"),
			mcpsdk.WithString("rule", mcpsdk.Required(), mcpsdk.Description("Rule ID whose matched requests will be cleared")),
		),
		handleClearMatched(c),
	)
}
