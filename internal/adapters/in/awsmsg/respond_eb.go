package awsmsg

import (
	"encoding/json"
	"net/http"
)

// respondEventBridge writes a valid PutEvents JSON response with one EventId
// per input entry, in order.
func respondEventBridge(w http.ResponseWriter, eventIDs []string) {
	entries := make([]map[string]string, len(eventIDs))
	for i, id := range eventIDs {
		entries[i] = map[string]string{"EventId": id}
	}
	resp := map[string]interface{}{
		"FailedEntryCount": 0,
		"Entries":          entries,
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
