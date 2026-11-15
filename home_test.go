package matter_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
)

// wired is a device's commands, recording what reached them.
type wired struct {
	mu    sync.Mutex
	asked []string
	fail  error
}

func (w *wired) note(format string, args ...any) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.asked = append(w.asked, fmt.Sprintf(format, args...))

	return w.fail
}

func (w *wired) got() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]string(nil), w.asked...)
}

func (w *wired) dimmer(id matter.ID) matter.Device {
	return matter.Device{
		ID: id, Name: string(id), Kind: matter.Light,
		OnOff: &matter.OnOff{Set: func(_ context.Context, on bool) error { return w.note("on %t", on) }},
		LevelControl: &matter.LevelControl{
			Level: 1,
			Set: func(_ context.Context, level matter.Level, over time.Duration) error {
				return w.note("level %v over %s", level, over)
			},
			Move: func(_ context.Context, direction matter.Direction) error { return w.note("move %d", direction) },
			Stop: func(context.Context) error { return w.note("stop") },
		},
	}
}

func remote(id matter.ID) matter.Device {
	return matter.Device{ID: id, Kind: matter.Remote, Buttons: []matter.Button{{Label: "On"}, {Label: "Off"}}}
}

// watch runs a Watch and gives what it was told, as text, on a channel.
func watch(t *testing.T, home *matter.Home) (<-chan string, context.CancelFunc, <-chan error) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	told := make(chan string, 1024)
	done := make(chan error, 1)

	go func() {
		done <- home.Watch(ctx, func(event matter.Event) {
			if added, is := event.Change.(matter.DeviceAdded); is {
				told <- fmt.Sprintf("%s added", added.Device.ID)

				return
			}

			told <- fmt.Sprintf("%s %T%v", event.Device, event.Change, event.Change)
		})
	}()

	return told, cancel, done
}

func next(t *testing.T, told <-chan string) string {
	t.Helper()

	select {
	case event := <-told:
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("the watcher was told nothing")

		return ""
	}
}

