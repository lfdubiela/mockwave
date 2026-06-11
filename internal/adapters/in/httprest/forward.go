package httprest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

type ForwardStage struct {
	client *http.Client
}

func NewForwardStage(client *http.Client) *ForwardStage {
	if client == nil {
		client = &http.Client{}
	}
	return &ForwardStage{client: client}
}

func (s *ForwardStage) Name() string { return "forward" }

func (s *ForwardStage) Execute(_ context.Context, pctx *pipeline.PipelineContext) error {
	if pctx.FaultShortCircuit {
		return nil
	}
	if !pctx.ShouldForward {
		return nil
	}
	if pctx.ForwardURL == "" {
		return fmt.Errorf("forward: no forward_url configured on selected bucket")
	}

	// Start the delay timer NOW so it runs concurrently with the upstream call.
	// Net effect on return is max(delay, upstreamLatency).
	var delay <-chan time.Time
	if pctx.ForwardDelayMs > 0 {
		delay = time.After(time.Duration(pctx.ForwardDelayMs) * time.Millisecond)
	}

	targetURL := strings.TrimRight(pctx.ForwardURL, "/") + pctx.Request.Path
	if pctx.Request.RawQuery != "" {
		// Forward the original percent-encoded query verbatim; the decoded map
		// loses encoding, multi-value params, and order.
		targetURL += "?" + pctx.Request.RawQuery
	} else if len(pctx.Request.Query) > 0 {
		params := url.Values{}
		for k, v := range pctx.Request.Query {
			params.Set(k, v)
		}
		targetURL += "?" + params.Encode()
	}
	req, err := http.NewRequest(pctx.Request.Method, targetURL, io.NopCloser(strings.NewReader(string(pctx.Request.Body))))
	if err != nil {
		return fmt.Errorf("forward: build request: %w", err)
	}
	for k, v := range pctx.Request.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("forward: upstream request: %w", err)
	}
	defer resp.Body.Close()
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("forward: read upstream body: %w", err)
	}
	headers := make(map[string]string)
	for k := range resp.Header {
		headers[strings.ToLower(k)] = resp.Header.Get(k)
	}
	var parsedBody interface{}
	_ = json.Unmarshal(rawBody, &parsedBody)
	if parsedBody == nil {
		parsedBody = string(rawBody)
	}
	pctx.Response = &pipeline.MockResponse{
		Status:  resp.StatusCode,
		Headers: headers,
		Body:    parsedBody,
	}
	// Drain the delay timer so total latency is max(delay, upstream). On an
	// upstream error we return early above and intentionally skip the delay.
	if delay != nil {
		<-delay
	}
	return nil
}
