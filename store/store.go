// Package store defines the DataStore interface that all mockwave storage
// backends must implement. Import this package to build a custom adapter.
package store

import (
	"context"

	"github.com/mockwave/mockwave/domain"
	"github.com/mockwave/mockwave/internal/matched"
)

// DataStore persists and retrieves rules and simulations.
// Implement this interface to plug in a custom storage backend.
//
// Contract for GetSimulation: returns (nil, nil) when the simulation does not
// exist — this is NOT an error. Callers must check for a nil pointer to detect
// not-found before dereferencing the result.
type DataStore interface {
	GetRules() ([]domain.Rule, error)

	// GetSimulation returns the simulation with the given id, or (nil, nil) if
	// no simulation with that id exists. A non-nil error indicates a storage
	// failure, not a missing record.
	GetSimulation(id string) (*domain.Simulation, error)

	// ListSimulations returns all simulations in the store.
	ListSimulations() ([]domain.Simulation, error)

	SaveRule(r domain.Rule) error
	SaveSimulation(s domain.Simulation) error
	DeleteRule(id string) error
	DeleteSimulation(id string) error
}

// FaultStore is an optional capability for stores that persist chaos fault
// profiles. Same contract style as DataStore: GetFaultProfile returns
// (nil, nil) when the profile does not exist.
type FaultStore interface {
	ListFaultProfiles() ([]domain.FaultProfile, error)
	GetFaultProfile(id string) (*domain.FaultProfile, error)
	SaveFaultProfile(p domain.FaultProfile) error
	DeleteFaultProfile(id string) error
}

// ScenarioStore is an optional capability for stores that persist chaos
// scenarios. GetScenario returns (nil, nil) when the scenario does not exist.
type ScenarioStore interface {
	ListScenarios() ([]domain.Scenario, error)
	GetScenario(id string) (*domain.Scenario, error)
	SaveScenario(s domain.Scenario) error
	DeleteScenario(id string) error
}

// MatchedQuery and MatchedPage are re-exported from the matched package so
// embedders implementing MatchedStore need not import an internal package for
// these value types.
type MatchedQuery = matched.Query
type MatchedPage = matched.Page

// MatchedStore is an optional capability for stores that persist matched
// requests (write-behind durability for the in-memory capture buffer).
// Implementations apply native TTL where available; SweepExpired is a no-op
// for those and an explicit delete for stores without native TTL (JSON).
type MatchedStore interface {
	// SaveMatched upserts a batch of requests and their out-of-line bodies.
	SaveMatched(ctx context.Context, reqs []matched.Request, reqBodies []matched.RequestBody, respBodies []matched.ResponseBody) error
	// ListMatched returns a filtered, paginated page for a rule, newest first.
	ListMatched(ctx context.Context, ruleID string, q MatchedQuery) (MatchedPage, error)
	// GetMatched returns the full request (with bodies), or (nil, nil) when absent.
	GetMatched(ctx context.Context, ruleID, id string) (*matched.FullRequest, error)
	// DeleteMatched removes a rule's captures, or all when ruleID == "".
	DeleteMatched(ctx context.Context, ruleID string) error
	// SweepExpired deletes entries whose TTL epoch-seconds is < before.
	// Native-TTL stores return (0, nil).
	SweepExpired(ctx context.Context, before int64) (int, error)
}

// VersionedStore is an optional capability. Stores that can report a monotonic
// config version enable the periodic version-poll reloader: the reloader reads
// ConfigVersion cheaply each tick and triggers a full in-memory reload only when
// the value changed. Stores that do not implement it (e.g. the JSON store) are
// never polled.
type VersionedStore interface {
	// ConfigVersion returns a monotonically increasing marker that changes on
	// every rule/simulation write. Returns 0 when no marker exists yet.
	ConfigVersion() (int64, error)
}
