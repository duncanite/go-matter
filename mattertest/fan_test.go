package mattertest_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/mattertest"
)

// fanned says whether a fan stands as a change has it.
func fanned(device matter.Device, as matter.FanChanged) bool {
	fan := device.FanControl

	return fan != nil && fan.Mode == as.Mode && fan.Speed == as.Speed && fan.Running == as.Running &&
		(fan.Oscillating != nil && *fan.Oscillating) == as.Oscillating && (fan.Sleep != nil && *fan.Sleep) == as.Sleep
}

// TestAFanNearIsAFanFar: a fan and an air purifier, with what each can
// do and as each stands, are the same on the far side; and a fan that
// neither oscillates nor sleeps, and takes any speed, is one that does
// neither there.
func TestAFanNearIsAFanFar(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Fan(&home, "fan")
	mattertest.Purifier(&home, "purifier")

	turning := matter.FanChanged{Mode: matter.FanOn, Speed: 0.3, Running: true, Oscillating: true, Sleep: true}
	if err := home.Report("purifier", turning); err != nil {
		t.Fatal(err)
	}

	setMode := func(context.Context, matter.FanMode) error { return nil }
	setSpeed := func(context.Context, matter.Level) error { return nil }

	if err := home.Put(matter.Device{
		ID: "plain", Name: "plain", Kind: matter.Fan, Room: "Attic",
		FanControl: &matter.FanControl{
			Mode: matter.FanOn, Speed: 0.42, Running: true, SetMode: setMode, SetSpeed: setSpeed,
		},
	}); err != nil {
		t.Fatal(err)
	}

	across := mattertest.Across(t, &home)

	fan := far(t, across, "fan", nil)
	if fan.Kind != matter.Fan || fan.OnOff != nil || fan.LevelControl != nil || fan.FanControl == nil ||
		fan.FanControl.Steps != 10 || fan.FanControl.SetMode == nil || fan.FanControl.SetSpeed == nil ||
		fan.FanControl.SetOscillating == nil || fan.FanControl.SetSleep == nil ||
		!fanned(fan, matter.FanChanged{Mode: matter.FanOff, Speed: 1}) {
		t.Errorf("the fan crossed as %+v, its fan: %+v", fan.Info, fan.FanControl)
	}

	purifier := far(t, across, "purifier", nil)
	if purifier.Kind != matter.AirPurifier || purifier.FanControl == nil || purifier.FanControl.Steps != 10 ||
		!fanned(purifier, turning) {
		t.Errorf("the purifier crossed as %+v, its fan: %+v", purifier.Info, purifier.FanControl)
	}

	plain := far(t, across, "plain", nil)
	if plain.Kind != matter.Fan || plain.Room != "Attic" || plain.FanControl == nil || plain.FanControl.Steps != 0 ||
		plain.FanControl.Oscillating != nil || plain.FanControl.SetOscillating != nil ||
		plain.FanControl.Sleep != nil || plain.FanControl.SetSleep != nil ||
		!fanned(plain, matter.FanChanged{Mode: matter.FanOn, Speed: 0.42, Running: true}) {
		t.Errorf("the plain fan crossed as %+v, its fan: %+v", plain.Info, plain.FanControl)
	}

	// A fan put anew that can do less is one that can do less there.
	if err := home.Put(matter.Device{
		ID: "fan", Name: "fan", Kind: matter.Fan,
		FanControl: &matter.FanControl{Mode: matter.FanOff, Speed: 1, SetMode: setMode, SetSpeed: setSpeed},
	}); err != nil {
		t.Fatal(err)
	}

	far(t, across, "fan", func(now matter.Device) bool {
		return now.FanControl.Steps == 0 && now.FanControl.Oscillating == nil && now.FanControl.Sleep == nil
	})
}

