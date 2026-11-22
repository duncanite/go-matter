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

// reads says whether a reading is what is expected of it: none, or that
// one, to what Matter keeps of it.
func reads(reading, want *float64) bool {
	return (reading == nil) == (want == nil) && (reading == nil || math.Abs(*reading-*want) < 1e-6)
}

// TestAPurifiersAirNearIsItsAirFar: what an air purifier reads, its
// filters and its heater are the same on the far side; a purifier that
// has none of them has none there; and one that has some has those.
func TestAPurifiersAirNearIsItsAirFar(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Heater(&home, "whole")
	mattertest.Purifier(&home, "plain")

	setMode := func(context.Context, matter.FanMode) error { return nil }
	setSpeed := func(context.Context, matter.Level) error { return nil }

	if err := home.Put(matter.Device{
		ID: "some", Name: "some", Kind: matter.AirPurifier,
		FanControl:  &matter.FanControl{Mode: matter.FanOff, Speed: 1, SetMode: setMode, SetSpeed: setSpeed},
		Temperature: &matter.Temperature{},
		AirQuality:  &matter.AirQuality{PM10: &matter.Particulates{Micrograms: new(3.5)}},
		Filters:     []matter.Filter{{Kind: matter.Carbon, Left: new(0.0), Replace: true}},
	}); err != nil {
		t.Fatal(err)
	}

	across := mattertest.Across(t, &home)

	whole := far(t, across, "whole", nil)
	if whole.Kind != matter.AirPurifier || whole.FanControl == nil ||
		whole.Temperature == nil || !reads(whole.Temperature.Celsius, new(21.5)) ||
		whole.Humidity == nil || !reads(whole.Humidity.Percent, new(40.0)) ||
		whole.AirQuality == nil || whole.AirQuality.Quality != matter.QualityGood ||
		whole.AirQuality.PM25 == nil || !reads(whole.AirQuality.PM25.Micrograms, new(8.0)) ||
		whole.AirQuality.PM10 == nil || !reads(whole.AirQuality.PM10.Micrograms, new(12.0)) {
		t.Errorf(
			"what the purifier reads crossed as %+v, %+v, %+v",
			whole.Temperature,
			whole.Humidity,
			whole.AirQuality,
		)
	}

	if len(whole.Filters) != 2 || whole.Filters[0].Kind != matter.HEPA || !reads(whole.Filters[0].Left, new(0.8)) ||
		whole.Filters[0].Replace || whole.Filters[1].Kind != matter.Carbon || whole.Filters[1].Left != nil ||
		whole.Filters[1].Replace {
		t.Errorf("the purifier's filters crossed as %+v", whole.Filters)
	}

	if heats := whole.Heater; heats == nil || heats.On || heats.Heating || heats.Target != 20 ||
		heats.Least != 1 || heats.Most != 37 || heats.SetOn == nil || heats.SetTarget == nil {
		t.Errorf("the purifier's heater crossed as %+v", whole.Heater)
	}

	plain := far(t, across, "plain", nil)
	if plain.Temperature != nil || plain.Humidity != nil || plain.AirQuality != nil || len(plain.Filters) != 0 ||
		plain.Heater != nil {
		t.Errorf("a purifier with a fan alone crossed with %+v", plain)
	}

	some := far(t, across, "some", nil)
	if some.Temperature == nil || some.Temperature.Celsius != nil || some.Humidity != nil || some.Heater != nil ||
		some.AirQuality == nil || some.AirQuality.Quality != matter.QualityUnknown || some.AirQuality.PM25 != nil ||
		some.AirQuality.PM10 == nil || !reads(some.AirQuality.PM10.Micrograms, new(3.5)) ||
		len(some.Filters) != 1 || some.Filters[0].Kind != matter.Carbon || !reads(some.Filters[0].Left, new(0.0)) ||
		!some.Filters[0].Replace {
		t.Errorf("a purifier with some of it crossed as %+v, %+v, %+v", some.Temperature, some.AirQuality, some.Filters)
	}

	// A purifier put anew with less is one with less there.
	if err := home.Put(matter.Device{
		ID: "some", Name: "some", Kind: matter.AirPurifier,
		FanControl:  &matter.FanControl{Mode: matter.FanOff, Speed: 1, SetMode: setMode, SetSpeed: setSpeed},
		Temperature: &matter.Temperature{},
	}); err != nil {
		t.Fatal(err)
	}

	far(t, across, "some", func(now matter.Device) bool {
		return now.Temperature != nil && now.AirQuality == nil && len(now.Filters) == 0
	})
}

