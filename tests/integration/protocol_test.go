package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	// Explicitly import the gRPC adapter to guarantee RawCodec is registered via
	// its init(), even if server.go is ever refactored to drop its direct import.
	_ "github.com/mockwave/mockwave/internal/adapters/in/grpc"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// startProtocolServer starts an httptest.Server with the given protocols enabled.
func startProtocolServer(t *testing.T, protocols []string, cfg domain.Config) *httptest.Server {
	t.Helper()
	store := jsonfile.NewMemStore(cfg)
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)
	ts := httptest.NewServer(srv.MockHandler(protocols, srv.NewProxy()))
	t.Cleanup(ts.Close)
	return ts
}

// startGRPCServer starts a real gRPC server on a random port and returns its address.
func startGRPCServer(t *testing.T, cfg domain.Config) string {
	t.Helper()
	store := jsonfile.NewMemStore(cfg)
	srv, err := server.New(server.Config{Store: store})
	require.NoError(t, err)
	grpcSrv := srv.GRPCServer(nil, srv.NewProxy())
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			// Serve returns nil on GracefulStop/Stop; any other error is unexpected.
			t.Errorf("gRPC server exited unexpectedly: %v", err)
		}
	}()
	t.Cleanup(grpcSrv.GracefulStop)
	return lis.Addr().String()
}

// --------------------------------------------------------------------------
// GraphQL tests
// --------------------------------------------------------------------------

func TestIntegration_GraphQL_QuerySimulation(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "gql-query",
			Match:   domain.MatchCriteria{Protocol: "graphql", Method: "query", Path: "GetUser"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-get-user"}},
		}},
		Simulations: []domain.Simulation{{
			ID:       "sim-get-user",
			Protocol: "graphql",
			Response: domain.HTTPResponse{
				Status: 200,
				Body:   map[string]interface{}{"user": map[string]interface{}{"id": "1", "name": "Alice"}},
			},
		}},
	}

	ts := startProtocolServer(t, []string{"graphql"}, cfg)

	body := bytes.NewBufferString(`{"query":"query GetUser { user { id name } }"}`)
	resp, err := http.Post(ts.URL+"/graphql", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	data, ok := result["data"].(map[string]interface{})
	require.True(t, ok, "expected data key in response")
	user, ok := data["user"].(map[string]interface{})
	require.True(t, ok, "expected user object in data")
	assert.Equal(t, "1", user["id"])
	assert.Equal(t, "Alice", user["name"])
}

func TestIntegration_GraphQL_MutationSimulation(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "gql-mutation",
			Match:   domain.MatchCriteria{Protocol: "graphql", Method: "mutation", Path: "CreateUser"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-create-user"}},
		}},
		Simulations: []domain.Simulation{{
			ID:       "sim-create-user",
			Protocol: "graphql",
			Response: domain.HTTPResponse{
				Status: 200,
				Body:   map[string]interface{}{"id": "42"},
			},
		}},
	}

	ts := startProtocolServer(t, []string{"graphql"}, cfg)

	body := bytes.NewBufferString(`{"query":"mutation CreateUser { id }"}`)
	resp, err := http.Post(ts.URL+"/graphql", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	data, ok := result["data"].(map[string]interface{})
	require.True(t, ok, "expected data key in response")
	assert.Equal(t, "42", data["id"])
}

func TestIntegration_GraphQL_NoMatch(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "gql-known",
			Match:   domain.MatchCriteria{Protocol: "graphql", Method: "query", Path: "KnownOp"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-known"}},
		}},
		Simulations: []domain.Simulation{{
			ID:       "sim-known",
			Protocol: "graphql",
			Response: domain.HTTPResponse{Status: 200, Body: map[string]interface{}{"ok": true}},
		}},
	}

	ts := startProtocolServer(t, []string{"graphql"}, cfg)

	body := bytes.NewBufferString(`{"query":"query UnknownOperation { field }"}`)
	resp, err := http.Post(ts.URL+"/graphql", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 404, resp.StatusCode)

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	_, hasErrors := result["errors"]
	assert.True(t, hasErrors, "expected errors key in response")
}

// --------------------------------------------------------------------------
// SOAP tests
// --------------------------------------------------------------------------

