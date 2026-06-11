package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAdmin records the last request and replies with the given status/body.
type fakeAdmin struct {
	method string
	path   string
	body   []byte

	status   int
	respBody string
}

func (f *fakeAdmin) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.method = r.Method
		f.path = r.URL.Path
		f.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.respBody))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRunFaultList(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusOK, respBody: `[{"id":"f1"}]`}
	srv := fa.server(t)

	var out bytes.Buffer
	require.NoError(t, runFaultList(srv.URL, &out))
	assert.Equal(t, http.MethodGet, fa.method)
	assert.Equal(t, "/api/faults", fa.path)
	assert.Equal(t, `[{"id":"f1"}]`, out.String())
}

func TestRunFaultGet(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusOK, respBody: `{"id":"f1"}`}
	srv := fa.server(t)

	var out bytes.Buffer
	require.NoError(t, runFaultGet(srv.URL, "f1", &out))
	assert.Equal(t, http.MethodGet, fa.method)
	assert.Equal(t, "/api/faults/f1", fa.path)
	assert.Equal(t, `{"id":"f1"}`, out.String())
}

func TestRunFaultCreate(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusCreated}
	srv := fa.server(t)

	profile := `{"id":"f1","type":"latency"}`
	path := filepath.Join(t.TempDir(), "profile.json")
	require.NoError(t, os.WriteFile(path, []byte(profile), 0o644))

	var out bytes.Buffer
	require.NoError(t, runFaultCreate(srv.URL, path, &out))
	assert.Equal(t, http.MethodPost, fa.method)
	assert.Equal(t, "/api/faults", fa.path)
	assert.Equal(t, profile, string(fa.body))
	assert.Equal(t, "created\n", out.String())
}

func TestRunFaultCreate_InvalidJSON(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusCreated}
	srv := fa.server(t)

	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))

	var out bytes.Buffer
	err := runFaultCreate(srv.URL, path, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not valid JSON")
	// must be rejected locally, never sent to the server
	assert.Empty(t, fa.method)
}

func TestRunFaultCreate_MissingFile(t *testing.T) {
	var out bytes.Buffer
	require.Error(t, runFaultCreate("http://unused", "/nonexistent/profile.json", &out))
}

func TestRunFaultDelete(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusNoContent}
	srv := fa.server(t)

	require.NoError(t, runFaultDelete(srv.URL, "f1"))
	assert.Equal(t, http.MethodDelete, fa.method)
	assert.Equal(t, "/api/faults/f1", fa.path)
}

func TestRunChaosHaltResume(t *testing.T) {
	for _, path := range []string{"/api/chaos/halt", "/api/chaos/resume"} {
		t.Run(path, func(t *testing.T) {
			fa := &fakeAdmin{status: http.StatusNoContent}
			srv := fa.server(t)

			require.NoError(t, runChaosPost(srv.URL, path))
			assert.Equal(t, http.MethodPost, fa.method)
			assert.Equal(t, path, fa.path)
		})
	}
}

func TestRunChaosStatus(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusOK, respBody: `{"halted":false}`}
	srv := fa.server(t)

	var out bytes.Buffer
	require.NoError(t, runChaosStatus(srv.URL, &out))
	assert.Equal(t, http.MethodGet, fa.method)
	assert.Equal(t, "/api/chaos/status", fa.path)
	assert.Equal(t, `{"halted":false}`, out.String())
}

func TestAdminError_NonOK(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusInternalServerError, respBody: "boom"}
	srv := fa.server(t)

	var out bytes.Buffer
	err := runFaultList(srv.URL, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "boom")
}

func TestRunScenarioList(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusOK, respBody: `[{"id":"s1"}]`}
	srv := fa.server(t)

	var out bytes.Buffer
	require.NoError(t, runScenarioList(srv.URL, &out))
	assert.Equal(t, http.MethodGet, fa.method)
	assert.Equal(t, "/api/scenarios", fa.path)
	assert.Equal(t, `[{"id":"s1"}]`, out.String())
}

func TestRunScenarioStart(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusAccepted}
	srv := fa.server(t)

	require.NoError(t, runScenarioStart(srv.URL, "s1"))
	assert.Equal(t, http.MethodPost, fa.method)
	assert.Equal(t, "/api/scenarios/s1/start", fa.path)
}

func TestRunScenarioStop(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusNoContent}
	srv := fa.server(t)

	require.NoError(t, runScenarioStop(srv.URL, "s1"))
	assert.Equal(t, http.MethodPost, fa.method)
	assert.Equal(t, "/api/scenarios/s1/stop", fa.path)
}

func TestRunScenario_NonOK(t *testing.T) {
	fa := &fakeAdmin{status: http.StatusInternalServerError, respBody: "boom"}
	srv := fa.server(t)

	err := runScenarioStart(srv.URL, "s1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "boom")
}

func TestFaultAndChaosCmd_Registered(t *testing.T) {
	root := rootCmd()
	for _, name := range []string{"fault", "chaos", "scenario"} {
		found := false
		for _, c := range root.Commands() {
			if c.Name() == name {
				found = true
			}
		}
		assert.True(t, found, "command %q not registered", name)
	}
}
