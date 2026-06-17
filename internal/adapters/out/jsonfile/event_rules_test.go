package jsonfile

import (
	"testing"

	"github.com/mockwave/mockwave/domain"
)

func TestEventRulesMemStore(t *testing.T) {
	st := NewMemStore(domain.Config{
		EventRules: []domain.EventRule{{ID: "a", Match: domain.EventMatch{Service: "sns"}}},
	})
	got, err := st.GetEventRules()
	if err != nil || len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("GetEventRules = %v, %v", got, err)
	}
}
