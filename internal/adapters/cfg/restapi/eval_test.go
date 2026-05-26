package restapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/cfg/restapi"
	"github.com/mockwave/mockwave/internal/scripting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func evalMux() *http.ServeMux {
	return restapi.NewMux(&memStore{}, nil, nil, nil, nil, scripting.NewEngine())
}

func TestEval_Success(t *testing.T) {
	mux := evalMux()
	body, _ := json.Marshal(map[string]string{
		"script": `return {"method": request.method, "ok": true};`,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/script/eval", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	var resp struct {
		Result     map[string]interface{} `json:"result"`
		Error      *string                `json:"error"`
		DurationMs int                    `json:"duration_ms"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Nil(t, resp.Error)
	assert.Equal(t, "GET", resp.Result["method"])
	assert.Equal(t, true, resp.Result["ok"])
}

func TestEval_ScriptError(t *testing.T) {
	mux := evalMux()
	body, _ := json.Marshal(map[string]string{
		"script": `return undefinedVariable.foo;`,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/script/eval", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code) // 200 even on script error — error is in body

	var resp struct {
		Result interface{} `json:"result"`
		Error  *string     `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp.Error)
	assert.Nil(t, resp.Result)
}

func TestEval_InvalidJSON(t *testing.T) {
	mux := evalMux()
	req := httptest.NewRequest(http.MethodPost, "/api/script/eval", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 400, w.Code)
}

func TestEval_MethodNotAllowed(t *testing.T) {
	mux := evalMux()
	req := httptest.NewRequest(http.MethodGet, "/api/script/eval", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 405, w.Code)
}

func TestEval_NilEngine(t *testing.T) {
	mux := restapi.NewMux(&memStore{}, nil, nil, nil, nil, nil)
	body, _ := json.Marshal(map[string]string{"script": "return {};"})
	req := httptest.NewRequest(http.MethodPost, "/api/script/eval", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 503, w.Code)
}

func TestEval_EmptyScript(t *testing.T) {
	mux := evalMux()
	body, _ := json.Marshal(map[string]string{"script": ""})
	req := httptest.NewRequest(http.MethodPost, "/api/script/eval", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)

	var resp struct {
		Result interface{} `json:"result"`
		Error  *string     `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Nil(t, resp.Error)
	// result may be empty map or nil depending on engine behavior — just verify no error
}

func TestEval_Timeout(t *testing.T) {
	mux := evalMux()
	body, _ := json.Marshal(map[string]string{
		"script": `while(true){}`,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/script/eval", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
	var resp struct {
		Error *string `json:"error"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotNil(t, resp.Error)
	assert.Contains(t, *resp.Error, "timed out")
}
