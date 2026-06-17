package awsmsg

import (
	"encoding/json"
	"net/http"
)

// respondSQS writes a valid SQS SendMessage JSON response. The SDK validates
// MD5OfMessageBody (and MD5OfMessageAttributes when attributes are present), so
// both are computed faithfully.
func respondSQS(w http.ResponseWriter, body string, attrs []MsgAttr, messageID string) {
	resp := map[string]interface{}{
		"MD5OfMessageBody": md5OfBody(body),
		"MessageId":        messageID,
		"SequenceNumber":   nil,
	}
	if md := md5OfAttributes(attrs); md != "" {
		resp["MD5OfMessageAttributes"] = md
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
