package cosmos

import (
	"github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	"github.com/mockwave/mockwave/store"
)

// compile-time assertion: cosmos's Store (mongodb.Store) satisfies EventRuleStore.
var _ store.EventRuleStore = (*mongodb.Store)(nil)
