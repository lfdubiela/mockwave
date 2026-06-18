package mcp

import "github.com/mockwave/mockwave/domain"

// --- Fault profiles ---

func (c *Client) ListFaultProfiles() ([]domain.FaultProfile, error) {
	var fps []domain.FaultProfile
	return fps, c.get("/api/faults", &fps)
}

// GetFaultProfile fetches a single fault profile by ID via GET /api/faults/{id}.
// Unlike event-rules, the faults admin endpoint has a real single-item GET.
func (c *Client) GetFaultProfile(id string) (*domain.FaultProfile, error) {
	var fp domain.FaultProfile
	return &fp, c.get("/api/faults/"+id, &fp)
}

func (c *Client) CreateFaultProfile(fp domain.FaultProfile) (*domain.FaultProfile, error) {
	var out domain.FaultProfile
	return &out, c.post("/api/faults", fp, &out)
}

func (c *Client) UpdateFaultProfile(id string, fp domain.FaultProfile) (*domain.FaultProfile, error) {
	var out domain.FaultProfile
	return &out, c.put("/api/faults/"+id, fp, &out)
}

func (c *Client) DeleteFaultProfile(id string) error {
	return c.doDelete("/api/faults/" + id)
}
