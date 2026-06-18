package main

import (
	"github.com/mark3labs/mcp-go/server"
	mockwavemcp "github.com/mockwave/mockwave/internal/mcp"
	"github.com/spf13/cobra"
)

func mcpCmd() *cobra.Command {
	var adminURL string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start MCP server — bridges Claude Code to a Mockwave admin API",
		Long: `Starts a stdio MCP server that allows AI assistants (e.g. Claude Code) to
manage rules and simulations on a Mockwave instance.

WARNING: The Mockwave admin API has no authentication. When using --admin-url
with a remote instance, ensure the admin port is not publicly accessible or
is protected by a reverse proxy/firewall.

Register in ~/.claude/mcp.json (mockwave must be on $PATH, e.g. via brew install):

  {
    "mcpServers": {
      "mockwave-local": {
        "command": "mockwave",
        "args": ["mcp", "--admin-url", "http://localhost:9090"]
      },
      "mockwave-sandbox": {
        "command": "mockwave",
        "args": ["mcp", "--admin-url", "https://mockwave.sandbox.example.com"]
      }
    }
  }

Available MCP tools:

  Rules
    list_rules          List all rules
    get_rule            Get a rule by ID
    create_rule         Create a new rule
    update_rule         Replace an existing rule
    delete_rule         Delete a rule

  Simulations
    list_simulations    List all simulations
    get_simulation      Get a simulation by ID
    create_simulation   Create a new simulation
    update_simulation   Replace an existing simulation
    delete_simulation   Delete a simulation
    generate_from_openapi  Generate rules + simulations from an OpenAPI 2.0/3.0 spec

  Event Rules
    list_event_rules    List all event rules
    get_event_rule      Get an event rule by ID
    create_event_rule   Create a new event rule
    update_event_rule   Replace an existing event rule
    delete_event_rule   Delete an event rule

  Event Captures
    list_event_captures  List captured events for a rule (supports body/attr/query filters)
    get_event_capture    Get the full detail of a single captured event (including bodies)
    clear_event_captures Delete all captured events for a rule

  Matched Requests
    list_matched         List matched HTTP requests for a rule (supports body/query filters)
    get_matched          Get the full detail of a single matched HTTP request (including bodies)
    clear_matched        Delete all matched HTTP requests for a rule

  Observability
    get_metrics         Traffic metrics snapshot (requests, hits, p95 latency)
    list_unmatched      Requests that matched no rule
    clear_unmatched     Clear the unmatched buffer
    reload              Hot-reload rules/simulations from the store
    health              Check admin API reachability`,
		Example: `  # Local instance (default port)
  mockwave mcp --admin-url http://localhost:9090

  # Remote sandbox
  mockwave mcp --admin-url https://mockwave.sandbox.example.com

  # Custom local port
  mockwave mcp --admin-url http://localhost:3001`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := mockwavemcp.NewServer(adminURL, version)
			return server.ServeStdio(s)
		},
	}

	cmd.Flags().StringVar(&adminURL, "admin-url", "http://localhost:9090",
		"Mockwave admin API base URL (local or remote)")

	return cmd
}
