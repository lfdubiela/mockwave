package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/mockwave/mockwave/domain"
)

// Client makes HTTP requests to a Mockwave admin API.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a Client targeting adminURL (e.g. "http://localhost:9090").
func NewClient(adminURL string) *Client {
	return &Client{baseURL: adminURL, http: &http.Client{Timeout: 30 * time.Second}}
}

// --- Rules ---

func (c *Client) ListRules() ([]domain.Rule, error) {
	var rules []domain.Rule
	return rules, c.get("/api/rules", &rules)
}

func (c *Client) GetRule(id string) (*domain.Rule, error) {
	var rule domain.Rule
	return &rule, c.get("/api/rules/"+id, &rule)
}

func (c *Client) CreateRule(r domain.Rule) (*domain.Rule, error) {
	var out domain.Rule
	return &out, c.post("/api/rules", r, &out)
}

func (c *Client) UpdateRule(id string, r domain.Rule) (*domain.Rule, error) {
	var out domain.Rule
	return &out, c.put("/api/rules/"+id, r, &out)
}

func (c *Client) DeleteRule(id string) error {
	return c.doDelete("/api/rules/" + id)
}

// --- Simulations ---

func (c *Client) ListSimulations() ([]domain.Simulation, error) {
	var sims []domain.Simulation
	return sims, c.get("/api/simulations", &sims)
}

func (c *Client) GetSimulation(id string) (*domain.Simulation, error) {
	var sim domain.Simulation
	return &sim, c.get("/api/simulations/"+id, &sim)
}

func (c *Client) CreateSimulation(s domain.Simulation) (*domain.Simulation, error) {
	var out domain.Simulation
	return &out, c.post("/api/simulations", s, &out)
}

func (c *Client) UpdateSimulation(id string, s domain.Simulation) (*domain.Simulation, error) {
	var out domain.Simulation
	return &out, c.put("/api/simulations/"+id, s, &out)
}

func (c *Client) DeleteSimulation(id string) error {
	return c.doDelete("/api/simulations/" + id)
}

// --- Observability ---

func (c *Client) GetMetrics() (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.get("/api/metrics", &raw)
}

func (c *Client) ListUnmatched() (json.RawMessage, error) {
	var raw json.RawMessage
	return raw, c.get("/api/unmatched", &raw)
}

func (c *Client) ClearUnmatched() error {
	return c.doDelete("/api/unmatched")
}

func (c *Client) Reload() error {
	return c.post("/api/reload", nil, nil)
}

func (c *Client) Health() error {
	var raw json.RawMessage
	return c.get("/api/health", &raw)
}

// --- HTTP helpers ---

func (c *Client) get(path string, out any) error {
	resp, err := c.http.Get(c.baseURL + path)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

// GetWithQuery issues a GET to path with the given query params and decodes the
// JSON response into dst.
func (c *Client) GetWithQuery(path string, q url.Values, dst any) error {
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	return c.get(path, dst)
}

func (c *Client) post(path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	resp, err := c.http.Post(c.baseURL+path, "application/json", r)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func (c *Client) put(path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	return decode(resp, out)
}

func (c *Client) doDelete(path string) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func decode(resp *http.Response, out any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
