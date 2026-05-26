package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mockwave/mockwave/domain"
)

// jsonResult marshals v as indented JSON and wraps it in a text tool result.
func jsonResult(v any) (*mcpsdk.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return mcpsdk.NewToolResultText(string(b)), nil
}

func stringParam(req mcpsdk.CallToolRequest, name string) (string, error) {
	args := req.GetArguments()
	v, ok := args[name]
	if !ok {
		return "", fmt.Errorf("missing required parameter: %s", name)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", name)
	}
	return s, nil
}

// jsonParam deserialises the named argument (which may be a map or string) into dst.
func jsonParam(req mcpsdk.CallToolRequest, name string, dst any) error {
	args := req.GetArguments()
	v, ok := args[name]
	if !ok {
		return fmt.Errorf("missing required parameter: %s", name)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("parameter %s: %w", name, err)
	}
	return json.Unmarshal(b, dst)
}

// --- Rule tools ---

func handleListRules(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rules, err := c.ListRules()
		if err != nil {
			return nil, err
		}
		return jsonResult(rules)
	}
}

func handleGetRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		rule, err := c.GetRule(id)
		if err != nil {
			return nil, err
		}
		return jsonResult(rule)
	}
}

func handleCreateRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var rule domain.Rule
		if err := jsonParam(req, "rule", &rule); err != nil {
			return nil, err
		}
		out, err := c.CreateRule(rule)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleUpdateRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		var rule domain.Rule
		if err := jsonParam(req, "rule", &rule); err != nil {
			return nil, err
		}
		out, err := c.UpdateRule(id, rule)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleDeleteRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		if err := c.DeleteRule(id); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("rule " + id + " deleted"), nil
	}
}

// --- Simulation tools ---

func handleListSimulations(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		sims, err := c.ListSimulations()
		if err != nil {
			return nil, err
		}
		return jsonResult(sims)
	}
}

func handleGetSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		sim, err := c.GetSimulation(id)
		if err != nil {
			return nil, err
		}
		return jsonResult(sim)
	}
}

func handleCreateSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var sim domain.Simulation
		if err := jsonParam(req, "simulation", &sim); err != nil {
			return nil, err
		}
		out, err := c.CreateSimulation(sim)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleUpdateSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		var sim domain.Simulation
		if err := jsonParam(req, "simulation", &sim); err != nil {
			return nil, err
		}
		out, err := c.UpdateSimulation(id, sim)
		if err != nil {
			return nil, err
		}
		return jsonResult(out)
	}
}

func handleDeleteSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return nil, err
		}
		if err := c.DeleteSimulation(id); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("simulation " + id + " deleted"), nil
	}
}

// --- Observability tools ---

func handleGetMetrics(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		raw, err := c.GetMetrics()
		if err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText(string(raw)), nil
	}
}

func handleListUnmatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		raw, err := c.ListUnmatched()
		if err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText(string(raw)), nil
	}
}

func handleClearUnmatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.ClearUnmatched(); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("unmatched buffer cleared"), nil
	}
}

func handleReload(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.Reload(); err != nil {
			return nil, err
		}
		return mcpsdk.NewToolResultText("reload triggered"), nil
	}
}

func handleHealth(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.Health(); err != nil {
			return nil, fmt.Errorf("admin API unreachable: %w", err)
		}
		return mcpsdk.NewToolResultText("ok"), nil
	}
}
