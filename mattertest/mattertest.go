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
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/todo"
)

// Fake is a device in a matter.Home that does as it is asked, at once,
// reports it, and remembers what it was asked. What happens to it at the
// wall is the home's to be told: home.Report. Reported unreachable, it
// is asked nothing, as the home answers for it.
type Fake struct {
	home *matter.Home
	id   matter.ID

	mu    sync.Mutex
	asked []Ask
	fail  error
}

// lowest is where a Dimmer told to move down ends: the least level it
// shows.
const lowest matter.Level = 0.01

// Light puts an on/off light in home, off, named as its ID. Like Dimmer
// and Plug, it panics on a device the home refuses, an empty ID: a test
// that asks for one is wrong, and has no error to check.
func Light(home *matter.Home, id matter.ID) *Fake {
	return put(home, id, matter.Light, false)
}

// Dimmer is Light with a level, at full, that moves and stops. Told to
// move, it is at once at the end it was sent towards: up, on and at
// full; down, at the least level it shows, on or off as it was.
func Dimmer(home *matter.Home, id matter.ID) *Fake {
	return put(home, id, matter.Light, true)
}

// Plug puts an outlet in home, off, named as its ID.
func Plug(home *matter.Home, id matter.ID) *Fake {
	return put(home, id, matter.Plug, false)
}

// put makes the fake and puts its device in the home. What the home
// refuses is the caller's ID, the one thing of the device that is the
// caller's, and panics.
func put(home *matter.Home, id matter.ID, kind matter.Kind, dims bool) *Fake {
	fake := &Fake{home: home, id: id}

	device := matter.Device{
		ID: id, Name: string(id), Kind: kind,
		OnOff: &matter.OnOff{Set: fake.setOn},
	}

	if dims {
		device.LevelControl = &matter.LevelControl{Level: 1, Set: fake.setLevel, Move: fake.move, Stop: fake.stop}
	}

	if err := home.Put(device); err != nil {
		panic(err)
	}

	return fake
}

// Fail makes its commands return err, and do nothing, until Fail(nil).
func (f *Fake) Fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fail = err
}

// Asked is every command that reached it, in order, those Fail refused
// too; not those the home answered while it was unreachable.
func (f *Fake) Asked() []Ask {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.asked)
}

// take records a command and says whether it is to be refused.
func (f *Fake) take(ask Ask) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.asked = append(f.asked, ask)

	return f.fail
}

func (f *Fake) setOn(_ context.Context, on bool) error {
	if err := f.take(SetOn{On: on}); err != nil {
		return err
	}

	return f.home.Report(f.id, matter.OnOffChanged{On: on})
}

// setLevel refuses, as a device would, a level outside a level's range
// and a time that runs backwards.
func (f *Fake) setLevel(_ context.Context, level matter.Level, over time.Duration) error {
	if err := f.take(SetLevel{Level: level, Over: over}); err != nil {
		return err
	}

	if !(level > 0 && level <= 1) || over < 0 {
		return fmt.Errorf("%w: the level %v over %s", matter.ErrInvalid, level, over)
	}

	return f.home.Report(f.id, matter.OnOffChanged{On: true}, matter.LevelChanged{Level: level})
}

func (f *Fake) move(_ context.Context, direction matter.Direction) error {
	if err := f.take(Move{Direction: direction}); err != nil {
		return err
	}

	switch direction {
	case matter.Up:
		return f.home.Report(f.id, matter.OnOffChanged{On: true}, matter.LevelChanged{Level: 1})
	case matter.Down:
		return f.home.Report(f.id, matter.LevelChanged{Level: lowest})
	default:
		return fmt.Errorf("%w: the direction %d", matter.ErrInvalid, direction)
	}
}

func (f *Fake) stop(context.Context) error {
	return f.take(Stop{})
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
