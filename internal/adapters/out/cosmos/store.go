// Package cosmos provides a DataStore backed by Azure Cosmos DB using the
// MongoDB wire protocol. Cosmos DB requires TLS (ssl=true) and retryWrites=false
// for compatibility with the MongoDB driver; EnsureCosmosParams applies these
// automatically to any connection URI.
package cosmos

import (
	"strings"

	"github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	gomongo "go.mongodb.org/mongo-driver/mongo"
)

// NewStore returns a DataStore backed by Azure Cosmos DB (MongoDB API).
// uri must be a Cosmos DB connection string; ssl=true and retryWrites=false
// are appended automatically if not already present.
func NewStore(uri, dbName string) (*mongodb.Store, error) {
	return mongodb.NewStore(EnsureCosmosParams(uri), dbName)
}

// NewStoreFromClient creates a Cosmos store from an existing mongo.Client.
// Use in tests to inject the mtest mock client.
func NewStoreFromClient(client *gomongo.Client, dbName string) *mongodb.Store {
	return mongodb.NewStoreFromClient(client, dbName)
}

// EnsureCosmosParams appends the required Cosmos DB query parameters to uri
// if they are not already present. Exported for testing.
func EnsureCosmosParams(uri string) string {
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	if !strings.Contains(uri, "ssl=true") {
		uri += sep + "ssl=true"
		sep = "&"
	}
	if !strings.Contains(uri, "retryWrites=false") {
		uri += sep + "retryWrites=false"
	}
	return uri
}
