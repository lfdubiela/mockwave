package integration_test

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connFaultConfig builds a rule that always simulates a body of the given size
// (so half/slow body assertions have a known full length) with the supplied
// connection-fault profile attached to its bucket.
func connFaultConfig(profile domain.FaultProfile, body string) domain.Config {
	return domain.Config{
		Rules: []domain.Rule{{
			ID:    "r-chaos",
			Match: domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/chaos"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate,
				SimulationID: "sim-ok", FaultProfileID: profile.ID,
			}},
		}},
		Simulations: []domain.Simulation{{
			ID: "sim-ok", Protocol: "http",
			Response: domain.HTTPResponse{Status: 200, Body: body},
		}},
		FaultProfiles: []domain.FaultProfile{profile},
	}
}

// generousClient avoids hanging the whole test run on hang/slowBody faults.
func generousClient() *http.Client { return &http.Client{Timeout: 10 * time.Second} }

func TestIntegration_Chaos_ResetFault(t *testing.T) {
	profile := domain.FaultProfile{
		ID: "p-reset", Name: "reset", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultReset, Probability: 1}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, "hello"))
	client := generousClient()
	resp, err := client.Get(ts.URL + "/chaos")
	// TCP RST observability varies across OSes. Relax (mirroring the adapter
	// tests): accept either a connection error, or any response that is not a
	// normal full 200 with the expected body.
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 && readErr == nil && string(body) == "hello" {
		t.Fatalf("expected reset to disrupt the response, got a clean 200")
	}
}

func TestIntegration_Chaos_HangFault(t *testing.T) {
	profile := domain.FaultProfile{
		ID: "p-hang", Name: "hang", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultHang, Probability: 1,
			Params: domain.FaultParams{MaxMs: 300},
		}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, "hello"))
	client := generousClient()
	start := time.Now()
	resp, err := client.Get(ts.URL + "/chaos")
	elapsed := time.Since(start)
	if err == nil {
		resp.Body.Close()
	}
	assert.GreaterOrEqual(t, elapsed, 280*time.Millisecond, "hang must block ~max_ms before closing")
}

func TestIntegration_Chaos_HalfResponseFault(t *testing.T) {
	full := strings.Repeat("A", 1000)
	profile := domain.FaultProfile{
		ID: "p-half", Name: "half", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultHalfResponse, Probability: 1,
			Params: domain.FaultParams{Fraction: 0.5},
		}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, full))
	client := generousClient()
	resp, err := client.Get(ts.URL + "/chaos")
	if err != nil {
		// Truncation may surface as a connection error; acceptable.
		return
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return // read error from truncated body is acceptable
	}
	assert.Less(t, len(body), len(full), "halfResponse must deliver a truncated body")
}

func TestIntegration_Chaos_SlowBodyFault(t *testing.T) {
	// ~2KB body at 2048 B/s ≈ 1s, comfortably clears the 500ms floor.
	full := strings.Repeat("B", 2048)
	profile := domain.FaultProfile{
		ID: "p-slow", Name: "slow", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultSlowBody, Probability: 1,
			Params: domain.FaultParams{BytesPerSec: 2048},
		}},
	}
	ts, _ := newChaosServer(t, connFaultConfig(profile, full))
	client := generousClient()
	start := time.Now()
	resp, err := client.Get(ts.URL + "/chaos")
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	assert.GreaterOrEqual(t, time.Since(start), 500*time.Millisecond, "slowBody must throttle the body write")
}

// --------------------------------------------------------------------------
// GraphQL / SOAP conn-fault coverage through the full server pipeline.
//
// These exercise the real FaultStage, which leaves Response nil for hang/reset
// conn faults. The protocol adapter must therefore check ConnFault before its
// nil-Response guard; otherwise these requests would return a clean 500.
// --------------------------------------------------------------------------

func resetProfile() domain.FaultProfile {
	return domain.FaultProfile{
		ID: "p-reset", Name: "reset", Enabled: true,
		Faults: []domain.Fault{{Type: domain.FaultReset, Probability: 1}},
	}
}

func hangProfile(maxMs int) domain.FaultProfile {
	return domain.FaultProfile{
		ID: "p-hang", Name: "hang", Enabled: true,
		Faults: []domain.Fault{{
			Type: domain.FaultHang, Probability: 1,
			Params: domain.FaultParams{MaxMs: maxMs},
		}},
	}
}

