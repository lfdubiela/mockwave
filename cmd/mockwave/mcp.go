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
  }`,
		RunE: func(cmd *cobra.Command, args []string) error {
			s := mockwavemcp.NewServer(adminURL, version)
			return server.ServeStdio(s)
		},
	}

	cmd.Flags().StringVar(&adminURL, "admin-url", "http://localhost:9090",
		"Mockwave admin API base URL (local or remote)")

	return cmd
}
