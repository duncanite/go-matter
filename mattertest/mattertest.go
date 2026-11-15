// Package mattertest is for tests of code that speaks package matter's
// vocabulary: devices that do as they are asked and remember it, and the
// protocol itself, whole, inside the test's process.
//
// A test that an error crosses the protocol:
//
//	var home matter.Home
//
//	lamp := mattertest.Dimmer(&home, "lamp")
//
//	far, err := mattertest.Across(t, &home).Devices(t.Context())
//	if err != nil || len(far) != 1 {
//		t.Fatal(far, err)
//	}
//
//	lamp.Fail(matter.ErrBusy)
//
//	err = far[0].OnOff.Set(t.Context(), true)
//	if !errors.Is(err, matter.ErrBusy) {
//		t.Fatalf("a busy lamp, across: %v", err)
//	}
package mattertest

import (
	"testing"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/todo"
)

// Fake is a device in a matter.Home that does as it is asked, at once,
// reports it, and remembers what it was asked. What happens to it at the
// wall is the home's to be told: home.Report. Reported unreachable, it
// is asked nothing, as the home answers for it.
type Fake struct{}

// Light puts an on/off light in home, off, named as its ID.
func Light(home *matter.Home, id matter.ID) *Fake {
	_ = todo.ImplementMe(home, id)

	return nil
}

// Dimmer is Light with a level, at full, that moves and stops.
func Dimmer(home *matter.Home, id matter.ID) *Fake {
	_ = todo.ImplementMe(home, id)

	return nil
}

// Plug puts an outlet in home, off.
func Plug(home *matter.Home, id matter.ID) *Fake {
	_ = todo.ImplementMe(home, id)

	return nil
}

// Fail makes its commands return err, and do nothing, until Fail(nil).
func (f *Fake) Fail(err error) {
	_ = todo.ImplementMe(f, err)
}

// Asked is every command that reached it, in order, those Fail refused
// too; not those the home answered while it was unreachable.
func (f *Fake) Asked() []Ask {
	_ = todo.ImplementMe(f)

	return nil
}

// Ask is one command a Fake was sent: one of the types below.
type Ask interface{ ask() }

type (
	// SetOn is OnOff.Set.
	SetOn struct{ On bool }
	// SetLevel is LevelControl.Set.
	SetLevel struct {
		Level matter.Level
		Over  time.Duration
	}
	// Move is LevelControl.Move.
	Move struct{ Direction matter.Direction }
	// Stop is LevelControl.Stop.
	Stop struct{}
)

func (SetOn) ask()    {}
func (SetLevel) ask() {}
func (Move) ask()     {}
func (Stop) ask()     {}

// Across serves near through a bridge, commissions that bridge with a
// controller, and returns the controller's view of it: the same devices,
// having crossed the protocol and back, in this process and on no
// network. What holds of near holds of what it returns, but for the
// devices' IDs, which every Source chooses for itself; a test of that is
// a test of this module. Both ends close with the test.
func Across(tb testing.TB, near matter.Source) matter.Source {
	tb.Helper()

	_ = todo.ImplementMe(tb, near)

	return nil
}
