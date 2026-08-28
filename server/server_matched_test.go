package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_MatchedBufferExposed(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "s1"}},
		}},
		Simulations: []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}}}},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	require.NotNil(t, srv.MatchedBuffer())

	h := srv.MockHandler([]string{"http"}, srv.NewProxy())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))
	assert.Equal(t, 200, rec.Code)

	page := srv.MatchedBuffer().List("r1", matched.Query{})
	require.Len(t, page.Items, 1)
	assert.Equal(t, "/ping", page.Items[0].Path)
}

func TestServer_MatchedDisabledByDefault(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{})
	srv, err := server.New(server.Config{Store: st})
	require.NoError(t, err)
	defer srv.Close()
	assert.Nil(t, srv.MatchedBuffer())
}

func TestServer_MatchedAdminEndpoint(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		Rules: []domain.Rule{{
			ID:      "r1",
			Match:   domain.MatchCriteria{Protocol: "http", Method: "GET", Path: "/ping"},
			Buckets: []domain.WeightedBucket{{Weight: 100, Action: "simulate", SimulationID: "s1"}},
		}},
		Simulations: []domain.Simulation{{ID: "s1", Protocol: "http", Response: domain.HTTPResponse{Status: 200, Body: map[string]any{"ok": true}}}},
	})
	srv, err := server.New(server.Config{
		Store:   st,
		Matched: server.MatchedConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	h := srv.MockHandler([]string{"http"}, srv.NewProxy())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", nil))

	mux := srv.AdminMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/matched/r1", nil))
	require.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Body.String(), `"/ping"`)
}