func quiet(t *testing.T, told <-chan string) {
	t.Helper()

	select {
	case event := <-told:
		t.Fatalf("the watcher was told %s", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHomeHoldsDevicesAsValues(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		w    wired
	)

	if _, held := home.Device("b"); held {
		t.Fatal("an empty home holds a device")
	}

	if err := home.Put(matter.Device{ID: "x", Kind: matter.Light}); !errors.Is(err, matter.ErrDescription) {
		t.Fatalf("a light without OnOff, put: %v", err)
	}

	put := w.dimmer("b")
	for _, device := range []matter.Device{put, w.dimmer("a"), remote("c")} {
		if err := home.Put(device); err != nil {
			t.Fatal(err)
		}
	}

	// The home took its own copy: what was put can change without it.
	put.OnOff.On = true
	put.Name = "changed"

	devices, err := home.Devices(t.Context())
	if err != nil || len(devices) != 3 || devices[0].ID != "a" || devices[1].ID != "b" || devices[2].ID != "c" {
		t.Fatalf("the devices, in the order of their IDs: %+v, %v", devices, err)
	}

	if devices[1].OnOff.On || devices[1].Name != "b" {
		t.Fatalf("the home's device followed the caller's: %+v", devices[1])
	}

	// And what it hands out is the caller's own.
	devices[1].OnOff.On = true
	devices[1].LevelControl.Level = 0.1
	devices[2].Buttons[0].Label = "changed"

	again, _ := home.Device("b")
	buttons, _ := home.Device("c")

	if again.OnOff.On || again.LevelControl.Level != 1 || buttons.Buttons[0].Label != "On" {
		t.Fatalf("the home followed a copy it handed out: %+v %+v", again, buttons)
	}

	// A device of one home put into another works through both.
	var elsewhere matter.Home

	if err := elsewhere.Put(again); err != nil {
		t.Fatalf("a device of one home put into another: %v", err)
	}

	moved, _ := elsewhere.Device("b")
	reached := len(w.got())

	if err := moved.OnOff.Set(t.Context(), true); err != nil || len(w.got()) != reached+1 {
		t.Fatalf("a command through two homes: %v, %v", err, w.got()[reached:])
	}

	home.Remove("a")
	home.Remove("a")

	if devices, _ = home.Devices(t.Context()); len(devices) != 2 {
		t.Fatalf("after a was removed: %+v", devices)
	}
}

func TestHomeReports(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		w    wired
	)

	if err := errors.Join(home.Put(w.dimmer("lamp")), home.Put(remote("pico"))); err != nil {
		t.Fatal(err)
	}

	told, cancel, _ := watch(t, &home)
	defer cancel()

	if first, second := next(t, told), next(t, told); first != "lamp added" || second != "pico added" {
		t.Fatalf("a watcher's first look: %s, %s", first, second)
	}

	// What changes is told; what does not is not.
	for _, step := range []struct {
		change matter.Change
		want   string
	}{
		{matter.OnOffChanged{On: true}, "lamp matter.OnOffChanged{true}"},
		{matter.OnOffChanged{On: true}, ""},
		{matter.LevelChanged{Level: 0.4}, "lamp matter.LevelChanged{0.4}"},
		{matter.LevelChanged{Level: 0.4}, ""},
		{matter.ReachabilityChanged{Unreachable: true}, "lamp matter.ReachabilityChanged{true}"},
		{matter.ReachabilityChanged{Unreachable: true}, ""},
		{matter.ReachabilityChanged{}, "lamp matter.ReachabilityChanged{false}"},
	} {
		if err := home.Report("lamp", step.change); err != nil {
			t.Fatalf("%#v: %v", step.change, err)
		}

		if step.want == "" {
			quiet(t, told)
		} else if got := next(t, told); got != step.want {
			t.Fatalf("%#v: told %s", step.change, got)
		}
	}

	lamp, _ := home.Device("lamp")
	if !lamp.OnOff.On || lamp.LevelControl.Level != 0.4 || lamp.Unreachable {
		t.Fatalf("the lamp's state did not follow: %+v %+v", lamp.OnOff, lamp.LevelControl)
	}

	// A button is an occurrence: two are two.
	for range 2 {
		if err := home.Report("pico", matter.ButtonPressed{Button: 1}, matter.ButtonReleased{Button: 1}); err != nil {
			t.Fatal(err)
		}

		if down, up := next(t, told), next(t, told); down != "pico matter.ButtonPressed{1}" ||
			up != "pico matter.ButtonReleased{1}" {
			t.Fatalf("a button: %s, %s", down, up)
		}
	}

	// What a device cannot have is refused, whole, and told to no one.
	for name, row := range map[string]struct {
		id      matter.ID
		changes []matter.Change
		want    error
	}{
		"no such device":     {"nobody", []matter.Change{matter.OnOffChanged{On: true}}, matter.ErrNoDevice},
		"a remote switched":  {"pico", []matter.Change{matter.OnOffChanged{On: true}}, matter.ErrDescription},
		"a remote dimmed":    {"pico", []matter.Change{matter.LevelChanged{Level: 0.5}}, matter.ErrDescription},
		"a level of zero":    {"lamp", []matter.Change{matter.LevelChanged{}}, matter.ErrDescription},
		"a level above full": {"lamp", []matter.Change{matter.LevelChanged{Level: 1.5}}, matter.ErrDescription},
		"a level not one":    {"lamp", []matter.Change{matter.LevelChanged{Level: matter.Level(math.NaN())}}, matter.ErrDescription},
		"a button too many":  {"pico", []matter.Change{matter.ButtonPressed{Button: 2}}, matter.ErrDescription},
		"a button below":     {"pico", []matter.Change{matter.ButtonReleased{Button: -1}}, matter.ErrDescription},
		"a lamp's button":    {"lamp", []matter.Change{matter.ButtonPressed{}}, matter.ErrDescription},
		"added by report":    {"lamp", []matter.Change{matter.DeviceAdded{}}, matter.ErrDescription},
		"removed by report":  {"lamp", []matter.Change{matter.DeviceRemoved{}}, matter.ErrDescription},
		"no change at all":   {"lamp", []matter.Change{nil}, matter.ErrDescription},
		"good, then refused": {
			"lamp", []matter.Change{matter.OnOffChanged{On: false}, matter.LevelChanged{Level: 7}}, matter.ErrDescription,
		},
	} {
		if err := home.Report(row.id, row.changes...); !errors.Is(err, row.want) {
			t.Errorf("%s: %v", name, err)
		}
	}

	quiet(t, told)

	if lamp, _ = home.Device("lamp"); !lamp.OnOff.On {
		t.Fatal("a refused report was taken in part")
	}

	// Put over a device is that one removed and the new one added.
	if err := home.Put(w.dimmer("lamp")); err != nil {
		t.Fatal(err)
	}

	if gone, back := next(t, told), next(t, told); gone != "lamp matter.DeviceRemoved{}" || back != "lamp added" {
		t.Fatalf("a device replaced: %s, %s", gone, back)
	}

	home.Remove("pico")

	if gone := next(t, told); gone != "pico matter.DeviceRemoved{}" {
		t.Fatalf("a device removed: %s", gone)
	}

	// Removing what is not there is told to no one.
	home.Remove("pico")
	home.Remove("nobody")
	quiet(t, told)
}

func TestHomeAnswersForItsDevices(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		w    wired
	)

	if err := home.Put(w.dimmer("lamp")); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	lamp, _ := home.Device("lamp")

	if err := errors.Join(
		lamp.OnOff.Set(ctx, true),
		lamp.LevelControl.Set(ctx, 0.3, 2*time.Second),
		lamp.LevelControl.Move(ctx, matter.Up),
		lamp.LevelControl.Stop(ctx),
	); err != nil {
		t.Fatal(err)
	}

	want := []string{"on true", "level 0.3 over 2s", "move 1", "stop"}
	if got := w.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the commands reached the device as %v", got)
	}

	// A command's own error is the caller's.
	w.fail = matter.ErrBusy

	if err := lamp.OnOff.Set(ctx, false); !errors.Is(err, matter.ErrBusy) {
		t.Fatalf("a busy device: %v", err)
	}

	w.fail = nil
	reached := len(w.got())

	// Unreachable: answered for, and not asked.
	if err := home.Report("lamp", matter.ReachabilityChanged{Unreachable: true}); err != nil {
		t.Fatal(err)
	}

	for name, err := range map[string]error{
		"on":    lamp.OnOff.Set(ctx, true),
		"level": lamp.LevelControl.Set(ctx, 1, 0),
		"move":  lamp.LevelControl.Move(ctx, matter.Down),
		"stop":  lamp.LevelControl.Stop(ctx),
	} {
		if !errors.Is(err, matter.ErrUnreachable) {
			t.Errorf("%s of an unreachable device: %v", name, err)
		}
	}

	if len(w.got()) != reached {
		t.Fatalf("an unreachable device was asked: %v", w.got()[reached:])
	}

	// Replaced: the copy read before is of a device the home no longer
	// holds, and the one read after reaches the new one.
	var other wired

	if err := home.Put(other.dimmer("lamp")); err != nil {
		t.Fatal(err)
	}

	for name, err := range map[string]error{
		"on":    lamp.OnOff.Set(ctx, true),
		"level": lamp.LevelControl.Set(ctx, 1, 0),
		"move":  lamp.LevelControl.Move(ctx, matter.Down),
		"stop":  lamp.LevelControl.Stop(ctx),
	} {
		if !errors.Is(err, matter.ErrNoDevice) {
			t.Errorf("%s through a copy of a device since replaced: %v", name, err)
		}
	}

	fresh, _ := home.Device("lamp")

	if err := fresh.OnOff.Set(ctx, true); err != nil || len(other.got()) != 1 || len(w.got()) != reached {
		t.Fatalf("the device read after: %v, new %v, old %v", err, other.got(), w.got()[reached:])
	}

	// A device put without Move and Stop is handed out without them.
	plain := other.dimmer("lamp")
	plain.LevelControl.Move, plain.LevelControl.Stop = nil, nil

	if err := home.Put(plain); err != nil {
		t.Fatal(err)
	}

	if handed, _ := home.Device("lamp"); handed.LevelControl.Move != nil || handed.LevelControl.Stop != nil {
		t.Fatal("a device put without Move and Stop was handed out with them")
	}

	// Removed: the same answer.
	fresh, _ = home.Device("lamp")
	home.Remove("lamp")

	if err := fresh.OnOff.Set(ctx, true); !errors.Is(err, matter.ErrNoDevice) {
		t.Fatalf("a command of a device removed: %v", err)
	}
}

