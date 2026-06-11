package chaos_test

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/chaos"
	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

func profile(faults ...domain.Fault) map[string]domain.FaultProfile {
	return map[string]domain.FaultProfile{"p": {ID: "p", Enabled: true, Faults: faults}}
}

func pctxWithProfile() *pipeline.PipelineContext {
	return &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r"}, FaultProfileID: "p"}
}

func TestErrorFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultError, Probability: 1,
		Params: domain.FaultParams{StatusCode: 503, Body: `{"error":"boom"}`, Headers: map[string]string{"X-Fault": "1"}},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	if err := stage.Execute(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if !pctx.FaultShortCircuit || pctx.Response == nil || pctx.Response.Status != 503 {
		t.Fatalf("expected 503 short-circuit, got %+v", pctx.Response)
	}
	if pctx.Response.Headers["X-Fault"] != "1" {
		t.Fatalf("expected fault header, got %v", pctx.Response.Headers)
	}
}

func TestJitterFaultAddsDelay(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultJitter, Probability: 1,
		Params: domain.FaultParams{BaseDelayMs: 100, JitterMs: 50},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	if err := stage.Execute(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
	if pctx.FaultDelayMs < 100 || pctx.FaultDelayMs >= 150 {
		t.Fatalf("delay %d outside [100,150)", pctx.FaultDelayMs)
	}
	if pctx.FaultShortCircuit {
		t.Fatal("jitter must not short-circuit")
	}
}

func TestJitterThenErrorBothApply(t *testing.T) {
	stage := chaos.NewFaultStage(profile(
		domain.Fault{Type: domain.FaultJitter, Probability: 1, Params: domain.FaultParams{BaseDelayMs: 100}},
		domain.Fault{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}},
	), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultDelayMs != 100 || !pctx.FaultShortCircuit || pctx.Response.Status != 503 {
		t.Fatalf("expected jitter+error, got delay=%d sc=%v", pctx.FaultDelayMs, pctx.FaultShortCircuit)
	}
}

func TestZeroProbabilityNeverFires(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultError, Probability: 0, Params: domain.FaultParams{StatusCode: 503},
	}), chaos.NewKillSwitch())
	for i := 0; i < 100; i++ {
		pctx := pctxWithProfile()
		_ = stage.Execute(context.Background(), pctx)
		if pctx.FaultShortCircuit {
			t.Fatal("probability 0 fired")
		}
	}
}

func TestKillSwitchDisablesFaults(t *testing.T) {
	ks := chaos.NewKillSwitch()
	ks.Halt()
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503},
	}), ks)
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("halted switch must disable faults")
	}
}

func TestDisabledProfileIsNoop(t *testing.T) {
	profiles := profile(domain.Fault{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}})
	p := profiles["p"]
	p.Enabled = false
	profiles["p"] = p
	stage := chaos.NewFaultStage(profiles, chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("disabled profile fired")
	}
}

func TestNoProfileIsNoop(t *testing.T) {
	stage := chaos.NewFaultStage(nil, chaos.NewKillSwitch())
	pctx := &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r"}}
	if err := stage.Execute(context.Background(), pctx); err != nil {
		t.Fatal(err)
	}
}

func TestHangFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultHang, Probability: 1, Params: domain.FaultParams{MaxMs: 5000},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if !pctx.FaultShortCircuit || pctx.ConnFault != "hang" || pctx.ConnFaultMaxMs != 5000 {
		t.Fatalf("expected hang directive, got %+v", pctx)
	}
}

func TestResetFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{Type: domain.FaultReset, Probability: 1}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if !pctx.FaultShortCircuit || pctx.ConnFault != "reset" {
		t.Fatalf("expected reset directive, got %+v", pctx)
	}
}

func TestHalfResponseFaultShortCircuits(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultHalfResponse, Probability: 1, Params: domain.FaultParams{Fraction: 0.5},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if !pctx.FaultShortCircuit || pctx.ConnFault != "halfResponse" || pctx.ConnFaultFraction != 0.5 {
		t.Fatalf("expected halfResponse directive, got %+v", pctx)
	}
}

