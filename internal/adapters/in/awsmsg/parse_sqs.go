package awsmsg

import (
	"encoding/json"
	"fmt"

	"github.com/mockwave/mockwave/domain"
)

type sqsSendMessage struct {
	QueueUrl               string                       `json:"QueueUrl"`
	MessageBody            string                       `json:"MessageBody"`
	MessageGroupId         string                       `json:"MessageGroupId"`
	MessageDeduplicationId string                       `json:"MessageDeduplicationId"`
	MessageAttributes      map[string]sqsAttributeValue `json:"MessageAttributes"`
}

type sqsAttributeValue struct {
	DataType    string `json:"DataType"`
	StringValue string `json:"StringValue"`
	BinaryValue []byte `json:"BinaryValue"` // JSON base64 → bytes
}

// parseSQS converts an SQS SendMessage JSON body into a normalized Event plus
// the raw message attributes the responder needs to compute MD5s.
func parseSQS(body []byte) (domain.Event, []MsgAttr, error) {
	var in sqsSendMessage
	if err := json.Unmarshal(body, &in); err != nil {
		return domain.Event{}, nil, fmt.Errorf("awsmsg: SQS body: %w", err)
	}
	if in.QueueUrl == "" {
		return domain.Event{}, nil, fmt.Errorf("awsmsg: SQS request missing QueueUrl")
	}
	var attrs []MsgAttr
	var flat map[string]string
	for name, v := range in.MessageAttributes {
		attrs = append(attrs, MsgAttr{Name: name, DataType: v.DataType, StringValue: v.StringValue, BinaryValue: v.BinaryValue})
		if flat == nil {
			flat = map[string]string{}
		}
		flat[name] = v.StringValue
	}
	ev := domain.Event{
		Service:    domain.EventServiceSQS,
		Operation:  "SendMessage",
		Target:     in.QueueUrl,
		Message:    []byte(in.MessageBody),
		GroupID:    in.MessageGroupId,
		DedupID:    in.MessageDeduplicationId,
		Attributes: flat,
	}
	return ev, attrs, nil
}
