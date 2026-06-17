package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestE2E_SNSForward(t *testing.T) {
	// Mockwave's static forward credential (resolved by awsforward from env).
	t.Setenv("MOCKWAVE_AWS_STATIC_FWD_KEY", "AKIDMOCKWAVE")
	t.Setenv("MOCKWAVE_AWS_STATIC_FWD_SECRET", "secret")

	// Stub "real SNS" upstream: records the signer, returns a real MessageId.
	var upstreamAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/"><PublishResult><MessageId>upstream-msg-42</MessageId></PublishResult><ResponseMetadata><RequestId>r</RequestId></ResponseMetadata></PublishResponse>`))
	}))
	defer upstream.Close()

	st := jsonfile.NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{
			ID:    "orders",
			Match: domain.EventMatch{Service: domain.EventServiceSNS},
			Forward: &domain.EventForward{
				Endpoint:   upstream.URL,
				Region:     "us-east-1",
				Credential: "static:fwd",
			},
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

	// App publishes to Mockwave with ITS OWN creds.
	cfg, err := awscfg.LoadDefaultConfig(context.Background(),
		awscfg.WithRegion("us-east-1"),
		awscfg.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIDAPP", "appsecret", "")),
	)
	require.NoError(t, err)
	client := sns.NewFromConfig(cfg, func(o *sns.Options) { o.BaseEndpoint = aws.String(mock.URL) })

	out, err := client.Publish(context.Background(), &sns.PublishInput{
		TopicArn: aws.String("arn:aws:sns:us-east-1:1:orders"),
		Message:  aws.String(`{"id":1}`),
	})
	require.NoError(t, err)
	// The app receives the UPSTREAM's real id, relayed by Mockwave.
	require.Equal(t, "upstream-msg-42", aws.ToString(out.MessageId))

	// The upstream was re-signed with Mockwave's key, NOT the app's.
	require.Contains(t, upstreamAuth, "AKIDMOCKWAVE")
	require.NotContains(t, upstreamAuth, "AKIDAPP")

	// The capture records the forward.
	resp, err := http.Get(admin.URL + "/api/event-captures/orders")
	require.NoError(t, err)
	defer resp.Body.Close()
	var page matched.Page
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	require.Len(t, page.Items, 1)
	assert.True(t, page.Items[0].Forwarded)
	assert.True(t, strings.HasPrefix(page.Items[0].ForwardTarget, upstream.URL) || page.Items[0].ForwardTarget == upstream.URL)
	assert.Equal(t, 200, page.Items[0].ResponseStatus)
}