// TestAFansCommandCrosses: what is asked of a fan on the far side is
// what the fan is asked, what it answers is what the far side is
// answered, and what it then does is how it stands there.
func TestAFansCommandCrosses(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()
	fan := mattertest.Fan(&home, "fan")
	across := mattertest.Across(t, &home)
	there := far(t, across, "fan", nil).FanControl

	for _, step := range []struct {
		ask  func() error
		want mattertest.Ask
		then matter.FanChanged
	}{
		{
			func() error { return there.SetMode(ctx, matter.FanOn) },
			mattertest.SetFanMode{Mode: matter.FanOn},
			matter.FanChanged{Mode: matter.FanOn, Speed: 1, Running: true},
		},
		{
			func() error { return there.SetSpeed(ctx, 0.3) },
			mattertest.SetFanSpeed{Speed: 0.3},
			matter.FanChanged{Mode: matter.FanOn, Speed: 0.3, Running: true},
		},
		{
			func() error { return there.SetOscillating(ctx, true) },
			mattertest.SetOscillating{On: true},
			matter.FanChanged{Mode: matter.FanOn, Speed: 0.3, Running: true, Oscillating: true},
		},
		{
			func() error { return there.SetSleep(ctx, true) },
			mattertest.SetSleep{On: true},
			matter.FanChanged{Mode: matter.FanOn, Speed: 0.3, Running: true, Oscillating: true, Sleep: true},
		},
		{
			func() error { return there.SetOscillating(ctx, false) },
			mattertest.SetOscillating{On: false},
			matter.FanChanged{Mode: matter.FanOn, Speed: 0.3, Running: true, Sleep: true},
		},
		{
			func() error { return there.SetSleep(ctx, false) },
			mattertest.SetSleep{On: false},
			matter.FanChanged{Mode: matter.FanOn, Speed: 0.3, Running: true},
		},
		{
			func() error { return there.SetMode(ctx, matter.FanAuto) },
			mattertest.SetFanMode{Mode: matter.FanAuto},
			matter.FanChanged{Mode: matter.FanAuto, Speed: 0.3, Running: true},
		},
		{
			func() error { return there.SetMode(ctx, matter.FanOff) },
			mattertest.SetFanMode{Mode: matter.FanOff},
			matter.FanChanged{Mode: matter.FanOff, Speed: 0.3},
		},
		// A speed sent to a fan that is off brings it on.
		{
			func() error { return there.SetSpeed(ctx, 0.7) },
			mattertest.SetFanSpeed{Speed: 0.7},
			matter.FanChanged{Mode: matter.FanOn, Speed: 0.7, Running: true},
		},
	} {
		before := len(fan.Asked())

		if err := step.ask(); err != nil {
			t.Fatalf("%+v: %v", step.want, err)
		}

		if asked := fan.Asked(); len(asked) != before+1 || !reflect.DeepEqual(asked[before], step.want) {
			t.Fatalf("%+v reached the fan as %+v", step.want, asked[before:])
		}

		far(t, across, "fan", func(now matter.Device) bool { return fanned(now, step.then) })
	}

	// What cannot be said in Matter is refused before it is sent.
	before := len(fan.Asked())

	for name, err := range map[string]error{
		"a mode that is none":     there.SetMode(ctx, 0),
		"a speed of zero":         there.SetSpeed(ctx, 0),
		"a speed above full":      there.SetSpeed(ctx, 1.1),
		"a speed that is not one": there.SetSpeed(ctx, matter.Level(math.NaN())),
	} {
		if !errors.Is(err, matter.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if len(fan.Asked()) != before {
		t.Fatalf("what cannot be said reached the fan: %+v", fan.Asked()[before:])
	}

	// The device's error is the far side's. Each asks for what the fan is
	// not at: what it is at already is asked of no one.
	for name, row := range map[string]struct{ fail, want error }{
		"invalid":     {matter.ErrInvalid, matter.ErrInvalid},
		"busy":        {matter.ErrBusy, matter.ErrBusy},
		"unreachable": {matter.ErrUnreachable, matter.ErrUnreachable},
		"failed":      {matter.ErrFailed, matter.ErrFailed},
		"its own":     {errFuse, matter.ErrFailed},
	} {
		fan.Fail(row.fail)

		for command, err := range map[string]error{
			"mode":      there.SetMode(ctx, matter.FanOff),
			"speed":     there.SetSpeed(ctx, 0.2),
			"oscillate": there.SetOscillating(ctx, true),
			"sleep":     there.SetSleep(ctx, true),
		} {
			if !errors.Is(err, row.want) {
				t.Errorf("%s, %s: %v", name, command, err)
			}
		}
	}
}

// TestAFansSpeedCrosses: a speed sent to a fan that takes any is the
// speed it is asked for, to the percent; one sent to a fan with steps is
// the step itself, which a percent would not say.
func TestAFansSpeedCrosses(t *testing.T) {
	t.Parallel()

	for name, row := range map[string]struct {
		steps int
		speed matter.Level
	}{
		"any speed":   {0, 0.37},
		"three steps": {3, matter.Level(2) / 3},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var (
				home  matter.Home
				asked = make(chan matter.Level, 1)
			)

			if err := home.Put(matter.Device{
				ID: "plain", Name: "plain", Kind: matter.Fan,
				FanControl: &matter.FanControl{
					Mode: matter.FanOff, Speed: 1, Steps: row.steps,
					SetMode: func(context.Context, matter.FanMode) error { return nil },
					SetSpeed: func(_ context.Context, speed matter.Level) error {
						asked <- speed

						return nil
					},
				},
			}); err != nil {
				t.Fatal(err)
			}

			there := far(t, mattertest.Across(t, &home), "plain", nil).FanControl

			if err := there.SetSpeed(t.Context(), row.speed); err != nil {
				t.Fatal(err)
			}

			if speed := <-asked; speed != row.speed {
				t.Fatalf("the fan was asked for the speed %v", speed)
			}
		})
	}
}

// TestWhatAFanDoesCrosses: what happens to a fan near is told to a
// watcher far, whole.
func TestWhatAFanDoesCrosses(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		told heard
	)

	mattertest.Purifier(&home, "purifier")

	across := mattertest.Across(t, &home)
	there := far(t, across, "purifier", nil)

	go func() { _ = across.Watch(t.Context(), told.take) }()

	told.wait(t, "the purifier, as it stands, first", func(event matter.Event) bool {
		added, is := event.Change.(matter.DeviceAdded)

		return is && added.Device.ID == there.ID && added.Device.FanControl != nil
	})

	for _, does := range []matter.FanChanged{
		{Mode: matter.FanOn, Speed: 0.5, Running: true},
		{Mode: matter.FanOn, Speed: 0.5, Running: true, Oscillating: true},
		{Mode: matter.FanOn, Speed: 0.9, Running: true, Oscillating: true, Sleep: true},
		{Mode: matter.FanAuto, Speed: 0.9, Running: false, Oscillating: true, Sleep: true},
		{Mode: matter.FanAuto, Speed: 0.9, Running: true},
		{Mode: matter.FanOff, Speed: 0.9},
	} {
		if err := home.Report("purifier", does); err != nil {
			t.Fatal(err)
		}

		told.wait(t, "the purifier doing as it does", func(event matter.Event) bool {
			return event.Device == there.ID && event.Change == does
		})
	}
}
