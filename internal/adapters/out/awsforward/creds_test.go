package awsforward

import (
	"context"
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func TestResolveConfigStaticFromEnv(t *testing.T) {
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_KEY", "AKIDTEST")
	t.Setenv("MOCKWAVE_AWS_STATIC_TEST_SECRET", "secret")
	cfg, err := resolveConfig(context.Background(), domain.EventForward{Region: "us-east-1", Credential: "static:test"})
	if err != nil {
		t.Fatal(err)
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID != "AKIDTEST" || creds.SecretAccessKey != "secret" {
		t.Fatalf("creds = %+v", creds)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("region = %q", cfg.Region)
	}
}

func TestResolveConfigStaticMissingEnv(t *testing.T) {
	if _, err := resolveConfig(context.Background(), domain.EventForward{Credential: "static:absent"}); err == nil {
		t.Fatal("expected error when static creds env is missing")
	}
}

func TestResolveConfigUnknownCredential(t *testing.T) {
	if _, err := resolveConfig(context.Background(), domain.EventForward{Credential: "vault:foo"}); err == nil {
		t.Fatal("expected error for unknown credential ref")
	}
}

func TestResolveConfigDefaultRegionFallback(t *testing.T) {
	// Empty region defaults to us-east-1; default credential chain is lazy
	// (no error at resolve time even without real AWS creds).
	cfg, err := resolveConfig(context.Background(), domain.EventForward{Credential: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Region != "us-east-1" {
		t.Fatalf("region = %q, want us-east-1", cfg.Region)
	}
}
