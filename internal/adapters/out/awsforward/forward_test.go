package awsforward

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func staticEnv(t *testing.T) {
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_KEY", "AKIDTEST")
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_SECRET", "secret")
}

func TestForwardSNS(t *testing.T) {
	staticEnv(t)
	var gotAuth string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/"><PublishResult><MessageId>real-sns-1</MessageId></PublishResult><ResponseMetadata><RequestId>r</RequestId></ResponseMetadata></PublishResponse>`)
	}))
	defer stub.Close()

	f := New()
	id, err := f.Forward(context.Background(),
		domain.Event{Service: domain.EventServiceSNS, Operation: "Publish", Target: "arn:aws:sns:us-east-1:1:orders", Message: []byte(`{"id":1}`)},
		domain.EventForward{Endpoint: stub.URL, Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "real-sns-1" {
		t.Fatalf("id = %q", id)
	}
	if !strings.Contains(gotAuth, "AKIDTEST") {
		t.Fatalf("upstream not re-signed with mockwave key: %q", gotAuth)
	}
}

func TestForwardSQS(t *testing.T) {
	staticEnv(t)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in struct{ MessageBody string }
		_ = json.Unmarshal(body, &in)
		sum := md5.Sum([]byte(in.MessageBody))
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		fmt.Fprintf(w, `{"MessageId":"real-sqs-1","MD5OfMessageBody":"%s"}`, hex.EncodeToString(sum[:]))
	}))
	defer stub.Close()

	f := New()
	id, err := f.Forward(context.Background(),
		domain.Event{Service: domain.EventServiceSQS, Operation: "SendMessage", Target: "https://sqs/q/orders", Message: []byte("hello")},
		domain.EventForward{Endpoint: stub.URL, Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "real-sqs-1" {
		t.Fatalf("id = %q", id)
	}
}

func TestForwardEventBridge(t *testing.T) {
	staticEnv(t)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		fmt.Fprint(w, `{"FailedEntryCount":0,"Entries":[{"EventId":"real-eb-1"}]}`)
	}))
	defer stub.Close()

	f := New()
	id, err := f.Forward(context.Background(),
		domain.Event{Service: domain.EventServiceEventBridge, Operation: "PutEvents", Target: "default", Source: "orders", DetailType: "Created", Message: []byte(`{"id":1}`)},
		domain.EventForward{Endpoint: stub.URL, Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	if id != "real-eb-1" {
		t.Fatalf("id = %q", id)
	}
}

func TestForwardUnsupportedService(t *testing.T) {
	staticEnv(t)
	f := New()
	if _, err := f.Forward(context.Background(), domain.Event{Service: "kinesis"}, domain.EventForward{Region: "us-east-1", Credential: "static:test"}); err == nil {
		t.Fatal("expected error for unsupported service")
	}
}
