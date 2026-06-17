package jsonpath

import (
	"encoding/json"
	"testing"
)

func TestResolveAndLeaf(t *testing.T) {
	var root interface{}
	_ = json.Unmarshal([]byte(`{"order":{"id":7,"items":["a","b"],"paid":true}}`), &root)

	cases := []struct {
		expr string
		ok   bool
		leaf string
	}{
		{"$.order.id", true, "7"},
		{"order.id", true, "7"},
		{"$.order.items.1", true, "b"},
		{"$.order.paid", true, "true"},
		{"$.order.missing", false, ""},
		{"$.order", false, ""}, // non-leaf
	}
	for _, c := range cases {
		leaf, ok := Resolve(root, c.expr)
		if ok != c.ok {
			t.Fatalf("Resolve(%q) ok = %v, want %v", c.expr, ok, c.ok)
		}
		if ok && LeafToString(leaf) != c.leaf {
			t.Fatalf("Resolve(%q) leaf = %q, want %q", c.expr, LeafToString(leaf), c.leaf)
		}
	}
}
