// Package jsonpath resolves dotted JSONPath-lite expressions against decoded
// JSON values. Shared by the HTTP rule matcher and the event matcher.
package jsonpath

import (
	"fmt"
	"strconv"
	"strings"
)

// Resolve walks expr (e.g. "$.order.id" or "order.items.0") against root and
// returns the leaf scalar. ok is false when the path is missing or lands on a
// container (map/array) rather than a scalar.
func Resolve(root interface{}, expr string) (interface{}, bool) {
	expr = strings.TrimPrefix(expr, "$")
	expr = strings.TrimPrefix(expr, ".")
	if expr == "" {
		return nil, false
	}
	cur := root
	for _, seg := range strings.Split(expr, ".") {
		switch node := cur.(type) {
		case map[string]interface{}:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []interface{}:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	switch cur.(type) {
	case map[string]interface{}, []interface{}:
		return nil, false
	}
	return cur, true
}

// LeafToString renders a resolved scalar as a string for exact comparison.
func LeafToString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}
