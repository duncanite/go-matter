package matter

import (
	"context"

	"github.com/duncanite/go-matter/internal/todo"
)

// Home is a Source held in memory, for whatever knows the devices to
// write into: a maker's adapter, several of them side by side, a test.
// It keeps every promise a Source makes, so that none of them has to:
// the devices as they stand, watchers told in order and none of them
// held up by another, nothing lost between a watcher's first look and
// what follows, a device it holds unreachable answered for with
// ErrUnreachable and its commands not called. The zero Home is empty and
// ready, and safe to use from several goroutines.
type Home struct{}

// Put adds a device, or replaces the one of the same ID, as it is
// described: its state as given, its commands the ones to call from now
// on, while it is reachable. The home takes its own copy. A description
// that does not Validate is refused with its error and changes nothing.
// Watchers are told a new device added, and a replaced one removed and
// added.
func (h *Home) Put(device Device) error {
	return todo.ImplementMe(h, device)
}

// Remove takes a device out. Removing one that is not there does
// nothing.
func (h *Home) Remove(id ID) {
	_ = todo.ImplementMe(h, id)
}

// Report says what happened to a device: its state follows, and watchers
// are told. A change that changes nothing is told to no one, so an
// adapter may report what its hub says as often as its hub says it; a
// button is always told. ErrNoDevice is a device that is not there, and
// ErrDescription a change the device cannot have: a level for one with
// no LevelControl, a button it lacks, a device added or removed, which
// are Put's and Remove's to say.
func (h *Home) Report(id ID, changes ...Change) error {
	return todo.ImplementMe(h, id, changes)
}

// Device is one device as it stands, and whether it is there.
func (h *Home) Device(id ID) (Device, bool) {
	_ = todo.ImplementMe(h, id)

	return Device{}, false
}

// Devices is Source's. It never fails.
func (h *Home) Devices(ctx context.Context) ([]Device, error) {
	return nil, todo.ImplementMe(h, ctx)
}

// Watch is Source's. It ends only with ctx.
func (h *Home) Watch(ctx context.Context, handle func(Event)) error {
	return todo.ImplementMe(h, ctx, handle)
}
