package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mockwave/mockwave/domain"
)

// jsonResult marshals v and wraps it in a tool result with StructuredContent
// populated per MCP 2025-03-26 spec.
func jsonResult(v any) (*mcpsdk.CallToolResult, error) {
	return mcpsdk.NewToolResultJSON(v)
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

// boolParam returns the named bool argument, or false if absent/wrong type.
func boolParam(req mcpsdk.CallToolRequest, name string) bool {
	v, ok := req.GetArguments()[name]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// optStringParam returns the named string argument, or "" if absent/wrong type.
func optStringParam(req mcpsdk.CallToolRequest, name string) string {
	v, ok := req.GetArguments()[name]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// CaptureFilterArgs reads the optional capture LIST filter arguments into a
// url.Values. The map-typed args (body/attr/query) become repeated "key:value"
// params; the scalar args pass through. Absent args produce nothing.
func CaptureFilterArgs(req mcpsdk.CallToolRequest) url.Values {
	q := url.Values{}
	args := req.GetArguments()
	for _, name := range []string{"body", "attr", "query"} {
		if m, ok := args[name].(map[string]any); ok {
			for k, v := range m {
				q.Add(name, fmt.Sprintf("%s:%v", k, v))
			}
		}
	}
	for _, name := range []string{"method", "path", "cursor"} {
		if s := optStringParam(req, name); s != "" {
			q.Set(name, s)
		}
	}
	if n, ok := args["status"].(float64); ok {
		q.Set("status", strconv.Itoa(int(n)))
	}
	if n, ok := args["limit"].(float64); ok {
		q.Set("limit", strconv.Itoa(int(n)))
	}
	return q
}

// upsertSimulation creates or (when overwrite=true) replaces a simulation.
func upsertSimulation(c *Client, sim domain.Simulation, overwrite bool) error {
	if !overwrite {
		_, err := c.CreateSimulation(sim)
		return err
	}
	if _, err := c.GetSimulation(sim.ID); err == nil {
		_, err = c.UpdateSimulation(sim.ID, sim)
		return err
	}
	_, err := c.CreateSimulation(sim)
	return err
}

// upsertRule creates or (when overwrite=true) replaces a rule.
func upsertRule(c *Client, rule domain.Rule, overwrite bool) error {
	if !overwrite {
		_, err := c.CreateRule(rule)
		return err
	}
	if _, err := c.GetRule(rule.ID); err == nil {
		_, err = c.UpdateRule(rule.ID, rule)
		return err
	}
	_, err := c.CreateRule(rule)
	return err
}

// --- Rule tools ---

func handleListRules(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		rules, err := c.ListRules()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(rules)
	}
}

func handleGetRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		rule, err := c.GetRule(id)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(rule)
	}
}

func handleCreateRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var rule domain.Rule
		if err := jsonParam(req, "rule", &rule); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.CreateRule(rule)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleUpdateRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		var rule domain.Rule
		if err := jsonParam(req, "rule", &rule); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.UpdateRule(id, rule)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleDeleteRule(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.DeleteRule(id); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("rule " + id + " deleted"), nil
	}
}

// --- Simulation tools ---

func handleListSimulations(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		sims, err := c.ListSimulations()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(sims)
	}
}

func handleGetSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		sim, err := c.GetSimulation(id)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(sim)
	}
}

func handleCreateSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var sim domain.Simulation
		if err := jsonParam(req, "simulation", &sim); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.CreateSimulation(sim)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleUpdateSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		var sim domain.Simulation
		if err := jsonParam(req, "simulation", &sim); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		out, err := c.UpdateSimulation(id, sim)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return jsonResult(out)
	}
}

func handleDeleteSimulation(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		id, err := stringParam(req, "id")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		if err := c.DeleteSimulation(id); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("simulation " + id + " deleted"), nil
	}
}

// --- Observability tools ---

func handleGetMetrics(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		raw, err := c.GetMetrics()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return mcpsdk.NewToolResultError("failed to parse response: " + err.Error()), nil
		}
		return jsonResult(v)
	}
}

func handleListUnmatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		raw, err := c.ListUnmatched()
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			return mcpsdk.NewToolResultError("failed to parse response: " + err.Error()), nil
		}
		return jsonResult(v)
	}
}

func handleClearUnmatched(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.ClearUnmatched(); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("unmatched buffer cleared"), nil
	}
}

func handleReload(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.Reload(); err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		return mcpsdk.NewToolResultText("reload triggered"), nil
	}
}

func handleHealth(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if err := c.Health(); err != nil {
			return mcpsdk.NewToolResultError("admin API unreachable: " + err.Error()), nil
		}
		return mcpsdk.NewToolResultText("ok"), nil
	}
}

// --- OpenAPI import ---

func handleGenerateFromOpenAPI(c *Client) func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	return func(ctx context.Context, req mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		source, err := stringParam(req, "source")
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}
		overwrite := boolParam(req, "overwrite")
		pathPrefix := optStringParam(req, "path_prefix")
		tags := optStringParam(req, "tags")

		spec, err := FetchAndParse(source)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}

		pairs, skipped, err := GeneratePairs(spec, pathPrefix, tags)
		if err != nil {
			return mcpsdk.NewToolResultError(err.Error()), nil
		}

		// Conflict check (only when overwrite=false).
		if !overwrite {
			var conflicts []string
			for _, p := range pairs {
				if _, err := c.GetRule(p.Rule.ID); err == nil {
					conflicts = append(conflicts, "rule: "+p.Rule.ID)
				}
				if _, err := c.GetSimulation(p.Simulation.ID); err == nil {
					conflicts = append(conflicts, "simulation: "+p.Simulation.ID)
				}
			}
			if len(conflicts) > 0 {
				msg := fmt.Sprintf("%d conflict(s) found — re-run with overwrite:true to replace:", len(conflicts))
				for _, conflict := range conflicts {
					msg += "\n  - " + conflict
				}
				return mcpsdk.NewToolResultError(msg), nil
			}
		}

		// Create / replace simulations first (rules reference them).
		simsCreated := 0
		for _, p := range pairs {
			if err := upsertSimulation(c, p.Simulation, overwrite); err != nil {
				return mcpsdk.NewToolResultError(
					fmt.Sprintf("created %d/%d simulations before error: %s", simsCreated, len(pairs), err),
				), nil
			}
			simsCreated++
		}

		// Create / replace rules.
		rulesCreated := 0
		for _, p := range pairs {
			if err := upsertRule(c, p.Rule, overwrite); err != nil {
				return mcpsdk.NewToolResultError(
					fmt.Sprintf("created %d/%d rules before error: %s", rulesCreated, len(pairs), err),
				), nil
			}
			rulesCreated++
		}

		verb := "created"
		if overwrite {
			verb = "created/updated"
		}
		summary := fmt.Sprintf("Generated from %s:\n  %d rules %s\n  %d simulations %s",
			source, rulesCreated, verb, simsCreated, verb)
		if len(skipped) > 0 {
			summary += fmt.Sprintf("\n  %d path(s) skipped (no 2xx response defined)", len(skipped))
		}
		return mcpsdk.NewToolResultText(summary), nil
	}
}