// TestHomeCommandsNeverComeBack: what the home handed out, put back into
// it by any road, is a device whose commands answer ErrNoDevice. They do
// not call themselves: each is bound to the device it was read from,
// which the Put replaced.
func TestHomeCommandsNeverComeBack(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	for name, back := range map[string]func(read matter.Device, other *matter.Home) matter.Device{
		"as it was read": func(read matter.Device, _ *matter.Home) matter.Device { return read },
		"its command in a capability of the caller's": func(read matter.Device, _ *matter.Home) matter.Device {
			return matter.Device{
				ID: read.ID, Kind: matter.Light,
				OnOff: &matter.OnOff{On: read.OnOff.On, Set: read.OnOff.Set},
			}
		},
		"its command wrapped": func(read matter.Device, _ *matter.Home) matter.Device {
			inner := read.OnOff.Set

			return matter.Device{
				ID: read.ID, Kind: matter.Light,
				OnOff: &matter.OnOff{Set: func(ctx context.Context, on bool) error { return inner(ctx, on) }},
			}
		},
		"through another home and back": func(read matter.Device, other *matter.Home) matter.Device {
			if err := other.Put(read); err != nil {
				t.Error(err)
			}

			through, _ := other.Device(read.ID)

			return through
		},
	} {
		var (
			home, other matter.Home
			w           wired
		)

		if err := home.Put(w.dimmer("lamp")); err != nil {
			t.Fatal(err)
		}

		read, _ := home.Device("lamp")

		if err := home.Put(back(read, &other)); err != nil {
			t.Fatalf("%s, put back: %v", name, err)
		}

		again, _ := home.Device("lamp")

		if err := again.OnOff.Set(ctx, true); !errors.Is(err, matter.ErrNoDevice) {
			t.Errorf("%s: its command answered %v", name, err)
		}

		if len(w.got()) != 0 {
			t.Errorf("%s: the device it replaced was asked %v", name, w.got())
		}
	}
}

