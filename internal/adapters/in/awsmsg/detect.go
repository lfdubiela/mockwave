// Package awsmsg intercepts AWS messaging publish calls (SNS/SQS/EventBridge)
// arriving on the mock port, normalizes them, and synthesizes valid responses.
package awsmsg

import (
	"net/http"
	"strings"

	"github.com/mockwave/mockwave/domain"
)

// DetectResult carries the AWS service and signer identity resolved from a
// request. Service is "" when the request is not an AWS messaging publish.
type DetectResult struct {
	Service  string // sns | sqs | eventbridge | ""
	Region   string
	Identity string // access key id from the SigV4 credential
}

// Detect inspects request headers for AWS SigV4 / X-Amz-Target markers.
func Detect(r *http.Request) DetectResult {
	res := parseAuthScope(r.Header.Get("Authorization"))
	switch target := r.Header.Get("X-Amz-Target"); {
	case strings.HasPrefix(target, "AmazonSQS."):
		res.Service = domain.EventServiceSQS
	case strings.HasPrefix(target, "AWSEvents."):
		res.Service = domain.EventServiceEventBridge
	}
	return res
}

// parseAuthScope reads "Credential=<akid>/<date>/<region>/<service>/aws4_request"
// from a SigV4 Authorization header and maps the AWS service token to our
// service constant.
func parseAuthScope(auth string) DetectResult {
	var res DetectResult
	const marker = "Credential="
	i := strings.Index(auth, marker)
	if i < 0 {
		return res
	}
	cred := auth[i+len(marker):]
	if c := strings.IndexByte(cred, ','); c >= 0 {
		cred = cred[:c]
	}
	parts := strings.Split(strings.TrimSpace(cred), "/")
	if len(parts) < 5 {
		return res
	}
	res.Identity = parts[0]
	res.Region = parts[2]
	switch parts[3] {
	case "sns":
		res.Service = domain.EventServiceSNS
	case "sqs":
		res.Service = domain.EventServiceSQS
	case "events":
		res.Service = domain.EventServiceEventBridge
	}
	return res
}
