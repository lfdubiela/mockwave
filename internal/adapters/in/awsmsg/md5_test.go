package awsmsg

import (
	"crypto/md5"
	"encoding/hex"
	"testing"
)

func TestMD5OfBody(t *testing.T) {
	// Known vector: md5("hello") = 5d41402abc4b2a76b9719d911017c592
	if got := md5OfBody("hello"); got != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("md5OfBody = %q", got)
	}
	if got := md5OfBody(""); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("md5OfBody(empty) = %q", got)
	}
}

func TestMD5OfAttributes(t *testing.T) {
	if got := md5OfAttributes(nil); got != "" {
		t.Fatalf("empty attrs should be \"\", got %q", got)
	}
	a := []MsgAttr{
		{Name: "env", DataType: "String", StringValue: "prod"},
		{Name: "n", DataType: "Number", StringValue: "7"},
	}
	// Deterministic + order-independent (sorted by name internally).
	h1 := md5OfAttributes(a)
	reordered := []MsgAttr{a[1], a[0]}
	h2 := md5OfAttributes(reordered)
	if h1 == "" || h1 != h2 {
		t.Fatalf("attr md5 must be deterministic & order-independent: %q vs %q", h1, h2)
	}
	// Different attribute sets produce different digests.
	if md5OfAttributes([]MsgAttr{{Name: "env", DataType: "String", StringValue: "dev"}}) == h1 {
		t.Fatalf("different attrs must differ")
	}
	// 32-char hex.
	if len(h1) != 32 {
		t.Fatalf("expected 32-char hex, got %d", len(h1))
	}
}

func TestMD5OfAttributesKnownAnswer(t *testing.T) {
	// Independent, explicit re-statement of the AWS wire format for a single
	// String attribute {Name:"env", DataType:"String", StringValue:"prod"}.
	// 4-byte BE length prefixes, transport-type byte 1 for String.
	expectedBytes := []byte{
		0, 0, 0, 3, 'e', 'n', 'v', // len("env")=3 + name
		0, 0, 0, 6, 'S', 't', 'r', 'i', 'n', 'g', // len("String")=6 + type
		1,                              // transport type: String
		0, 0, 0, 4, 'p', 'r', 'o', 'd', // len("prod")=4 + value
	}
	sum := md5.Sum(expectedBytes)
	want := hex.EncodeToString(sum[:])

	got := md5OfAttributes([]MsgAttr{{Name: "env", DataType: "String", StringValue: "prod"}})
	if got != want {
		t.Fatalf("md5OfAttributes single String attr = %q, want %q (encoding mismatch)", got, want)
	}
}

func TestMD5OfAttributesBinaryKnownAnswer(t *testing.T) {
	// Binary attribute uses transport-type byte 2 and the BinaryValue bytes.
	bin := []byte{0xDE, 0xAD}
	expectedBytes := []byte{
		0, 0, 0, 1, 'b', // len("b")=1 + name
		0, 0, 0, 6, 'B', 'i', 'n', 'a', 'r', 'y', // len("Binary")=6 + type
		2,                      // transport type: Binary
		0, 0, 0, 2, 0xDE, 0xAD, // len(bin)=2 + value
	}
	sum := md5.Sum(expectedBytes)
	want := hex.EncodeToString(sum[:])

	got := md5OfAttributes([]MsgAttr{{Name: "b", DataType: "Binary", BinaryValue: bin}})
	if got != want {
		t.Fatalf("md5OfAttributes Binary attr = %q, want %q", got, want)
	}
}
