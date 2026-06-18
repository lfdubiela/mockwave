package mcp

import "github.com/mockwave/mockwave/domain"

// --- Scenarios ---

func (c *Client) ListScenarios() ([]domain.Scenario, error) {
	var ss []domain.Scenario
	return ss, c.get("/api/scenarios", &ss)
}

func (c *Client) GetScenario(id string) (*domain.Scenario, error) {
	var s domain.Scenario
	return &s, c.get("/api/scenarios/"+id, &s)
}

func (c *Client) CreateScenario(s domain.Scenario) (*domain.Scenario, error) {
	var out domain.Scenario
	return &out, c.post("/api/scenarios", s, &out)
}

func (c *Client) UpdateScenario(id string, s domain.Scenario) (*domain.Scenario, error) {
	var out domain.Scenario
	return &out, c.put("/api/scenarios/"+id, s, &out)
}

func (c *Client) DeleteScenario(id string) error {
	return c.doDelete("/api/scenarios/" + id)
}

func (c *Client) StartScenario(id string) error {
	return c.post("/api/scenarios/"+id+"/start", nil, nil)
}

func (c *Client) StopScenario(id string) error {
	return c.post("/api/scenarios/"+id+"/stop", nil, nil)
}
