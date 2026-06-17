package awsmsg

import (
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"io"
	"sort"
	"strings"
)

// MsgAttr is a message attribute carrying the fields needed to compute the AWS
// MD5-of-message-attributes checksum the SQS SDK validates.
type MsgAttr struct {
	Name        string
	DataType    string
	StringValue string
	BinaryValue []byte
}

// md5OfBody returns the hex MD5 of a message body — what the SDK validates as
// MD5OfMessageBody.
func md5OfBody(body string) string {
	sum := md5.Sum([]byte(body))
	return hex.EncodeToString(sum[:])
}

// md5OfAttributes computes the AWS MD5-of-message-attributes digest: attributes
// sorted by name, each encoded as length-prefixed name, length-prefixed data
// type, a transport-type byte (1 = String/Number, 2 = Binary), then the
// length-prefixed value. Returns "" when there are no attributes.
func md5OfAttributes(attrs []MsgAttr) string {
	if len(attrs) == 0 {
		return ""
	}
	sorted := make([]MsgAttr, len(attrs))
	copy(sorted, attrs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	h := md5.New()
	for _, a := range sorted {
		writeLenPrefixed(h, []byte(a.Name))
		writeLenPrefixed(h, []byte(a.DataType))
		if strings.HasPrefix(a.DataType, "Binary") {
			h.Write([]byte{2})
			writeLenPrefixed(h, a.BinaryValue)
		} else {
			h.Write([]byte{1})
			writeLenPrefixed(h, []byte(a.StringValue))
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func writeLenPrefixed(h io.Writer, b []byte) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(b)))
	_, _ = h.Write(lenBuf[:])
	_, _ = h.Write(b)
}
