package awsmsg

import (
	"fmt"
	"net/http"
)

// respondSNS writes a valid SNS PublishResponse so the caller's SDK succeeds.
func respondSNS(w http.ResponseWriter, messageID, requestID string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w,
		`<PublishResponse xmlns="https://sns.amazonaws.com/doc/2010-03-31/">`+
			`<PublishResult><MessageId>%s</MessageId></PublishResult>`+
			`<ResponseMetadata><RequestId>%s</RequestId></ResponseMetadata>`+
			`</PublishResponse>`,
		messageID, requestID)
}
