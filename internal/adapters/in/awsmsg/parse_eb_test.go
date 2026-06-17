package awsmsg

import "testing"

func TestParseEventBridge(t *testing.T) {
	body := []byte(`{"Entries":[
		{"Source":"orders","DetailType":"Created","Detail":"{\"id\":1}","EventBusName":"bus-a"},
		{"Source":"orders","DetailType":"Shipped","Detail":"{\"id\":2}"}
	]}`)
	evs, err := parseEventBridge(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 events, got %d", len(evs))
	}
	if evs[0].Service != "eventbridge" || evs[0].Operation != "PutEvents" {
		t.Fatalf("service/op = %q/%q", evs[0].Service, evs[0].Operation)
	}
	if evs[0].Source != "orders" || evs[0].DetailType != "Created" || string(evs[0].Message) != `{"id":1}` {
		t.Fatalf("entry0 = %+v", evs[0])
	}
	if evs[0].Target != "bus-a" {
		t.Fatalf("entry0 bus = %q", evs[0].Target)
	}
	// EventBusName defaults to "default" when omitted.
	if evs[1].Target != "default" {
		t.Fatalf("entry1 bus = %q, want default", evs[1].Target)
	}
}

func TestParseEventBridgeErrors(t *testing.T) {
	if _, err := parseEventBridge([]byte(`{"Entries":[]}`)); err == nil {
		t.Fatal("expected error on empty entries")
	}
	if _, err := parseEventBridge([]byte(`not json`)); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}