// TestWhatAPurifierReadsCrosses: what a purifier comes to read, a filter
// wearing and a heater moving are how it stands on the far side, to and
// from a reading that is not known.
func TestWhatAPurifierReadsCrosses(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Heater(&home, "purifier")

	across := mattertest.Across(t, &home)
	far(t, across, "purifier", nil)

	for _, step := range []struct {
		change matter.Change
		holds  func(now matter.Device) bool
	}{
		{matter.TemperatureChanged{Celsius: new(-3.25)}, func(now matter.Device) bool {
			return reads(now.Temperature.Celsius, new(-3.25))
		}},
		{matter.TemperatureChanged{}, func(now matter.Device) bool { return now.Temperature.Celsius == nil }},
		{matter.TemperatureChanged{Celsius: new(24.0)}, func(now matter.Device) bool {
			return reads(now.Temperature.Celsius, new(24.0))
		}},
		{matter.HumidityChanged{Percent: new(62.5)}, func(now matter.Device) bool {
			return reads(now.Humidity.Percent, new(62.5))
		}},
		{matter.HumidityChanged{}, func(now matter.Device) bool { return now.Humidity.Percent == nil }},
		{matter.HumidityChanged{Percent: new(100.0)}, func(now matter.Device) bool {
			return reads(now.Humidity.Percent, new(100.0))
		}},
		{
			matter.AirQualityChanged{Quality: matter.QualityVeryPoor, PM25: new(120.0), PM10: new(180.5)},
			func(now matter.Device) bool {
				return now.AirQuality.Quality == matter.QualityVeryPoor &&
					reads(now.AirQuality.PM25.Micrograms, new(120.0)) && reads(now.AirQuality.PM10.Micrograms, new(180.5))
			},
		},
		{matter.AirQualityChanged{Quality: matter.QualityUnknown, PM10: new(2.0)}, func(now matter.Device) bool {
			return now.AirQuality.Quality == matter.QualityUnknown && now.AirQuality.PM25.Micrograms == nil &&
				reads(now.AirQuality.PM10.Micrograms, new(2.0))
		}},
		{matter.AirQualityChanged{Quality: matter.QualityExtremelyPoor, PM25: new(0.0)}, func(now matter.Device) bool {
			return now.AirQuality.Quality == matter.QualityExtremelyPoor &&
				reads(now.AirQuality.PM25.Micrograms, new(0.0)) && now.AirQuality.PM10.Micrograms == nil
		}},
		{matter.FilterChanged{Filter: 0, Left: new(0.03), Replace: true}, func(now matter.Device) bool {
			return reads(now.Filters[0].Left, new(0.03)) && now.Filters[0].Replace && !now.Filters[1].Replace
		}},
		{matter.FilterChanged{Filter: 1, Replace: true}, func(now matter.Device) bool {
			return now.Filters[1].Replace && now.Filters[1].Left == nil
		}},
		{matter.FilterChanged{Filter: 0, Left: new(1.0)}, func(now matter.Device) bool {
			return reads(now.Filters[0].Left, new(1.0)) && !now.Filters[0].Replace && now.Filters[1].Replace
		}},
		{matter.HeaterChanged{On: true, Heating: true, Target: 20}, func(now matter.Device) bool {
			return now.Heater.On && now.Heater.Heating && now.Heater.Target == 20
		}},
		{matter.HeaterChanged{On: true, Target: 18.5}, func(now matter.Device) bool {
			return now.Heater.On && !now.Heater.Heating && now.Heater.Target == 18.5
		}},
		{matter.HeaterChanged{Target: 37}, func(now matter.Device) bool {
			return !now.Heater.On && !now.Heater.Heating && now.Heater.Target == 37
		}},
	} {
		if err := home.Report("purifier", step.change); err != nil {
			t.Fatalf("%#v: %v", step.change, err)
		}

		far(t, across, "purifier", step.holds)
	}
}

