package mattertest_test

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/mattertest"
)

func TestFakesDoAsAsked(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()
	light := mattertest.Light(&home, "light")
	dimmer := mattertest.Dimmer(&home, "dimmer")
	plug := mattertest.Plug(&home, "plug")

	devices, err := home.Devices(ctx)
	if err != nil || len(devices) != 3 {
		t.Fatal(devices, err)
	}

	// As they are put: named as their IDs, off, the dimmer at full.
	got, held := home.Device("dimmer")
	if !held || got.Name != "dimmer" || got.Kind != matter.Light || got.OnOff.On || got.LevelControl.Level != 1 ||
		got.LevelControl.Move == nil || got.LevelControl.Stop == nil {
		t.Fatalf("a dimmer as put: %+v", got)
	}

	if got, _ = home.Device("light"); got.Kind != matter.Light || got.LevelControl != nil {
		t.Fatalf("a light as put: %+v", got)
	}

	if got, _ = home.Device("plug"); got.Kind != matter.Plug || got.LevelControl != nil || got.OnOff.On {
		t.Fatalf("a plug as put: %+v", got)
	}

	// What it is asked it does, at once, and the home says so.
	got, _ = home.Device("dimmer")

	for _, step := range []struct {
		ask   func() error
		on    bool
		level matter.Level
	}{
		{func() error { return got.OnOff.Set(ctx, true) }, true, 1},
		{func() error { return got.LevelControl.Set(ctx, 0.3, 2*time.Second) }, true, 0.3},
		{func() error { return got.OnOff.Set(ctx, false) }, false, 0.3},
		{func() error { return got.LevelControl.Set(ctx, 0.6, 0) }, true, 0.6},
		{func() error { return got.LevelControl.Move(ctx, matter.Down) }, true, 0.01},
		{func() error { return got.LevelControl.Stop(ctx) }, true, 0.01},
		{func() error { return got.OnOff.Set(ctx, false) }, false, 0.01},
		{func() error { return got.LevelControl.Move(ctx, matter.Up) }, true, 1},
	} {
		if err = step.ask(); err != nil {
			t.Fatal(err)
		}

		now, _ := home.Device("dimmer")
		if now.OnOff.On != step.on || now.LevelControl.Level != step.level {
			t.Fatalf(
				"the dimmer stands on %t at %v, for %t at %v",
				now.OnOff.On,
				now.LevelControl.Level,
				step.on,
				step.level,
			)
		}
	}

	want := []mattertest.Ask{
		mattertest.SetOn{On: true},
		mattertest.SetLevel{Level: 0.3, Over: 2 * time.Second},
		mattertest.SetOn{On: false},
		mattertest.SetLevel{Level: 0.6},
		mattertest.Move{Direction: matter.Down},
		mattertest.Stop{},
		mattertest.SetOn{On: false},
		mattertest.Move{Direction: matter.Up},
	}
	if asked := dimmer.Asked(); !reflect.DeepEqual(asked, want) {
		t.Fatalf("the dimmer was asked %+v", asked)
	}

	if len(light.Asked())+len(plug.Asked()) != 0 {
		t.Fatal("a device not asked remembers being asked")
	}

	// Asked's answer is the caller's own.
	dimmer.Asked()[0] = mattertest.Stop{}

	if dimmer.Asked()[0] != (mattertest.SetOn{On: true}) {
		t.Fatal("the record followed a copy of it")
	}
}

func TestFakesRefuse(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()
	dimmer := mattertest.Dimmer(&home, "dimmer")
	got, _ := home.Device("dimmer")

	// What no dimmer could do is ErrInvalid, recorded, and changes nothing.
	for name, err := range map[string]error{
		"a level of zero":     got.LevelControl.Set(ctx, 0, 0),
		"a level not one":     got.LevelControl.Set(ctx, matter.Level(math.NaN()), 0),
		"a level above full":  got.LevelControl.Set(ctx, 1.5, 0),
		"a time backwards":    got.LevelControl.Set(ctx, 0.5, -time.Second),
		"a direction not one": got.LevelControl.Move(ctx, 0),
	} {
		if !errors.Is(err, matter.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if now, _ := home.Device("dimmer"); now.OnOff.On || now.LevelControl.Level != 1 || len(dimmer.Asked()) != 5 {
		t.Fatalf("after five refusals: %+v %+v, asked %d", now.OnOff, now.LevelControl, len(dimmer.Asked()))
	}

	// Fail: every command returns it, is recorded, and does nothing.
	dimmer.Fail(matter.ErrBusy)

	for name, err := range map[string]error{
		"on":    got.OnOff.Set(ctx, true),
		"level": got.LevelControl.Set(ctx, 0.5, 0),
		"move":  got.LevelControl.Move(ctx, matter.Down),
		"stop":  got.LevelControl.Stop(ctx),
	} {
		if !errors.Is(err, matter.ErrBusy) {
			t.Errorf("%s of a busy dimmer: %v", name, err)
		}
	}

	if now, _ := home.Device("dimmer"); now.OnOff.On || now.LevelControl.Level != 1 || len(dimmer.Asked()) != 9 {
		t.Fatalf("a failing dimmer did something: %+v %+v, asked %d", now.OnOff, now.LevelControl, len(dimmer.Asked()))
	}

	dimmer.Fail(nil)

	if err := got.OnOff.Set(ctx, true); err != nil {
		t.Fatalf("after Fail(nil): %v", err)
	}

	// Unreachable: the home answers, and the fake is not asked.
	if err := home.Report("dimmer", matter.ReachabilityChanged{Unreachable: true}); err != nil {
		t.Fatal(err)
	}

	if err := got.OnOff.Set(ctx, false); !errors.Is(err, matter.ErrUnreachable) || len(dimmer.Asked()) != 10 {
		t.Fatalf("an unreachable dimmer: %v, asked %d", err, len(dimmer.Asked()))
	}
}

// TestFakesNeedAnID: a constructor asked for a device the home refuses
// panics.
func TestFakesNeedAnID(t *testing.T) {
	t.Parallel()

	for name, make := range map[string]func(*matter.Home, matter.ID) *mattertest.Fake{
		"a light": mattertest.Light, "a dimmer": mattertest.Dimmer, "a plug": mattertest.Plug,
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s with no ID was made", name)
				}
			}()

			var home matter.Home

			make(&home, "")
		}()
	}
}
