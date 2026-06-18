package mcp

import (
	"net/url"

	"github.com/mockwave/mockwave/internal/matched"
)

// --- Matched requests (HTTP capture) ---

// ListMatched returns a paginated page of matched HTTP requests for the given
// rule. q may carry filter/pagination params built by captureFilterArgs.
func (c *Client) ListMatched(rule string, q url.Values) (matched.Page, error) {
	var page matched.Page
	return page, c.getWithQuery("/api/matched/"+rule, q, &page)
}

// GetMatched returns the full matched request (including bodies) for
// rule + request id.
func (c *Client) GetMatched(rule, id string) (*matched.FullRequest, error) {
	var full matched.FullRequest
	return &full, c.get("/api/matched/"+rule+"/"+id, &full)
}

// ClearMatched deletes all matched requests for the given rule.
func (c *Client) ClearMatched(rule string) error {
	return c.doDelete("/api/matched/" + rule)
}
