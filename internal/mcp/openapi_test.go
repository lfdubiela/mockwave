package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixtureFile returns the absolute path to a testdata file.
func fixtureFile(name string) string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "testdata", name)
}

// findPair locates a GeneratedPair by rule ID.
func findPair(pairs []GeneratedPair, id string) *GeneratedPair {
	for i := range pairs {
		if pairs[i].Rule.ID == id {
			return &pairs[i]
		}
	}
	return nil
}

// --- FetchAndParse ---

func TestFetchSpec_File(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.NotEmpty(t, spec.Paths.Map())
}

func TestFetchSpec_URL(t *testing.T) {
	data, err := os.ReadFile(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(200)
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	spec, err := FetchAndParse(srv.URL + "/spec.yaml")
	require.NoError(t, err)
	require.NotNil(t, spec)
	assert.NotEmpty(t, spec.Paths.Map())
}

func TestFetchSpec_URLNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	_, err := FetchAndParse(srv.URL + "/missing.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch spec")
}

func TestFetchSpec_FileNotFound(t *testing.T) {
	_, err := FetchAndParse("/nonexistent/path/spec.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read spec")
}

func TestFetchSpec_InvalidYAML(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.yaml")
	require.NoError(t, os.WriteFile(bad, []byte("{{not yaml{{"), 0o600))

	_, err := FetchAndParse(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid spec")
}

func TestFetchSpec_UnsupportedVersion(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "spec.yaml")
	require.NoError(t, os.WriteFile(f, []byte("openapi: \"4.0\"\ninfo:\n  title: X\n  version: \"1.0\"\npaths: {}\n"), 0o600))

	_, err := FetchAndParse(f)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported spec version")
}

// --- GeneratePairs — OpenAPI 3.0 ---

func TestGeneratePairs_OpenAPI3(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, skipped, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)
	// /pets GET, /pets POST, /pets/{id} GET, /health GET — /no-success GET skipped
	assert.Len(t, pairs, 4)
	assert.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "no-success")

	p := findPair(pairs, "get-pets")
	require.NotNil(t, p, "expected pair with id=get-pets")
	assert.Equal(t, "GET", p.Rule.Match.Method)
	assert.Equal(t, "/pets", p.Rule.Match.Path)
	assert.Equal(t, "get-pets-sim", p.Rule.Buckets[0].SimulationID)
	assert.Equal(t, 100, p.Rule.Buckets[0].Weight)
	assert.Equal(t, "simulate", p.Rule.Buckets[0].Action)
	assert.Equal(t, "get-pets-sim", p.Simulation.ID)
	assert.Equal(t, "http", p.Simulation.Protocol)
	assert.Equal(t, 200, p.Simulation.Response.Status)
}

func TestGeneratePairs_IDGeneration(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)

	ids := make(map[string]bool)
	for _, p := range pairs {
		ids[p.Rule.ID] = true
	}
	// {id} normalised to literal "id"
	assert.True(t, ids["get-pets-id"], "expected get-pets-id, got ids: %v", ids)
}

func TestGeneratePairs_GlobPath(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)

	p := findPair(pairs, "get-pets-id")
	require.NotNil(t, p)
	assert.Equal(t, "/pets/*", p.Rule.Match.Path)
}

// --- Swagger 2.0 ---

func TestGeneratePairs_Swagger2(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore2.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)
	// /pets GET, /pets POST, /pets/{id} GET
	assert.Len(t, pairs, 3)

	p := findPair(pairs, "get-pets")
	require.NotNil(t, p)
	assert.Equal(t, "GET", p.Rule.Match.Method)
	assert.Equal(t, "/pets", p.Rule.Match.Path)
}

// --- Filters ---

func TestGeneratePairs_PathPrefixFilter(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "/pets", "")
	require.NoError(t, err)
	// /pets and /pets/{id} — /health and /no-success excluded
	assert.Len(t, pairs, 3)
	for _, p := range pairs {
		assert.True(t, len(p.Rule.Match.Path) >= len("/pets"), p.Rule.ID)
	}
}

func TestGeneratePairs_TagsFilter(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "", "Pets")
	require.NoError(t, err)
	// Only Pets-tagged: GET /pets, POST /pets, GET /pets/{id}
	assert.Len(t, pairs, 3)
}

func TestGeneratePairs_NoMatchFilter(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	_, _, err = GeneratePairs(spec, "/nonexistent", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no paths matched")
}

// --- Body generation ---

func TestGeneratePairs_ExamplePreferred(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)

	// GET /pets has example in spec
	p := findPair(pairs, "get-pets")
	require.NotNil(t, p)
	assert.NotNil(t, p.Simulation.Response.Body)
}

func TestGeneratePairs_SchemaBody(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)

	// GET /health has schema but no example → typed zero values
	p := findPair(pairs, "get-health")
	require.NotNil(t, p)
	body, ok := p.Simulation.Response.Body.(map[string]interface{})
	require.True(t, ok, "expected object body")
	_, hasStatus := body["status"]
	assert.True(t, hasStatus, "expected 'status' field from schema")
}

func TestGeneratePairs_SkipsNoSuccessResponse(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	_, skipped, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)
	assert.Len(t, skipped, 1)
	assert.Contains(t, skipped[0], "no-success")
}

// --- Status codes ---

func TestGeneratePairs_Status201(t *testing.T) {
	spec, err := FetchAndParse(fixtureFile("petstore3.yaml"))
	require.NoError(t, err)

	pairs, _, err := GeneratePairs(spec, "", "")
	require.NoError(t, err)

	p := findPair(pairs, "post-pets")
	require.NotNil(t, p)
	assert.Equal(t, 201, p.Simulation.Response.Status)
}
