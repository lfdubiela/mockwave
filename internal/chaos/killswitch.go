// Package chaos holds runtime chaos-engineering primitives: the global fault
// kill switch and the fault-injection pipeline stage.
package chaos

import "sync/atomic"

// KillSwitch globally disables fault injection. Not persisted: a restart
// always resumes chaos.
type KillSwitch struct {
	halted atomic.Bool
}

func NewKillSwitch() *KillSwitch { return &KillSwitch{} }

func (k *KillSwitch) Halt()        { k.halted.Store(true) }
func (k *KillSwitch) Resume()      { k.halted.Store(false) }
func (k *KillSwitch) Halted() bool { return k.halted.Load() }
