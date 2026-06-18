package mcp

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// --- Config transfer tools ---

func handleExportConfig(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		cfg, err := c.ExportConfig()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(cfg)
	}
}

func handleImportConfig(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		// Decode the required "config" object param into a json.RawMessage so it
		// is forwarded verbatim to the admin API without losing any fields.
		args := req.GetArguments()
		v, ok := args["config"]
		if !ok {
			return mcpsdk.NewToolResultError("missing required parameter: config"), nil
		}
		b, err := json.Marshal(v)
		if err != nil {
			return mcpsdk.NewToolResultError("parameter config: " + err.Error()), nil
		}
		var payload json.RawMessage = b

		preview := boolParam(req, "preview")

		result, err := c.ImportConfig(payload, preview)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(result)
	}
}

// registerTransferTools adds export_config and import_config to s.
func registerTransferTools(s *server.MCPServer, c *Client) {
	s.AddTool(
		mcpsdk.NewTool("export_config",
			mcpsdk.WithDescription("Export the full Mockwave config (rules, simulations, fault profiles, scenarios, event rules) as JSON"),
		),
		handleExportConfig(c),
	)
	s.AddTool(
		mcpsdk.NewTool("import_config",
			mcpsdk.WithDescription("Bulk-import a Mockwave config. Use preview:true for a dry-run that returns conflict info without writing anything."),
			mcpsdk.WithObject("config", mcpsdk.Required(), mcpsdk.Description("Full config object to import (rules, simulations, fault_profiles, scenarios, event_rules)")),
			mcpsdk.WithBoolean("preview", mcpsdk.Description("Dry-run: validate without applying")),
		),
		handleImportConfig(c),
	)
}
