package scripting

import (
	"fmt"

	"github.com/dop251/goja"
)

type Engine struct{}

func NewEngine() *Engine { return &Engine{} }

func (e *Engine) Run(script string, req map[string]interface{}, resp map[string]interface{}) (map[string]interface{}, error) {
	if script == "" {
		return resp, nil
	}
	vm := goja.New()
	if req != nil {
		_ = vm.Set("request", req)
	} else {
		_ = vm.Set("request", map[string]interface{}{})
	}
	_ = vm.Set("response", resp)

	wrapped := fmt.Sprintf("(function(request, response){ %s })(request, response)", script)
	val, err := vm.RunString(wrapped)
	if err != nil {
		return nil, fmt.Errorf("script error: %w", err)
	}
	result, ok := val.Export().(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("script must return response object, got %T", val.Export())
	}
	return result, nil
}