func TestHomeWatchers(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		w    wired
	)

	if err := home.Put(w.dimmer("lamp")); err != nil {
		t.Fatal(err)
	}

	// One watcher stuck in its handler holds up no other, and nothing
	// the home is asked.
	stuck, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	slow := make(chan error, 1)
	seen := 0

	go func() {
		slow <- home.Watch(ctx, func(matter.Event) {
			seen++

			if seen == 1 {
				close(stuck)
				<-release
			}
		})
	}()

	<-stuck

	told, stop, done := watch(t, &home)

	if first := next(t, told); first != "lamp added" {
		t.Fatalf("a second watcher's first look: %s", first)
	}

	for index := range 100 {
		if err := home.Report("lamp", matter.OnOffChanged{On: index%2 == 0}); err != nil {
			t.Fatal(err)
		}
	}

	for index := range 100 {
		want := fmt.Sprintf("lamp matter.OnOffChanged{%t}", index%2 == 0)
		if got := next(t, told); got != want {
			t.Fatalf("event %d: %s", index, got)
		}
	}

	// A handler may ask things of the home and of the devices.
	asking, stopAsking := context.WithCancel(t.Context())
	asked := make(chan error, 1)

	go func() {
		_ = home.Watch(asking, func(event matter.Event) {
			if added, is := event.Change.(matter.DeviceAdded); is {
				asked <- errors.Join(
					added.Device.OnOff.Set(asking, true),
					home.Report(event.Device, matter.LevelChanged{Level: 0.2}),
				)
			}
		})
	}()

	if err := <-asked; err != nil {
		t.Fatalf("a handler asking: %v", err)
	}

	stopAsking()

	// A watch ends with its context, and with its context's error.
	stop()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("a watch whose context ended: %v", err)
	}

	// The stuck one, let go, ends with its context as any other.
	close(release)
	cancel()

	if err := <-slow; !errors.Is(err, context.Canceled) {
		t.Fatalf("the slow watch: %v", err)
	}
}

