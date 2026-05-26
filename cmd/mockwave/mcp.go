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

Register in ~/.claude/mcp.json:

  {
    "mcpServers": {
      "mockwave-local": {
        "command": "mockwave",
        "args": ["mcp", "--admin-url", "http://localhost:9090"]
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
	_ = cmd.MarkFlagRequired("admin-url")

	return cmd
}
