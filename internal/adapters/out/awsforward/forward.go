package awsforward

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/mockwave/mockwave/domain"
)

// Forwarder re-emits events to a real broker via aws-sdk-go-v2, signing with
// the credentials resolved from each rule's EventForward. Stateless: a config
// is resolved and a client built per call.
type Forwarder struct{}

// New returns a Forwarder.
func New() *Forwarder { return &Forwarder{} }

// Forward re-emits ev to the broker described by fwd and returns the broker's
// real message/event id.
func (f *Forwarder) Forward(ctx context.Context, ev domain.Event, fwd domain.EventForward) (string, error) {
	cfg, err := resolveConfig(ctx, fwd)
	if err != nil {
		return "", err
	}
	switch ev.Service {
	case domain.EventServiceSNS:
		client := sns.NewFromConfig(cfg, func(o *sns.Options) {
			if fwd.Endpoint != "" {
				o.BaseEndpoint = aws.String(fwd.Endpoint)
			}
		})
		out, err := client.Publish(ctx, &sns.PublishInput{
			TopicArn:               aws.String(ev.Target),
			Message:                aws.String(string(ev.Message)),
			Subject:                optStr(ev.Subject),
			MessageGroupId:         optStr(ev.GroupID),
			MessageDeduplicationId: optStr(ev.DedupID),
			MessageAttributes:      snsAttrs(ev.Attributes),
		})
		if err != nil {
			return "", fmt.Errorf("awsforward: sns publish: %w", err)
		}
		return aws.ToString(out.MessageId), nil

	case domain.EventServiceSQS:
		client := sqs.NewFromConfig(cfg, func(o *sqs.Options) {
			if fwd.Endpoint != "" {
				o.BaseEndpoint = aws.String(fwd.Endpoint)
			}
		})
		out, err := client.SendMessage(ctx, &sqs.SendMessageInput{
			QueueUrl:               aws.String(ev.Target),
			MessageBody:            aws.String(string(ev.Message)),
			MessageGroupId:         optStr(ev.GroupID),
			MessageDeduplicationId: optStr(ev.DedupID),
			MessageAttributes:      sqsAttrs(ev.Attributes),
		})
		if err != nil {
			return "", fmt.Errorf("awsforward: sqs send: %w", err)
		}
		return aws.ToString(out.MessageId), nil

	case domain.EventServiceEventBridge:
		client := eventbridge.NewFromConfig(cfg, func(o *eventbridge.Options) {
			if fwd.Endpoint != "" {
				o.BaseEndpoint = aws.String(fwd.Endpoint)
			}
		})
		out, err := client.PutEvents(ctx, &eventbridge.PutEventsInput{
			Entries: []ebtypes.PutEventsRequestEntry{{
				Source:       optStr(ev.Source),
				DetailType:   optStr(ev.DetailType),
				Detail:       aws.String(string(ev.Message)),
				EventBusName: optStr(ev.Target),
			}},
		})
		if err != nil {
			return "", fmt.Errorf("awsforward: eventbridge putevents: %w", err)
		}
		if len(out.Entries) == 0 {
			return "", fmt.Errorf("awsforward: PutEvents returned no entries")
		}
		e := out.Entries[0]
		if e.EventId == nil {
			return "", fmt.Errorf("awsforward: PutEvents entry failed: %s", aws.ToString(e.ErrorMessage))
		}
		return aws.ToString(e.EventId), nil

	default:
		return "", fmt.Errorf("awsforward: unsupported service %q", ev.Service)
	}
}

// optStr returns a *string for non-empty s, else nil (so the SDK omits the field).
func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// snsAttrs/sqsAttrs convert the normalized name→value map into SDK attribute
// maps. DataType is "String" only — the normalized Event lost the original data
// type (Number/Binary forwarding is a roadmap item).
func snsAttrs(m map[string]string) map[string]snstypes.MessageAttributeValue {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]snstypes.MessageAttributeValue, len(m))
	for k, v := range m {
		out[k] = snstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}
	return out
}

func sqsAttrs(m map[string]string) map[string]sqstypes.MessageAttributeValue {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]sqstypes.MessageAttributeValue, len(m))
	for k, v := range m {
		out[k] = sqstypes.MessageAttributeValue{DataType: aws.String("String"), StringValue: aws.String(v)}
	}
	return out
}