// TestHomeLosesNothing: watchers that start while the home is written to
// each end with the home's own state, having folded what they were told.
func TestHomeLosesNothing(t *testing.T) {
	t.Parallel()

	const (
		devices  = 8
		watchers = 16
		writes   = 400
	)

	var (
		home    matter.Home
		w       wired
		writing sync.WaitGroup
		folding sync.WaitGroup
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	final := matter.LevelChanged{Level: 0.75}
	folded := make([]map[matter.ID]matter.Level, watchers)
	settled := make(chan struct{}, watchers*devices)

	for index := range watchers {
		folded[index] = map[matter.ID]matter.Level{}

		folding.Go(func() {
			_ = home.Watch(ctx, func(event matter.Event) {
				switch change := event.Change.(type) {
				case matter.DeviceAdded:
					folded[index][event.Device] = change.Device.LevelControl.Level
				case matter.LevelChanged:
					folded[index][event.Device] = change.Level
				}

				if folded[index][event.Device] == final.Level {
					settled <- struct{}{}
				}
			})
		})
	}

	for index := range devices {
		id := matter.ID(fmt.Sprintf("d%d", index))

		writing.Go(func() {
			if err := home.Put(w.dimmer(id)); err != nil {
				t.Error(err)

				return
			}

			for write := range writes {
				_ = home.Report(id, matter.LevelChanged{Level: matter.Level(write%10+1) / 20})
			}

			_ = home.Report(id, final)
		})
	}

	writing.Wait()

	for range watchers * devices {
		select {
		case <-settled:
		case <-time.After(10 * time.Second):
			t.Fatal("a watcher never heard a device's last level")
		}
	}

	cancel()
	folding.Wait()

	for index, levels := range folded {
		if len(levels) != devices {
			t.Fatalf("watcher %d knows %d devices", index, len(levels))
		}

		for id, level := range levels {
			if level != final.Level {
				t.Fatalf("watcher %d has %s at %v", index, id, level)
			}
		}
	}
}

// TestHomeWatchersAreApart: what one watcher is told is its own, and a
// watch whose context has ended is handed nothing more.
func TestHomeWatchersAreApart(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		w    wired
	)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	if err := home.Put(w.dimmer("ready")); err != nil {
		t.Fatal(err)
	}

	// Both watch before the remote is put, so that both are told of it
	// by the same telling: each says so on its first look.
	watching := make(chan struct{}, 2)
	first, second := make(chan matter.Device, 1), make(chan matter.Device, 1)

	for _, told := range []chan matter.Device{first, second} {
		go func() {
			_ = home.Watch(ctx, func(event matter.Event) {
				added, is := event.Change.(matter.DeviceAdded)

				switch {
				case !is:
				case event.Device == "ready":
					watching <- struct{}{}
				default:
					told <- added.Device
				}
			})
		}()
	}

	<-watching
	<-watching

	if err := home.Put(remote("pico")); err != nil {
		t.Fatal(err)
	}

	mine := <-first
	mine.Buttons[0].Label = "changed"

	if theirs := <-second; theirs.Buttons[0].Label != "On" {
		t.Fatalf("one watcher wrote into what another was told: %+v", theirs)
	}

	cancel()

	// A handler that ends its own context is handed nothing after: two
	// devices wait in its first look, and it sees one.
	ending, end := context.WithCancel(t.Context())
	handed := 0

	err := home.Watch(ending, func(matter.Event) {
		handed++

		end()
	})
	if !errors.Is(err, context.Canceled) || handed != 1 {
		t.Fatalf("a watch that ended at its first event was handed %d, and returned %v", handed, err)
	}
}
