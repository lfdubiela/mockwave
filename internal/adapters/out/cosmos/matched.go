// Package cosmos provides a DataStore backed by Azure Cosmos DB using the
// MongoDB wire protocol. The MatchedStore methods are implemented on
// mongodb.Store, which cosmos.NewStore returns directly. No additional code is
// needed here — this file exists only to document the delegation and hold the
// compile-time assertion.
package cosmos

import (
	"github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	"github.com/mockwave/mockwave/store"
)

// compile-time assertion: cosmos's Store (mongodb.Store) satisfies MatchedStore.
var _ store.MatchedStore = (*mongodb.Store)(nil)
