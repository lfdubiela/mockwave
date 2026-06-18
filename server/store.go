package server

import (
	"fmt"
	"os"
	"time"

	cosmos "github.com/mockwave/mockwave/internal/adapters/out/cosmos"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	mongodb "github.com/mockwave/mockwave/internal/adapters/out/mongodb"
	"github.com/mockwave/mockwave/store"
)

// reloadIntervalFromEnv returns the parsed MOCKWAVE_RELOAD_INTERVAL, or 0 when
// unset/invalid (0 lets Config.reloadInterval fall back to the 15s default).
func reloadIntervalFromEnv() time.Duration {
	if v := os.Getenv("MOCKWAVE_RELOAD_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 0
}

// buildStoreFromEnv constructs a DataStore from environment variables.
// MOCKWAVE_STORE selects the backend (default: "json").
// Returns an error if required variables are missing or construction fails.
func buildStoreFromEnv() (store.DataStore, error) {
	backend := envOr("MOCKWAVE_STORE", "json")
	switch backend {
	case "json":
		path := os.Getenv("MOCKWAVE_CONFIG")
		if path == "" {
			return nil, fmt.Errorf("server: MOCKWAVE_CONFIG is required when MOCKWAVE_STORE=json")
		}
		return jsonfile.NewStore(path)
	case "dynamodb":
		return dynamostore.NewStore(dynamostore.Config{
			RulesTable:      envOr("MOCKWAVE_DYNAMO_RULES_TABLE", "mockwave-rules"),
			SimsTable:       envOr("MOCKWAVE_DYNAMO_SIMS_TABLE", "mockwave-simulations"),
			FaultsTable:     envOr("MOCKWAVE_DYNAMO_FAULTS_TABLE", "mockwave-fault-profiles"),
			ScenariosTable:  envOr("MOCKWAVE_DYNAMO_SCENARIOS_TABLE", "mockwave-scenarios"),
			MatchedTable:    envOr("MOCKWAVE_DYNAMO_MATCHED_TABLE", "mockwave-matched-requests"),
			EventRulesTable: envOr("MOCKWAVE_DYNAMO_EVENT_RULES_TABLE", "mockwave-event-rules"),
			Region:          envOr("MOCKWAVE_DYNAMO_REGION", "us-east-1"),
			Endpoint:        os.Getenv("MOCKWAVE_DYNAMO_ENDPOINT"),
		})
	case "mongo":
		return mongodb.NewStore(
			envOr("MOCKWAVE_MONGO_URI", "mongodb://localhost:27017"),
			envOr("MOCKWAVE_MONGO_DB", "mockwave"),
		)
	case "cosmos":
		uri := os.Getenv("MOCKWAVE_COSMOS_URI")
		if uri == "" {
			return nil, fmt.Errorf("server: MOCKWAVE_COSMOS_URI is required when MOCKWAVE_STORE=cosmos")
		}
		return cosmos.NewStore(uri, envOr("MOCKWAVE_COSMOS_DB", "mockwave"))
	default:
		return nil, fmt.Errorf("server: unknown MOCKWAVE_STORE %q — valid: json, dynamodb, mongo, cosmos", backend)
	}
}

// envOr returns the value of the named environment variable,
// or fallback if the variable is unset or empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
