// Package awsforward re-emits intercepted events to a real AWS broker, signed
// with Mockwave's own credentials (the app's SigV4 signature cannot be reused —
// it is bound to the original host and the secret key never travels).
package awsforward

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"

	"github.com/mockwave/mockwave/domain"
)

// resolveConfig builds an aws.Config for a forward target. Region defaults to
// us-east-1 when unset. Credential resolution:
//
//	""/"default"   → SDK default chain
//	"profile:<n>"  → shared config profile <n>
//	"static:<n>"   → env MOCKWAVE_AWS_STATIC_<N>_KEY/_SECRET/_TOKEN
func resolveConfig(ctx context.Context, fwd domain.EventForward) (aws.Config, error) {
	region := fwd.Region
	if region == "" {
		region = "us-east-1"
	}
	opts := []func(*config.LoadOptions) error{config.WithRegion(region)}

	switch {
	case fwd.Credential == "" || fwd.Credential == "default":
		// default credential chain
	case strings.HasPrefix(fwd.Credential, "profile:"):
		opts = append(opts, config.WithSharedConfigProfile(strings.TrimPrefix(fwd.Credential, "profile:")))
	case strings.HasPrefix(fwd.Credential, "static:"):
		cp, err := staticCredsFromEnv(strings.TrimPrefix(fwd.Credential, "static:"))
		if err != nil {
			return aws.Config{}, err
		}
		opts = append(opts, config.WithCredentialsProvider(cp))
	default:
		return aws.Config{}, fmt.Errorf("awsforward: unknown credential ref %q", fwd.Credential)
	}
	return config.LoadDefaultConfig(ctx, opts...)
}

func staticCredsFromEnv(name string) (aws.CredentialsProvider, error) {
	up := strings.ToUpper(name)
	key := os.Getenv("MOCKWAVE_AWS_STATIC_" + up + "_KEY")
	secret := os.Getenv("MOCKWAVE_AWS_STATIC_" + up + "_SECRET")
	if key == "" || secret == "" {
		return nil, fmt.Errorf("awsforward: static credential %q requires MOCKWAVE_AWS_STATIC_%s_KEY and _SECRET", name, up)
	}
	token := os.Getenv("MOCKWAVE_AWS_STATIC_" + up + "_TOKEN")
	return credentials.NewStaticCredentialsProvider(key, secret, token), nil
}