func TestIntegration_SOAP_Simulation(t *testing.T) {
	const envelope = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUserResponse><id>1</id></GetUserResponse></soap:Body></soap:Envelope>`

	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "soap-get-user",
			Match:   domain.MatchCriteria{Protocol: "soap", Method: "POST", Path: "urn:GetUser"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-soap-user"}},
		}},
		Simulations: []domain.Simulation{{
			ID:           "sim-soap-user",
			Protocol:     "soap",
			SOAPEnvelope: envelope,
			Response:     domain.HTTPResponse{Status: 200},
		}},
	}

	ts := startProtocolServer(t, []string{"soap"}, cfg)

	reqBody := bytes.NewBufferString(`<soap:Envelope><soap:Body><GetUser/></soap:Body></soap:Envelope>`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", "urn:GetUser")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/xml")

	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, envelope, buf.String())
}

func TestIntegration_SOAP_QuotedSOAPAction(t *testing.T) {
	const envelope = `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><GetUserResponse><id>1</id></GetUserResponse></soap:Body></soap:Envelope>`

	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "soap-get-user-quoted",
			Match:   domain.MatchCriteria{Protocol: "soap", Method: "POST", Path: "urn:GetUser"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-soap-user-quoted"}},
		}},
		Simulations: []domain.Simulation{{
			ID:           "sim-soap-user-quoted",
			Protocol:     "soap",
			SOAPEnvelope: envelope,
			Response:     domain.HTTPResponse{Status: 200},
		}},
	}

	ts := startProtocolServer(t, []string{"soap"}, cfg)

	reqBody := bytes.NewBufferString(`<soap:Envelope><soap:Body><GetUser/></soap:Body></soap:Envelope>`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	// Quoted SOAPAction — handler must strip quotes to match the rule
	req.Header.Set("SOAPAction", `"urn:GetUser"`)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 200, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/xml")

	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, envelope, buf.String())
}

func TestIntegration_SOAP_NoMatch(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "soap-known",
			Match:   domain.MatchCriteria{Protocol: "soap", Method: "POST", Path: "urn:KnownAction"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-soap-known"}},
		}},
		Simulations: []domain.Simulation{{
			ID:           "sim-soap-known",
			Protocol:     "soap",
			SOAPEnvelope: `<soap:Envelope/>`,
			Response:     domain.HTTPResponse{Status: 200},
		}},
	}

	ts := startProtocolServer(t, []string{"soap"}, cfg)

	reqBody := bytes.NewBufferString(`<soap:Envelope><soap:Body><Unknown/></soap:Body></soap:Envelope>`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/", reqBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", "urn:Unknown")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, 404, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/xml")

	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	require.NoError(t, err)
	// Response must be a well-formed SOAP Fault envelope
	body := buf.String()
	assert.Contains(t, body, "soap:Envelope", "expected SOAP envelope wrapper")
	assert.Contains(t, body, "<soap:Fault>", "expected SOAP Fault element")
}

// --------------------------------------------------------------------------
// gRPC tests
// --------------------------------------------------------------------------

func TestIntegration_GRPC_SimulationResponse(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "grpc-ping",
			Match:   domain.MatchCriteria{Protocol: "grpc", Method: "/test.TestService/Ping", Path: "/test.TestService/Ping"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-grpc-ping"}},
		}},
		Simulations: []domain.Simulation{{
			ID:          "sim-grpc-ping",
			Protocol:    "grpc",
			GRPCMessage: `{"pong":true}`,
			GRPCStatus:  0,
		}},
	}

	addr := startGRPCServer(t, cfg)

	conn, err := googlegrpc.NewClient(addr,
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	ctx := context.Background()
	reqBytes := []byte(`{}`)
	var respBytes []byte
	err = conn.Invoke(ctx, "/test.TestService/Ping", reqBytes, &respBytes)
	require.NoError(t, err)
	assert.Equal(t, `{"pong":true}`, string(respBytes))
}

func TestIntegration_GRPC_NoMatch(t *testing.T) {
	cfg := domain.Config{
		Rules: []domain.Rule{{
			ID:      "grpc-ping-only",
			Match:   domain.MatchCriteria{Protocol: "grpc", Method: "/test.TestService/Ping", Path: "/test.TestService/Ping"},
			Buckets: []domain.WeightedBucket{{Weight: 1, Action: domain.ActionSimulate, SimulationID: "sim-grpc-ping-only"}},
		}},
		Simulations: []domain.Simulation{{
			ID:          "sim-grpc-ping-only",
			Protocol:    "grpc",
			GRPCMessage: `{"pong":true}`,
			GRPCStatus:  0,
		}},
	}

	addr := startGRPCServer(t, cfg)

	conn, err := googlegrpc.NewClient(addr,
		googlegrpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer conn.Close()

	ctx := context.Background()
	reqBytes := []byte(`{}`)
	var respBytes []byte
	err = conn.Invoke(ctx, "/test.TestService/Unknown", reqBytes, &respBytes)
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}
