package server

import (
	"os"
	"strconv"
	"time"

	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/store"
)

// MatchedConfig configures matched-request capture. Disabled by default; when
// off there is zero capture overhead. Explicit non-zero fields win; otherwise
// env vars fill in; otherwise built-in defaults apply.
type MatchedConfig struct {
	Enabled      bool
	TTL          time.Duration      // global expiry; default 1h
	BufferSize   int                // in-memory capacity; default 10000
	SyncInterval time.Duration      // write-behind cadence; default 30s
	Store        store.MatchedStore // BYO override; nil → derived from backend
}

func resolveMatchedConfig(in MatchedConfig) MatchedConfig {
	out := in
	if !out.Enabled && os.Getenv("MOCKWAVE_MATCHED_CAPTURE") == "true" {
		out.Enabled = true
	}
	if out.TTL <= 0 {
		if v := envInt("MOCKWAVE_MATCHED_TTL", 0); v > 0 {
			out.TTL = time.Duration(v) * time.Second
		} else {
			out.TTL = time.Hour
		}
	}
	if out.BufferSize <= 0 {
		out.BufferSize = envInt("MOCKWAVE_MATCHED_BUFFER_SIZE", 10000)
	}
	if out.SyncInterval <= 0 {
		if v := envInt("MOCKWAVE_MATCHED_SYNC_INTERVAL", 0); v > 0 {
			out.SyncInterval = time.Duration(v) * time.Second
		} else {
			out.SyncInterval = 30 * time.Second
		}
	}
	return out
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// ttlSeconds returns the TTL as whole seconds for capture hints.
func (c MatchedConfig) ttlSeconds() int {
	return int(c.TTL / time.Second)
}

// matchedSink picks the MatchedStore: explicit override, else the backend if it
// implements store.MatchedStore, else nil (memory-only).
func matchedSink(cfg MatchedConfig, backend store.DataStore) matched.Sink {
	if cfg.Store != nil {
		return cfg.Store
	}
	if ms, ok := backend.(store.MatchedStore); ok {
		return ms
	}
	return nil
}
