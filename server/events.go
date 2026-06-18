package server

import (
	"os"
	"strings"
	"time"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// EventConfig configures AWS event interception + capture. Disabled by default.
// Explicit non-zero fields win; otherwise env vars fill in; otherwise defaults.
type EventConfig struct {
	Enabled      bool
	TTL          time.Duration      // capture expiry; default 1h
	BufferSize   int                // in-memory capacity; default 10000
	SyncInterval time.Duration      // write-behind cadence; default 30s
	Store        store.MatchedStore // BYO override; nil → derived from backend
}

func resolveEventConfig(in EventConfig) EventConfig {
	out := in
	if !out.Enabled && os.Getenv("MOCKWAVE_EVENT_CAPTURE") == "true" {
		out.Enabled = true
	}
	if out.TTL <= 0 {
		if v := envInt("MOCKWAVE_EVENT_TTL", 0); v > 0 {
			out.TTL = time.Duration(v) * time.Second
		} else {
			out.TTL = time.Hour
		}
	}
	if out.BufferSize <= 0 {
		out.BufferSize = envInt("MOCKWAVE_EVENT_BUFFER_SIZE", 10000)
	}
	if out.SyncInterval <= 0 {
		if v := envInt("MOCKWAVE_EVENT_SYNC_INTERVAL", 0); v > 0 {
			out.SyncInterval = time.Duration(v) * time.Second
		} else {
			out.SyncInterval = 30 * time.Second
		}
	}
	return out
}

func (c EventConfig) ttlSeconds() int { return int(c.TTL / time.Second) }

// eventSink picks the MatchedStore for event-capture persistence: explicit
// override, else the backend when it implements MatchedStore, else nil.
func eventSink(cfg EventConfig, backend store.DataStore) matched.Sink {
	if cfg.Store != nil {
		return cfg.Store
	}
	if ms, ok := backend.(store.MatchedStore); ok {
		return ms
	}
	return nil
}

// awsCaptures keeps only aws-* protocol captures (intercepted events). Event
// captures share the matched store with HTTP captures; the protocol prefix
// separates the two views on hydration.
func awsCaptures(items []matched.Request) []matched.Request {
	out := make([]matched.Request, 0, len(items))
	for _, r := range items {
		if strings.HasPrefix(r.Protocol, "aws-") {
			out = append(out, r)
		}
	}
	return out
}

// nonAWSCaptures keeps everything except aws-* (HTTP/GraphQL/SOAP/gRPC).
func nonAWSCaptures(items []matched.Request) []matched.Request {
	out := make([]matched.Request, 0, len(items))
	for _, r := range items {
		if !strings.HasPrefix(r.Protocol, "aws-") {
			out = append(out, r)
		}
	}
	return out
}

// eventQuery flattens the non-body event metadata into the matched.Request
// Query map so it is filterable/visible in the capture admin view.
func eventQuery(ev domain.Event) map[string]string {
	q := map[string]string{}
	if ev.Source != "" {
		q["source"] = ev.Source
	}
	if ev.DetailType != "" {
		q["detail_type"] = ev.DetailType
	}
	if ev.Subject != "" {
		q["subject"] = ev.Subject
	}
	if ev.GroupID != "" {
		q["group_id"] = ev.GroupID
	}
	if ev.DedupID != "" {
		q["dedup_id"] = ev.DedupID
	}
	for k, v := range ev.Attributes {
		q["attr."+k] = v
	}
	return q
}
