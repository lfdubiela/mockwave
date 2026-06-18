package mcp

import (
	"context"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// handleEvalScript calls POST /api/script/eval with {"script": "<value>"}.
// The /api/script/eval handler (restapi.scriptEval) decodes an evalRequest
// struct with a single field: Script string `json:"script"`. Explicit params
// are declared so the LLM knows the exact shape.
func handleEvalScript(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		script, err := stringParam(req, "script")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.EvalScript(map[string]any{"script": script})
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

// handleGetMetricsHistory calls GET /api/metrics/history (no params).
func handleGetMetricsHistory(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		out, err := c.MetricsHistory()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

// registerDevTools adds the two dev/debugging tools to s.
func registerDevTools(s *server.MCPServer, c *Client) {
	s.AddTool(
		mcpsdk.NewTool("eval_script",
			mcpsdk.WithDescription("Evaluate a JavaScript simulation script against a sample request to test computed responses"),
			mcpsdk.WithString("script", mcpsdk.Required(), mcpsdk.Description("JavaScript source to evaluate")),
		),
		handleEvalScript(c),
	)
	s.AddTool(
		mcpsdk.NewTool("get_metrics_history",
			mcpsdk.WithDescription("Get the rolling metrics history (recent traffic time-series)"),
		),
		handleGetMetricsHistory(c),
	)
}
