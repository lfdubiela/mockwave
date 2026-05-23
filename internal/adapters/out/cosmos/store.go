// Package cosmos provides a DataStore backed by Azure Cosmos DB using the
// MongoDB wire protocol. Cosmos DB requires TLS (ssl=true) and retryWrites=false
// for compatibility with the MongoDB driver; EnsureCosmosParams applies these
// automatically to any connection URI.
package cosmos

import (
	"net/url"

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
// if they are not already present. Uses proper URL query-parameter parsing to
// avoid false positives from substring matches. Exported for testing.
func EnsureCosmosParams(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		// Unparseable URI — fall back to appending blindly so callers still get
		// a useful error from the MongoDB driver rather than a silent wrong URI.
		return uri + "?ssl=true&retryWrites=false"
	}
	q := u.Query()
	if q.Get("ssl") == "" {
		q.Set("ssl", "true")
	}
	if q.Get("retryWrites") == "" {
		q.Set("retryWrites", "false")
	}
	u.RawQuery = q.Encode()
	return u.String()
}
