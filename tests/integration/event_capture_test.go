package integration

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
	"github.com/mockwave/mockwave/internal/adapters/out/jsonfile"
	"github.com/mockwave/mockwave/internal/matched"
	"github.com/mockwave/mockwave/server"
)

func TestE2E_SNSEventCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "orders",
			Name:  "Order events",
			Match: domain.EventMatch{Service: domain.EventServiceSNS, Target: "arn:aws:sns:*:*:orders"},
		}},
	})
	srv, err := server.New(server.Config{
		Store: st,
		Event: server.EventConfig{Enabled: true, BufferSize: 100, SyncInterval: time.Hour},
	})
	require.NoError(t, err)
	defer srv.Close()

	mock := httptest.NewServer(srv.MockHandler([]string{"http", "aws"}, srv.NewProxy()))
	defer mock.Close()
	admin := httptest.NewServer(srv.AdminMux())
	defer admin.Close()

	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "")),
	)
	require.NoError(t, err)
	client := sns.NewFromConfig(cfg, func(o *sns.Options) {
		o.BaseEndpoint = aws.String(mock.URL)
	})

	out, err := client.Publish(context.Background(), &sns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:123456789012:orders"),
		Message:  aws.String(`{"type":"created","total":42}`),
	})
	require.NoError(t, err, "SDK must accept the synthesized response")
	require.NotNil(t, out.MessageId)
	assert.NotEmpty(t, *out.MessageId)

	resp, err := http.Get(admin.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)

	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	item := page.Items[0]
	assert.Equal(t, "aws-sns", item.Protocol)
	assert.Equal(t, "Publish", item.Method)
	assert.Equal(t, "arn:aws:sns:us-east-1:123456789012:orders", item.Path)
	assert.Equal(t, "AKIDEXAMPLE", item.Identity)

	detResp, err := http.Get(admin.URL + "/api/event-captures/orders/" + item.ID)
	require.NoError(t, err)
	defer detResp.Body.Close()
	require.Equal(t, 200, detResp.StatusCode)
	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(detResp.Body).Decode(&full))
	assert.JSONEq(t, `{"type":"created","total":42}`, string(full.RequestBody))
}