// TestAHeatersCommandCrosses: what is asked of a heater on the far side
// is what the heater is asked, what it answers is what the far side is
// answered, and what it then does is how it stands there.
func TestAHeatersCommandCrosses(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()
	heater := mattertest.Heater(&home, "purifier")
	across := mattertest.Across(t, &home)
	there := far(t, across, "purifier", nil).Heater

	for _, step := range []struct {
		ask  func() error
		want mattertest.Ask
		then func(now *matter.Heater) bool
	}{
		{
			func() error { return there.SetOn(ctx, true) },
			mattertest.SetHeat{On: true},
			func(now *matter.Heater) bool { return now.On && now.Heating && now.Target == 20 },
		},
		{
			func() error { return there.SetTarget(ctx, 23.5) },
			mattertest.SetHeatTarget{Celsius: 23.5},
			func(now *matter.Heater) bool { return now.On && now.Heating && now.Target == 23.5 },
		},
		{
			func() error { return there.SetOn(ctx, false) },
			mattertest.SetHeat{On: false},
			func(now *matter.Heater) bool { return !now.On && !now.Heating && now.Target == 23.5 },
		},
		{
			func() error { return there.SetTarget(ctx, 1) },
			mattertest.SetHeatTarget{Celsius: 1},
			func(now *matter.Heater) bool { return !now.On && now.Target == 1 },
		},
	} {
		before := len(heater.Asked())

		if err := step.ask(); err != nil {
			t.Fatalf("%+v: %v", step.want, err)
		}

		if asked := heater.Asked(); len(asked) != before+1 || !reflect.DeepEqual(asked[before], step.want) {
			t.Fatalf("%+v reached the heater as %+v", step.want, asked[before:])
		}

		far(t, across, "purifier", func(now matter.Device) bool { return step.then(now.Heater) })
	}

	// A target the heater does not take is refused before it is sent.
	before := len(heater.Asked())

	for name, err := range map[string]error{
		"a target below the least": there.SetTarget(ctx, 0.5),
		"a target above the most":  there.SetTarget(ctx, 37.5),
		"a target that is none":    there.SetTarget(ctx, math.NaN()),
	} {
		if !errors.Is(err, matter.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if len(heater.Asked()) != before {
		t.Fatalf("what cannot be asked reached the heater: %+v", heater.Asked()[before:])
	}

	// The device's error is the far side's.
	for name, row := range map[string]struct{ fail, want error }{
		"invalid":     {matter.ErrInvalid, matter.ErrInvalid},
		"busy":        {matter.ErrBusy, matter.ErrBusy},
		"unreachable": {matter.ErrUnreachable, matter.ErrUnreachable},
		"failed":      {matter.ErrFailed, matter.ErrFailed},
		"its own":     {errFuse, matter.ErrFailed},
	} {
		heater.Fail(row.fail)

		for command, err := range map[string]error{
			"heat":   there.SetOn(ctx, true),
			"target": there.SetTarget(ctx, 30),
		} {
			if !errors.Is(err, row.want) {
				t.Errorf("%s, %s: %v", name, command, err)
			}
		}
	}

	// What the heater refused is not how it stands there, once what
	// came after has crossed.
	heater.Fail(nil)

	if err := home.Report("purifier", matter.TemperatureChanged{Celsius: new(5.0)}); err != nil {
		t.Fatal(err)
	}

	after := far(
		t,
		across,
		"purifier",
		func(now matter.Device) bool { return reads(now.Temperature.Celsius, new(5.0)) },
	)
	if now := after.Heater; now.On || now.Target != 1 {
		t.Fatalf("what the heater refused is how it stands on the far side: %+v", now)
	}
}
