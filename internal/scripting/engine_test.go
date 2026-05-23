package scripting_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/scripting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngine_Run_ModifiesBody(t *testing.T) {
	engine := scripting.NewEngine()
	req := map[string]interface{}{"headers": map[string]interface{}{"x-cid": "12345"}}
	resp := map[string]interface{}{"status": 200, "body": map[string]interface{}{"name": "mock"}}
	result, err := engine.Run(`response.body.cid = request.headers["x-cid"]; return response;`, req, resp)
	require.NoError(t, err)
	body := result["body"].(map[string]interface{})
	assert.Equal(t, "12345", body["cid"])
}

func TestEngine_Run_ModifiesStatus(t *testing.T) {
	engine := scripting.NewEngine()
	resp := map[string]interface{}{"status": 200, "body": nil}
	result, err := engine.Run(`response.status = 422; return response;`, nil, resp)
	require.NoError(t, err)
	assert.EqualValues(t, 422, result["status"])
}

func TestEngine_Run_InvalidScript(t *testing.T) {
	engine := scripting.NewEngine()
	_, err := engine.Run(`this is not valid js !!!`, nil, map[string]interface{}{})
	assert.Error(t, err)
}

func TestEngine_Run_ScriptThrows(t *testing.T) {
	engine := scripting.NewEngine()
	_, err := engine.Run(`throw new Error("intentional");`, nil, map[string]interface{}{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "intentional")
}

func TestEngine_Run_EmptyScript(t *testing.T) {
	engine := scripting.NewEngine()
	resp := map[string]interface{}{"status": 200}
	result, err := engine.Run(``, nil, resp)
	require.NoError(t, err)
	assert.Equal(t, resp, result)
}
