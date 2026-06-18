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
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
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

func TestE2E_SQSEventCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "sqs-orders",
			Match: domain.EventMatch{Service: domain.EventServiceSQS},
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
	client := sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	out, err := client.SendMessage(context.Background(), &sqs.SendMessageInput{
		QueueUrl:    aws.String(mock.URL + "/000000000000/orders"),
		MessageBody: aws.String(`{"type":"created"}`),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"env": {DataType: aws.String("String"), StringValue: aws.String("prod")},
		},
	})
	require.NoError(t, err, "SDK must accept the response (validates MD5OfMessageBody)")
	require.NotNil(t, out.MessageId)

	resp, err := http.Get(admin.URL + "/api/event-captures/sqs-orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	assert.Equal(t, "aws-sqs", page.Items[0].Protocol)
	assert.Equal(t, "SendMessage", page.Items[0].Method)
	assert.Equal(t, "AKIDEXAMPLE", page.Items[0].Identity)
}

func TestE2E_EventBridgeEventCapture(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "eb-orders",
			Match: domain.EventMatch{Service: domain.EventServiceEventBridge, Source: "orders.service"},
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
	client := eventbridge.NewFromConfig(cfg, func(o *eventbridge.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	out, err := client.PutEvents(context.Background(), &eventbridge.PutEventsInput{
		Entries: []ebtypes.PutEventsRequestEntry{
			{Source: aws.String("orders.service"), DetailType: aws.String("OrderCreated"), Detail: aws.String(`{"id":1}`)},
			{Source: aws.String("orders.service"), DetailType: aws.String("OrderShipped"), Detail: aws.String(`{"id":2}`)},
		},
	})
	require.NoError(t, err)
	require.Equal(t, int32(0), out.FailedEntryCount)
	require.Len(t, out.Entries, 2)

	resp, err := http.Get(admin.URL + "/api/event-captures/eb-orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 2)
	assert.Equal(t, "aws-eventbridge", page.Items[0].Protocol)
	assert.Equal(t, "PutEvents", page.Items[0].Method)
}

func TestE2E_SNSEventCaptureBodyFilter(t *testing.T) {
	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "orders",
			Match: domain.EventMatch{Service: domain.EventServiceSNS},
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
	client := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	for _, cid := range []string{"abc-123", "zzz-999"} {
		_, err := client.Publish(context.Background(), &sns.PublishInput{
			TopicArn: aws.String("arn:aws:sns:us-east-1:1:orders"),
			Message:  aws.String(`{"correlation_id":"` + cid + `","total":42}`),
		})
		require.NoError(t, err)
	}

	// Body filter returns exactly the abc-123 event.
	resp, err := http.Get(admin.URL + "/api/event-captures/orders?body=$.correlation_id:abc-123")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)

	det, err := http.Get(admin.URL + "/api/event-captures/orders/" + page.Items[0].ID)
	require.NoError(t, err)
	defer det.Body.Close()
	var full matched.FullRequest
	require.NoError(t, json.NewDecoder(det.Body).Decode(&full))
	assert.JSONEq(t, `{"correlation_id":"abc-123","total":42}`, string(full.RequestBody))

	// Unfiltered list returns both.
	all, err := http.Get(admin.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer all.Body.Close()
	var allPage matched.Page
	require.NoError(t, json.NewDecoder(all.Body).Decode(&allPage))
	require.Len(t, allPage.Items, 2)
}
