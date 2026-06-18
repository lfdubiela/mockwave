package mcp

import "github.com/mockwave/mockwave/domain"

// --- Event rules ---

func (c *Client) ListEventRules() ([]domain.EventRule, error) {
	var rules []domain.EventRule
	return rules, c.get("/api/event-rules", &rules)
}

// GetEventRule returns the event rule with the given id, or nil if not found.
// The admin API has no GET /api/event-rules/{id}, so this filters the list.
func (c *Client) GetEventRule(id string) (*domain.EventRule, error) {
	rules, err := c.ListEventRules()
	if err != nil {
		return nil, err
	}
	for i := range rules {
		if rules[i].ID == id {
			return &rules[i], nil
		}
	}
	return nil, nil
}

func (c *Client) CreateEventRule(r domain.EventRule) (*domain.EventRule, error) {
	var out domain.EventRule
	return &out, c.post("/api/event-rules", r, &out)
}

func (c *Client) UpdateEventRule(id string, r domain.EventRule) (*domain.EventRule, error) {
	var out domain.EventRule
	return &out, c.put("/api/event-rules/"+id, r, &out)
}

func (c *Client) DeleteEventRule(id string) error {
	return c.doDelete("/api/event-rules/" + id)
}
