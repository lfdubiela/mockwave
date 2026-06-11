package chaos_test

import (
	"testing"

	"github.com/mockwave/mockwave/internal/chaos"
)

func TestKillSwitch(t *testing.T) {
	ks := chaos.NewKillSwitch()
	if ks.Halted() {
		t.Fatal("new switch must start non-halted")
	}
	ks.Halt()
	if !ks.Halted() {
		t.Fatal("expected halted")
	}
	ks.Resume()
	if ks.Halted() {
		t.Fatal("expected resumed")
	}
}
