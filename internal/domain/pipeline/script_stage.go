package pipeline

import (
	"context"
	"fmt"
)

// ScriptRunner executes a JavaScript string against request/response maps.
// Internal interface — not exposed in the public store package.
type ScriptRunner interface {
	Run(script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error)
}

type ScriptProvider func(pctx *PipelineContext) string

type ScriptStage struct {
	runner    ScriptRunner
	getScript ScriptProvider
}

func NewScriptStage(runner ScriptRunner, getScript ScriptProvider) *ScriptStage {
	return &ScriptStage{runner: runner, getScript: getScript}
}

func (s *ScriptStage) Name() string { return "script" }

func (s *ScriptStage) Execute(_ context.Context, pctx *PipelineContext) error {
	if pctx.ShouldForward || pctx.Response == nil {
		return nil
	}
	script := s.getScript(pctx)
	if script == "" {
		return nil
	}
	req := map[string]interface{}{
		"headers":  toIfaceMap(pctx.Request.Headers),
		"query":    toIfaceMap(pctx.Request.Query),
		"path":     pctx.Request.PathSegs,
		"method":   pctx.Request.Method,
		"protocol": pctx.Request.Protocol,
	}
	resp := map[string]interface{}{
		"status":   pctx.Response.Status,
		"headers":  toIfaceMap(pctx.Response.Headers),
		"body":     pctx.Response.Body,
		"delay_ms": pctx.Response.DelayMs,
	}
	result, err := s.runner.Run(script, req, resp)
	if err != nil {
		return fmt.Errorf("script stage: %w", err)
	}
	if status, ok := result["status"]; ok {
		if n, ok := toInt(status); ok {
			pctx.Response.Status = n
		}
	}
	if body, ok := result["body"]; ok {
		pctx.Response.Body = body
	}
	if delayMs, ok := result["delay_ms"]; ok {
		if n, ok := toInt(delayMs); ok {
			pctx.Response.DelayMs = n
		}
	}
	return nil
}

func toIfaceMap(m map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
