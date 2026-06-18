package matched

import (
	"encoding/base64"
	"path"
	"strings"
)

// DefaultLimit is the page size used when a query omits Limit.
const DefaultLimit = 20

// MaxLimit caps the page size a caller may request.
const MaxLimit = 100

// Query filters a rule's captured requests and controls pagination.
type Query struct {
	Cursor  string            // opaque; "" starts from the newest page
	Limit   int               // 0 → DefaultLimit; capped at MaxLimit
	Method  string            // exact, case-insensitive; "" = any
	Path    string            // glob (path.Match); "" = any
	Status  int               // exact response status; 0 = any
	Headers map[string]string // AND-matched, exact value, case-insensitive key
	Query   map[string]string // matched against Request.Query, exact key (case-sensitive) + value
	Body    map[string]string // JSONPath expr → expected scalar value (applied in Buffer.List, not here)
}

// EffectiveLimit resolves Limit against the default and cap.
func (q Query) EffectiveLimit() int {
	if q.Limit <= 0 {
		return DefaultLimit
	}
	if q.Limit > MaxLimit {
		return MaxLimit
	}
	return q.Limit
}

// Matches reports whether r satisfies every filter in q. A zero-value Query
// matches everything.
func (q Query) Matches(r Request) bool {
	if q.Method != "" && !strings.EqualFold(q.Method, r.Method) {
		return false
	}
	if q.Path != "" {
		ok, err := path.Match(q.Path, r.Path)
		if err != nil || !ok {
			return false
		}
	}
	if q.Status != 0 && q.Status != r.ResponseStatus {
		return false
	}
	for k, v := range q.Headers {
		if headerLookup(r.Headers, k) != v {
			return false
		}
	}
	for k, v := range q.Query {
		if r.Query[k] != v {
			return false
		}
	}
	return true
}

// headerLookup finds key case-insensitively, returning "" when absent.
func headerLookup(h map[string]string, key string) string {
	if hv, ok := h[key]; ok {
		return hv
	}
	for hk, hv := range h {
		if strings.EqualFold(hk, key) {
			return hv
		}
	}
	return ""
}

// Page is one slice of list results.
type Page struct {
	Items      []Request `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

// EncodeCursor wraps an id as an opaque cursor token.
func EncodeCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}

// DecodeCursor reverses EncodeCursor. An empty cursor decodes to "".
func DecodeCursor(c string) (string, error) {
	if c == "" {
		return "", nil
	}
	b, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