func graphqlConnFaultConfig(profile domain.FaultProfile) domain.Config {
	return domain.Config{
		Rules: []domain.Rule{{
			ID:    "gql-chaos",
			Match: domain.MatchCriteria{Protocol: "graphql", Method: "query", Path: "GetUser"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate,
				SimulationID: "sim-gql", FaultProfileID: profile.ID,
			}},
		}},
		Simulations: []domain.Simulation{{
			ID: "sim-gql", Protocol: "graphql",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"ok": true}},
		}},
		FaultProfiles: []domain.FaultProfile{profile},
	}
}

func soapConnFaultConfig(profile domain.FaultProfile) domain.Config {
	const envelope = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUserResponse><id>1</id></GetUserResponse></soap:Body></soap:Envelope>`
	return domain.Config{
		Rules: []domain.Rule{{
			ID:    "soap-chaos",
			Match: domain.MatchCriteria{Protocol: "soap", Method: "POST", Path: "urn:GetUser"},
			Buckets: []domain.WeightedBucket{{
				Weight: 100, Action: domain.ActionSimulate,
				SimulationID: "sim-soap", FaultProfileID: profile.ID,
			}},
		}},
		Simulations: []domain.Simulation{{
			ID: "sim-soap", Protocol: "soap",
			SOAPEnvelope: envelope,
			Response:     domain.HTTPResponse{Status: 200},
		}},
		FaultProfiles: []domain.FaultProfile{profile},
	}
}

func TestIntegration_Chaos_GraphQL_ResetFault(t *testing.T) {
	ts := startProtocolServer(t, []string{"graphql"}, graphqlConnFaultConfig(resetProfile()))
	client := generousClient()
	resp, err := client.Post(ts.URL+"/graphql", "application/json",
		bytes.NewBufferString(`{"query":"query GetUser { user { id } }"}`))
	if err != nil {
		return // connection error is an acceptable reset outcome
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusInternalServerError {
		t.Fatal("reset must not hit the nil-Response guard (500)")
	}
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 && readErr == nil && strings.Contains(string(body), `"ok"`) {
		t.Fatalf("expected reset to disrupt the response, got a clean 200")
	}
}

func TestIntegration_Chaos_GraphQL_HangFault(t *testing.T) {
	ts := startProtocolServer(t, []string{"graphql"}, graphqlConnFaultConfig(hangProfile(300)))
	client := generousClient()
	start := time.Now()
	resp, err := client.Post(ts.URL+"/graphql", "application/json",
		bytes.NewBufferString(`{"query":"query GetUser { user { id } }"}`))
	elapsed := time.Since(start)
	if err == nil {
		if resp.StatusCode == http.StatusInternalServerError {
			resp.Body.Close()
			t.Fatal("hang must not hit the nil-Response guard (500)")
		}
		resp.Body.Close()
	}
	assert.GreaterOrEqual(t, elapsed, 280*time.Millisecond, "hang must block ~max_ms before closing")
}

func soapRequest(t *testing.T, url string) (*http.Request, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url+"/",
		bytes.NewBufferString(`<soap:Envelope><soap:Body><GetUser/></soap:Body></soap:Envelope>`))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", "urn:GetUser")
	return req, nil
}

func TestIntegration_Chaos_SOAP_ResetFault(t *testing.T) {
	ts := startProtocolServer(t, []string{"soap"}, soapConnFaultConfig(resetProfile()))
	client := generousClient()
	req, err := soapRequest(t, ts.URL)
	require.NoError(t, err)
	resp, err := client.Do(req)
	if err != nil {
		return // connection error is an acceptable reset outcome
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusInternalServerError {
		t.Fatal("reset must not hit the nil-Response guard (500)")
	}
	body, readErr := io.ReadAll(resp.Body)
	if resp.StatusCode == 200 && readErr == nil && strings.Contains(string(body), "GetUserResponse") {
		t.Fatalf("expected reset to disrupt the response, got a clean 200")
	}
}

func TestIntegration_Chaos_SOAP_HangFault(t *testing.T) {
	ts := startProtocolServer(t, []string{"soap"}, soapConnFaultConfig(hangProfile(300)))
	client := generousClient()
	req, err := soapRequest(t, ts.URL)
	require.NoError(t, err)
	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err == nil {
		if resp.StatusCode == http.StatusInternalServerError {
			resp.Body.Close()
			t.Fatal("hang must not hit the nil-Response guard (500)")
		}
		resp.Body.Close()
	}
	assert.GreaterOrEqual(t, elapsed, 280*time.Millisecond, "hang must block ~max_ms before closing")
}
