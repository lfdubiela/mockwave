//go:build integration

package dynamostore_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mockwave/mockwave/domain"
	dynamostore "github.com/mockwave/mockwave/internal/adapters/out/dynamodb"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
)

func TestE2E_Dynamo_EventCaptureAndRules(t *testing.T) {
	endpoint := dynamoEndpoint(t) // skips when DYNAMO_TEST_ENDPOINT unset
	client := newLocalClient(t, endpoint)

	const (
		rulesTable      = "e2e-ev-rules"
		simsTable       = "e2e-ev-sims"
		faultsTable     = "e2e-ev-faults"
		scenariosTable  = "e2e-ev-scenarios"
		matchedTable    = "e2e-ev-matched"
		eventRulesTable = "e2e-ev-event-rules"
	)
	createTable(t, client, rulesTable)
	createTable(t, client, simsTable)
	createTable(t, client, faultsTable)
	createTable(t, client, scenariosTable)
	createTable(t, client, eventRulesTable)
	createMatchedTable(t, client, matchedTable)

	dynStore := dynamostore.NewStoreFromClient(client, dynamostore.Config{
		RulesTable:      rulesTable,
		SimsTable:       simsTable,
		FaultsTable:     faultsTable,
		ScenariosTable:  scenariosTable,
		MatchedTable:    matchedTable,
		EventRulesTable: eventRulesTable,
	})

	// Event rule persisted in Dynamo (proves EventRuleStore round-trips).
	require.NoError(t, dynStore.SaveEventRule(domain.EventRule{
		ID:    "orders",
		Match: domain.EventMatch{Service: domain.EventServiceSNS},
	}))
	got, err := dynStore.GetEventRules()
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "orders", got[0].ID)

	const syncInterval = 50 * time.Millisecond
	srv, err := server.New(server.Config{
		Store: dynStore,
		Event: server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: syncInterval},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Close() })

	mock := httptest.NewServer(srv.MockHandler([]string{"http", "aws"}, srv.NewProxy()))
	t.Cleanup(mock.Close)

	// Real SNS SDK publishes through Mockwave (the rule was loaded from Dynamo).
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	snsClient := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(mock.URL) })
	_, err = snsClient.Publish(context.Background(), &sns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:1:orders"),
		Message:  aws.String(`{"id":1}`),
	})
	require.NoError(t, err)

	// The capture is durably written to Dynamo within a couple of sync ticks.
	ctx := context.Background()
	assert.Eventually(t, func() bool {
		page, err := dynStore.ListMatched(ctx, "orders", matched.Query{})
		return err == nil && len(page.Items) == 1 && page.Items[0].Protocol == "aws-sns"
	}, 2*time.Second, 20*time.Millisecond, "event capture must persist to DynamoDB")

	// Restart: a second server hydrates the event capture from Dynamo.
	srv2, err := server.New(server.Config{
		Store: dynStore,
		Event: server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: syncInterval},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv2.Close() })

	admin2 := httptest.NewServer(srv2.AdminMux())
	t.Cleanup(admin2.Close)

	resp, err := http.Get(admin2.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	assert.GreaterOrEqual(t, len(page.Items), 1, "second server must hydrate event captures from DynamoDB")
}
