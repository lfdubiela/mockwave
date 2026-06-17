package server

import (
	"os"
	"time"

	"github.com/mockwave/mockwave/domain"
)

// EventConfig configures AWS event interception + capture. Disabled by default.
// Explicit non-zero fields win; otherwise env vars fill in; otherwise defaults.
type EventConfig struct {
	Enabled      bool
	TTL          time.Duration // capture expiry; default 1h
	BufferSize   int           // in-memory capacity; default 10000
	SyncInterval time.Duration // write-behind cadence; default 30s
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
