package awsmsg

import (
	"fmt"
	"net/url"

	"github.com/mockwave/mockwave/domain"
)

// parseSNS converts an SNS Publish form body into a normalized Event.
func parseSNS(form url.Values) (domain.Event, error) {
	ev := domain.Event{
		Service:    domain.EventServiceSNS,
		Operation:  form.Get("Action"),
		Target:     firstNonEmpty(form.Get("TopicArn"), form.Get("TargetArn"), form.Get("PhoneNumber")),
		Subject:    form.Get("Subject"),
		Message:    []byte(form.Get("Message")),
		GroupID:    form.Get("MessageGroupId"),
		DedupID:    form.Get("MessageDeduplicationId"),
		Attributes: parseSNSAttributes(form),
	}
	if ev.Operation == "" {
		return domain.Event{}, fmt.Errorf("awsmsg: SNS request missing Action")
	}
	return ev, nil
}

// parseSNSAttributes reads MessageAttributes.entry.N.{Name,Value.StringValue}.
func parseSNSAttributes(form url.Values) map[string]string {
	var out map[string]string
	for i := 1; ; i++ {
		name := form.Get(fmt.Sprintf("MessageAttributes.entry.%d.Name", i))
		if name == "" {
			break
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = form.Get(fmt.Sprintf("MessageAttributes.entry.%d.Value.StringValue", i))
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
