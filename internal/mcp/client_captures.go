package mcp

import (
	"net/url"

	"github.com/mockwave/mockwave/internal/matched"
)

// --- Event captures ---

// ListEventCaptures returns a paginated page of captured requests for the given
// rule. q may carry filter/pagination params built by captureFilterArgs.
func (c *Client) ListEventCaptures(rule string, q url.Values) (matched.Page, error) {
	var page matched.Page
	return page, c.getWithQuery("/api/event-captures/"+rule, q, &page)
}

// GetEventCapture returns the full captured request (including bodies) for
// rule + capture id.
func (c *Client) GetEventCapture(rule, id string) (*matched.FullRequest, error) {
	var full matched.FullRequest
	return &full, c.get("/api/event-captures/"+rule+"/"+id, &full)
}

// ClearEventCaptures deletes all captured requests for the given rule.
func (c *Client) ClearEventCaptures(rule string) error {
	return c.doDelete("/api/event-captures/" + rule)
}