func TestSlowBodyFaultIsModifier(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultSlowBody, Probability: 1, Params: domain.FaultParams{BytesPerSec: 2048},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("slowBody must not short-circuit")
	}
	if pctx.SlowBodyBytesPerSec != 2048 {
		t.Fatalf("expected throttle 2048, got %d", pctx.SlowBodyBytesPerSec)
	}
}

func TestSlowBodyCombinesWithError(t *testing.T) {
	stage := chaos.NewFaultStage(profile(
		domain.Fault{Type: domain.FaultSlowBody, Probability: 1, Params: domain.FaultParams{BytesPerSec: 512}},
		domain.Fault{Type: domain.FaultError, Probability: 1, Params: domain.FaultParams{StatusCode: 503}},
	), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.SlowBodyBytesPerSec != 512 || !pctx.FaultShortCircuit || pctx.Response.Status != 503 {
		t.Fatalf("expected slowBody + error, got throttle=%d sc=%v", pctx.SlowBodyBytesPerSec, pctx.FaultShortCircuit)
	}
}

func TestRetryStormFailsFirstThenPasses(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultRetryStorm, Probability: 1,
		Params: domain.FaultParams{FailFirst: 2, StatusCode: 503, KeyBy: "path", WindowSec: 60},
	}), chaos.NewKillSwitch())
	mk := func() *pipeline.PipelineContext {
		return &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r"}, FaultProfileID: "p",
			Request: pipeline.NormalizedRequest{Path: "/orders"}}
	}
	for i := 0; i < 2; i++ {
		pctx := mk()
		_ = stage.Execute(context.Background(), pctx)
		if !pctx.FaultShortCircuit || pctx.Response.Status != 503 {
			t.Fatalf("request %d should fail with 503", i)
		}
	}
	pctx := mk()
	_ = stage.Execute(context.Background(), pctx)
	if pctx.FaultShortCircuit {
		t.Fatal("third request should pass through")
	}
}

func TestRetryStormKeyByHeader(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultRetryStorm, Probability: 1,
		Params: domain.FaultParams{FailFirst: 1, StatusCode: 500, KeyBy: "header:x-request-id", WindowSec: 60},
	}), chaos.NewKillSwitch())
	mk := func(id string) *pipeline.PipelineContext {
		return &pipeline.PipelineContext{Matched: &domain.Rule{ID: "r"}, FaultProfileID: "p",
			Request: pipeline.NormalizedRequest{Path: "/x", Headers: map[string]string{"x-request-id": id}}}
	}
	p1 := mk("req-1")
	_ = stage.Execute(context.Background(), p1)
	if !p1.FaultShortCircuit {
		t.Fatal("first req-1 should fail")
	}
	p2 := mk("req-2")
	_ = stage.Execute(context.Background(), p2)
	if !p2.FaultShortCircuit {
		t.Fatal("first req-2 should fail (independent key)")
	}
	p1b := mk("req-1")
	_ = stage.Execute(context.Background(), p1b)
	if p1b.FaultShortCircuit {
		t.Fatal("second req-1 should pass")
	}
}

func TestErrorBodyParsedAsJSON(t *testing.T) {
	stage := chaos.NewFaultStage(profile(domain.Fault{
		Type: domain.FaultError, Probability: 1,
		Params: domain.FaultParams{StatusCode: 500, Body: `{"a":1}`},
	}), chaos.NewKillSwitch())
	pctx := pctxWithProfile()
	_ = stage.Execute(context.Background(), pctx)
	m, ok := pctx.Response.Body.(map[string]interface{})
	if !ok || m["a"] != float64(1) {
		t.Fatalf("expected parsed JSON body, got %T %v", pctx.Response.Body, pctx.Response.Body)
	}
}
