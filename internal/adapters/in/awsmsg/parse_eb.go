package awsmsg

import (
	"encoding/json"
	"fmt"

	"github.com/mockwave/mockwave/domain"
)

type ebPutEvents struct {
	Entries []ebEntry `json:"Entries"`
}

type ebEntry struct {
	Source       string `json:"Source"`
	DetailType   string `json:"DetailType"`
	Detail       string `json:"Detail"`
	EventBusName string `json:"EventBusName"`
}

// parseEventBridge converts a PutEvents JSON body into one normalized Event per
// entry — PutEvents is a batch operation.
func parseEventBridge(body []byte) ([]domain.Event, error) {
	var in ebPutEvents
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("awsmsg: PutEvents body: %w", err)
	}
	if len(in.Entries) == 0 {
		return nil, fmt.Errorf("awsmsg: PutEvents has no entries")
	}
	evs := make([]domain.Event, len(in.Entries))
	for i, e := range in.Entries {
		bus := e.EventBusName
		if bus == "" {
			bus = "default"
		}
		evs[i] = domain.Event{
			Service:    domain.EventServiceEventBridge,
			Operation:  "PutEvents",
			Target:     bus,
			Source:     e.Source,
			DetailType: e.DetailType,
			Message:    []byte(e.Detail),
		}
	}
	return evs, nil
}
