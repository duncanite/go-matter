package mattertest_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/mattertest"
)

var errFuse = errors.New("a fuse")

// far waits for the far side to hold a device by a name, as it stands
// once so() says it does.
func far(t *testing.T, source matter.Source, name string, so func(matter.Device) bool) matter.Device {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		devices, err := source.Devices(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		for index := range devices {
			if devices[index].Name == name && (so == nil || so(devices[index])) {
				return devices[index]
			}
		}
	}

	t.Fatalf("%s never crossed as expected", name)

	return matter.Device{}
}

// TestWhatHoldsNearHoldsFar: each kind of device, as it is described and
// as it stands, is the same device on the far side.
func TestWhatHoldsNearHoldsFar(t *testing.T) {
	t.Parallel()

	var home matter.Home

	set := func(context.Context, bool) error { return nil }
	setLevel := func(context.Context, matter.Level, time.Duration) error { return nil }

	mattertest.Light(&home, "light")
	mattertest.Dimmer(&home, "dimmer")
	mattertest.Plug(&home, "plug")

	if err := home.Put(matter.Device{
		ID: "outlet", Name: "outlet", Kind: matter.Plug, Room: "Hall", Vendor: "Maker", Unreachable: true,
		OnOff: &matter.OnOff{On: true, Set: set}, LevelControl: &matter.LevelControl{Level: 0.5, Set: setLevel},
	}); err != nil {
		t.Fatal(err)
	}

	across := mattertest.Across(t, &home)

	near, err := home.Devices(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for index := range near {
		here := near[index]
		there := far(
			t,
			across,
			here.Name,
			func(there matter.Device) bool { return there.Unreachable == here.Unreachable },
		)

		if there.Kind != here.Kind || there.Room != here.Room ||
			(there.LevelControl == nil) != (here.LevelControl == nil) ||
			there.OnOff == nil ||
			there.OnOff.On != here.OnOff.On ||
			there.OnOff.Set == nil {
			t.Errorf("%s crossed as %+v, on: %+v", here.Name, there.Info, there.OnOff)
		}

		if here.LevelControl != nil &&
			(there.LevelControl.Level != here.LevelControl.Level || there.LevelControl.Set == nil) {
			t.Errorf("%s crossed at the level %v", here.Name, there.LevelControl.Level)
		}

		if here.Vendor != "" && there.Vendor != here.Vendor {
			t.Errorf("%s crossed as made by %q", here.Name, there.Vendor)
		}

		if there.ID == here.ID || there.ID == "" {
			t.Errorf("%s crossed with the ID %q", here.Name, there.ID)
		}
	}

	if all, _ := across.Devices(t.Context()); len(all) != len(near) {
		t.Fatalf("%d devices crossed, of %d", len(all), len(near))
	}
}

// TestACommandCrosses: what is asked on the far side is what the device
// is asked, and what it answers is what the far side is answered.
func TestACommandCrosses(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()
	lamp := mattertest.Dimmer(&home, "lamp")
	across := mattertest.Across(t, &home)
	there := far(t, across, "lamp", nil)

	for _, step := range []struct {
		ask  func() error
		want mattertest.Ask
	}{
		{func() error { return there.OnOff.Set(ctx, true) }, mattertest.SetOn{On: true}},
		{func() error { return there.LevelControl.Set(ctx, 0.5, 2*time.Second) }, mattertest.SetLevel{Level: 0.5, Over: 2 * time.Second}},
		{func() error { return there.LevelControl.Set(ctx, 1, 0) }, mattertest.SetLevel{Level: 1}},
		{func() error { return there.LevelControl.Move(ctx, matter.Down) }, mattertest.Move{Direction: matter.Down}},
		{func() error { return there.LevelControl.Stop(ctx) }, mattertest.Stop{}},
		{func() error { return there.LevelControl.Move(ctx, matter.Up) }, mattertest.Move{Direction: matter.Up}},
		{func() error { return there.OnOff.Set(ctx, false) }, mattertest.SetOn{On: false}},
	} {
		before := len(lamp.Asked())

		if err := step.ask(); err != nil {
			t.Fatalf("%+v: %v", step.want, err)
		}

		if asked := lamp.Asked(); len(asked) != before+1 || !reflect.DeepEqual(asked[before], step.want) {
			t.Fatalf("%+v reached the lamp as %+v", step.want, asked[before:])
		}
	}

	// What cannot be said in Matter is refused before it is sent.
	before := len(lamp.Asked())

	for name, err := range map[string]error{
		"a level of zero":           there.LevelControl.Set(ctx, 0, 0),
		"a level above full":        there.LevelControl.Set(ctx, 1.1, 0),
		"a level that is not one":   there.LevelControl.Set(ctx, matter.Level(math.NaN()), 0),
		"a time backwards":          there.LevelControl.Set(ctx, 0.5, -time.Second),
		"a time between tenths":     there.LevelControl.Set(ctx, 0.5, 150*time.Millisecond),
		"a time Matter cannot hold": there.LevelControl.Set(ctx, 0.5, 3*time.Hour),
		"a direction that is none":  there.LevelControl.Move(ctx, 0),
	} {
		if !errors.Is(err, matter.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if len(lamp.Asked()) != before {
		t.Fatalf("what cannot be said reached the lamp: %+v", lamp.Asked()[before:])
	}

	// The device's error is the far side's.
	for name, row := range map[string]struct{ fail, want error }{
		"invalid":     {matter.ErrInvalid, matter.ErrInvalid},
		"busy":        {matter.ErrBusy, matter.ErrBusy},
		"unreachable": {matter.ErrUnreachable, matter.ErrUnreachable},
		"failed":      {matter.ErrFailed, matter.ErrFailed},
		"its own":     {errFuse, matter.ErrFailed},
	} {
		lamp.Fail(row.fail)

		for command, err := range map[string]error{
			"on":    there.OnOff.Set(ctx, true),
			"level": there.LevelControl.Set(ctx, 0.5, 0),
			"move":  there.LevelControl.Move(ctx, matter.Up),
			"stop":  there.LevelControl.Stop(ctx),
		} {
			if !errors.Is(err, row.want) {
				t.Errorf("%s, %s: %v", name, command, err)
			}
		}
	}
}

// heard collects what a Watch is told.
type heard struct {
	mu     sync.Mutex
	events []matter.Event
}

func (h *heard) take(event matter.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.events = append(h.events, event)
}

// wait is the first event that is so, among those heard.
func (h *heard) wait(t *testing.T, what string, so func(matter.Event) bool) matter.Event {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		h.mu.Lock()

		for _, event := range h.events {
			if so(event) {
				h.mu.Unlock()

				return event
			}
		}

		h.mu.Unlock()
	}

	t.Fatalf("never heard: %s", what)

	return matter.Event{}
}

// TestWhatHappensCrosses: what happens to a device near is told to a
// watcher far.
func TestWhatHappensCrosses(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		told heard
	)

	mattertest.Dimmer(&home, "lamp")

	across := mattertest.Across(t, &home)
	there := far(t, across, "lamp", nil)

	go func() { _ = across.Watch(t.Context(), told.take) }()

	told.wait(t, "the lamp, as it stands, first", func(event matter.Event) bool {
		added, is := event.Change.(matter.DeviceAdded)

		return is && added.Device.ID == there.ID && added.Device.Name == "lamp"
	})

	if err := home.Report("lamp", matter.OnOffChanged{On: true}, matter.LevelChanged{Level: 0.25}); err != nil {
		t.Fatal(err)
	}

	told.wait(t, "the lamp on", func(event matter.Event) bool {
		return event.Device == there.ID && event.Change == matter.OnOffChanged{On: true}
	})

	told.wait(t, "the lamp near a quarter", func(event matter.Event) bool {
		changed, is := event.Change.(matter.LevelChanged)

		return is && event.Device == there.ID && math.Abs(float64(changed.Level)-0.25) < 0.005
	})

	if err := home.Report("lamp", matter.ReachabilityChanged{Unreachable: true}); err != nil {
		t.Fatal(err)
	}

	told.wait(t, "the lamp unreachable", func(event matter.Event) bool {
		return event.Device == there.ID && event.Change == matter.ReachabilityChanged{Unreachable: true}
	})

	// A device that comes and one that goes.
	mattertest.Plug(&home, "plug")

	plug := told.wait(t, "the plug added", func(event matter.Event) bool {
		added, is := event.Change.(matter.DeviceAdded)

		return is && added.Device.Name == "plug" && added.Device.Kind == matter.Plug
	})

	home.Remove("plug")

	told.wait(t, "the plug removed", func(event matter.Event) bool {
		return event.Device == plug.Device && event.Change == matter.DeviceRemoved{}
	})

	// A remote crosses as a remote for each of its buttons, named by
	// both, and a button down and up near is that remote's far.
	if err := home.Put(matter.Device{
		ID: "pico", Name: "Pico", Kind: matter.Remote, Buttons: []matter.Button{{Label: "On"}, {Label: "Off"}},
	}); err != nil {
		t.Fatal(err)
	}

	first, second := far(t, across, "Pico On", nil), far(t, across, "Pico Off", nil)
	if first.Kind != matter.Remote || len(first.Buttons) != 1 || len(second.Buttons) != 1 || first.ID == second.ID {
		t.Fatalf("a remote crossed as %+v and %+v", first, second)
	}

	// Its one button is labelled as the switch is named.
	if first.Buttons[0].Label != "Pico On" || second.Buttons[0].Label != "Pico Off" {
		t.Fatalf("the buttons crossed labelled %q and %q", first.Buttons[0].Label, second.Buttons[0].Label)
	}

	if err := home.Report("pico", matter.ButtonPressed{Button: 1}, matter.ButtonReleased{Button: 1}); err != nil {
		t.Fatal(err)
	}

	told.wait(t, "the second button down", func(event matter.Event) bool {
		return event.Device == second.ID && event.Change == matter.ButtonPressed{}
	})

	told.wait(t, "the second button up", func(event matter.Event) bool {
		return event.Device == second.ID && event.Change == matter.ButtonReleased{}
	})

	// A remote lost and found again is neither a button down nor one up:
	// what a bridge says of a device's reachability is not a switch's.
	ups := func() int {
		told.mu.Lock()
		defer told.mu.Unlock()

		count := 0

		for _, event := range told.events {
			if _, up := event.Change.(matter.ButtonReleased); up {
				count++
			}

			if _, down := event.Change.(matter.ButtonPressed); down {
				count++
			}
		}

		return count
	}

	before := ups()

	if err := home.Report("pico", matter.ReachabilityChanged{Unreachable: true}); err != nil {
		t.Fatal(err)
	}

	told.wait(t, "the remote lost", func(event matter.Event) bool {
		return event.Device == second.ID && event.Change == matter.ReachabilityChanged{Unreachable: true}
	})

	if err := home.Report("pico", matter.ReachabilityChanged{}); err != nil {
		t.Fatal(err)
	}

	told.wait(t, "the remote found", func(event matter.Event) bool {
		return event.Device == second.ID && event.Change == matter.ReachabilityChanged{}
	})

	time.Sleep(200 * time.Millisecond)

	if after := ups(); after != before {
		t.Fatalf("a remote lost and found was heard as %d presses or releases", after-before)
	}

	// Held down long enough to be a long press, a button still goes down
	// and comes up, once each.
	if err := home.Report("pico", matter.ButtonPressed{Button: 0}); err != nil {
		t.Fatal(err)
	}

	told.wait(t, "the first button down", func(event matter.Event) bool {
		return event.Device == first.ID && event.Change == matter.ButtonPressed{}
	})

	time.Sleep(2500 * time.Millisecond)

	if err := home.Report("pico", matter.ButtonReleased{Button: 0}); err != nil {
		t.Fatal(err)
	}

	told.wait(t, "the first button up, after a long press", func(event matter.Event) bool {
		return event.Device == first.ID && event.Change == matter.ButtonReleased{}
	})

	// A device whose name alone changed is told of anew, under it.
	if err := home.Put(matter.Device{
		ID: "lamp", Name: "renamed", Kind: matter.Light,
		OnOff: &matter.OnOff{Set: func(context.Context, bool) error { return nil }},
		LevelControl: &matter.LevelControl{
			Level: 1, Set: func(context.Context, matter.Level, time.Duration) error { return nil },
		},
	}); err != nil {
		t.Fatal(err)
	}

	if renamed := far(t, across, "renamed", nil); renamed.LevelControl == nil {
		t.Fatalf("a device renamed crossed as %+v", renamed)
	}

	// One that lost its level, under the same name, is told of anew
	// without it.
	if err := home.Put(matter.Device{
		ID: "lamp", Name: "renamed", Kind: matter.Light,
		OnOff: &matter.OnOff{Set: func(context.Context, bool) error { return nil }},
	}); err != nil {
		t.Fatal(err)
	}

	far(t, across, "renamed", func(there matter.Device) bool { return there.LevelControl == nil })

	// A device described anew is that device, anew.
	renamed := matter.Device{
		ID: "lamp", Name: "lantern", Kind: matter.Light,
		OnOff: &matter.OnOff{Set: func(context.Context, bool) error { return nil }},
	}

	if err := home.Put(renamed); err != nil {
		t.Fatal(err)
	}

	if lantern := far(t, across, "lantern", nil); lantern.LevelControl != nil || lantern.Kind != matter.Light {
		t.Fatalf("a device described anew crossed as %+v", lantern)
	}

	told.wait(t, "the lantern, told of", func(event matter.Event) bool {
		added, is := event.Change.(matter.DeviceAdded)

		return is && added.Device.Name == "lantern"
	})

	told.mu.Lock()
	defer told.mu.Unlock()

	presses := 0

	for _, event := range told.events {
		if event.Device == first.ID && event.Change == (matter.ButtonPressed{}) {
			presses++
		}
	}

	if presses != 1 {
		t.Fatalf("one long press of the first button was heard as %d", presses)
	}

	for _, event := range told.events {
		if event.Device == second.ID {
			if _, pressed := event.Change.(matter.LevelChanged); pressed {
				t.Fatalf("the second button heard %#v", event.Change)
			}
		}
	}
}
