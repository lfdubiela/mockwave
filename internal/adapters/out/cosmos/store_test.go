package cosmos_test

import (
	"strings"
	"testing"

	"github.com/mockwave/mockwave/internal/adapters/out/cosmos"
	"github.com/stretchr/testify/assert"
)

func TestEnsureCosmosParams_AddsSSLAndRetryWrites(t *testing.T) {
	uri := "mongodb://account.mongo.cosmos.azure.com:10255/db"
	got := cosmos.EnsureCosmosParams(uri)
	assert.Contains(t, got, "ssl=true")
	assert.Contains(t, got, "retryWrites=false")
}

func TestEnsureCosmosParams_NoDoubleSSL(t *testing.T) {
	uri := "mongodb://host:10255/db?ssl=true"
	got := cosmos.EnsureCosmosParams(uri)
	assert.Equal(t, 1, strings.Count(got, "ssl=true"))
}

func TestEnsureCosmosParams_NoDoubleRetryWrites(t *testing.T) {
	uri := "mongodb://host:10255/db?ssl=true&retryWrites=false"
	got := cosmos.EnsureCosmosParams(uri)
	assert.Equal(t, 1, strings.Count(got, "retryWrites=false"))
	assert.Equal(t, 1, strings.Count(got, "ssl=true"))
}

func TestEnsureCosmosParams_PreservesExistingParams(t *testing.T) {
	uri := "mongodb://host:10255/db?authMechanism=SCRAM-SHA-256"
	got := cosmos.EnsureCosmosParams(uri)
	assert.Contains(t, got, "authMechanism=SCRAM-SHA-256")
	assert.Contains(t, got, "ssl=true")
	assert.Contains(t, got, "retryWrites=false")
}
