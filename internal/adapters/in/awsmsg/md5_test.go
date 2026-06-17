package awsmsg

import "testing"

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
