package matter_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/duncanite/go-matter"
)

// aired is a purifier with everything one may have beside its fan,
// changed as a test has it.
func aired(w *wired, change func(*matter.Device)) matter.Device {
	device := fan(matter.AirPurifier, nil)
	device.ID, device.Name = "purifier", "purifier"
	device.Temperature = &matter.Temperature{Celsius: new(21.5)}
	device.Humidity = &matter.Humidity{Percent: new(40.0)}
	device.AirQuality = &matter.AirQuality{
		Quality: matter.QualityGood,
		PM25:    &matter.Particulates{Micrograms: new(8.0)},
		PM10:    &matter.Particulates{},
	}
	device.Filters = []matter.Filter{{Kind: matter.HEPA, Left: new(0.8)}, {Kind: matter.Carbon}}
	device.Heater = &matter.Heater{
		Target: 20, Least: 1, Most: 37,
		SetOn:     func(_ context.Context, on bool) error { return w.note("heat %t", on) },
		SetTarget: func(_ context.Context, celsius float64) error { return w.note("target %v", celsius) },
	}

	if change != nil {
		change(&device)
	}

	return device
}

// TestValidateAir: what an air purifier may have beside its fan is
// served as it is described, and refused when it cannot be.
func TestValidateAir(t *testing.T) {
	t.Parallel()

	var w wired

	for name, row := range map[string]struct {
		change func(*matter.Device)
		served bool
	}{
		"a purifier with everything": {nil, true},
		"one that reads nothing for now": {func(d *matter.Device) {
			d.Temperature.Celsius, d.Humidity.Percent, d.AirQuality.PM25.Micrograms = nil, nil, nil
			d.AirQuality.Quality = matter.QualityUnknown
		}, true},
		"one with a temperature alone": {func(d *matter.Device) {
			d.Humidity, d.AirQuality, d.Filters, d.Heater = nil, nil, nil, nil
		}, true},
		"the coldest and the driest": {func(d *matter.Device) {
			d.Temperature.Celsius, d.Humidity.Percent = new(-273.15), new(0.0)
		}, true},
		"a filter with nothing left, to be replaced": {func(d *matter.Device) {
			d.Filters[0].Left, d.Filters[0].Replace = new(0.0), true
		}, true},
		"a heater at an end of its range": {func(d *matter.Device) { d.Heater.Target = 37 }, true},

		"a fan with a temperature": {func(d *matter.Device) {
			d.Kind, d.Humidity, d.AirQuality, d.Filters, d.Heater = matter.Fan, nil, nil, nil, nil
		}, false},
		"a fan with filters": {func(d *matter.Device) {
			d.Kind, d.Temperature, d.Humidity, d.AirQuality, d.Heater = matter.Fan, nil, nil, nil, nil
		}, false},
		"a fan with a heater": {func(d *matter.Device) {
			d.Kind, d.Temperature, d.Humidity, d.AirQuality, d.Filters = matter.Fan, nil, nil, nil, nil
		}, false},
		"a temperature below any":        {func(d *matter.Device) { d.Temperature.Celsius = new(-300.0) }, false},
		"a temperature above Matter":     {func(d *matter.Device) { d.Temperature.Celsius = new(400.0) }, false},
		"a temperature that is none":     {func(d *matter.Device) { d.Temperature.Celsius = new(math.NaN()) }, false},
		"a humidity below zero":          {func(d *matter.Device) { d.Humidity.Percent = new(-1.0) }, false},
		"a humidity above all":           {func(d *matter.Device) { d.Humidity.Percent = new(100.5) }, false},
		"a quality that is none":         {func(d *matter.Device) { d.AirQuality.Quality = 7 }, false},
		"a quality below unknown":        {func(d *matter.Device) { d.AirQuality.Quality = -1 }, false},
		"particulates below zero":        {func(d *matter.Device) { d.AirQuality.PM25.Micrograms = new(-1.0) }, false},
		"coarse ones that are none":      {func(d *matter.Device) { d.AirQuality.PM10.Micrograms = new(math.NaN()) }, false},
		"a filter of no kind":            {func(d *matter.Device) { d.Filters[1].Kind = 0 }, false},
		"two filters of a kind":          {func(d *matter.Device) { d.Filters[1].Kind = matter.HEPA }, false},
		"a filter with more than all":    {func(d *matter.Device) { d.Filters[0].Left = new(1.1) }, false},
		"a filter with less than none":   {func(d *matter.Device) { d.Filters[0].Left = new(-0.1) }, false},
		"a heater that cannot be set":    {func(d *matter.Device) { d.Heater.SetOn = nil }, false},
		"a heater with no target to set": {func(d *matter.Device) { d.Heater.SetTarget = nil }, false},
		"a heater with no range":         {func(d *matter.Device) { d.Heater.Least = 37 }, false},
		"a heater above Matter":          {func(d *matter.Device) { d.Heater.Most = 400 }, false},
		"a heater below any":             {func(d *matter.Device) { d.Heater.Least = -300 }, false},
		"a target outside its range":     {func(d *matter.Device) { d.Heater.Target = 40 }, false},
		"a target that is none":          {func(d *matter.Device) { d.Heater.Target = math.NaN() }, false},
	} {
		device := aired(&w, row.change)

		err := device.Validate()
		if row.served && err != nil {
			t.Errorf("%s: %v", name, err)
		}

		if !row.served && !errors.Is(err, matter.ErrDescription) {
			t.Errorf("%s: served, or refused otherwise: %v", name, err)
		}
	}

	// What is no air purifier has none of it.
	for name, device := range map[string]matter.Device{
		"a light": {ID: "a", Kind: matter.Light, OnOff: &matter.OnOff{Set: setOn}, Humidity: &matter.Humidity{}},
		"a remote": {
			ID: "a", Kind: matter.Remote, Buttons: []matter.Button{{Label: "On"}},
			Temperature: &matter.Temperature{},
		},
	} {
		if err := device.Validate(); !errors.Is(err, matter.ErrDescription) {
			t.Errorf("%s with what a purifier has: %v", name, err)
		}
	}
}

// TestHomeHoldsAPurifiersAir: what a purifier reads, its filters and its
// heater are the home's own copy, follow what is reported, are told when
// they move and only then, and its heater is asked through the home.
func TestHomeHoldsAPurifiersAir(t *testing.T) {
	t.Parallel()

	var (
		home matter.Home
		w    wired
	)

	ctx := t.Context()
	put := aired(&w, nil)

	if err := home.Put(put); err != nil {
		t.Fatal(err)
	}

	// The home took its own copy, and hands out the caller's own.
	*put.Temperature.Celsius, *put.Filters[0].Left, put.Heater.Target = 99, 0.1, 30
	*put.AirQuality.PM25.Micrograms = 500

	held, _ := home.Device("purifier")
	*held.Humidity.Percent, held.Filters[1].Replace, held.AirQuality.Quality = 1, true, matter.QualityPoor

	again, _ := home.Device("purifier")
	if *again.Temperature.Celsius != 21.5 || *again.Filters[0].Left != 0.8 || again.Heater.Target != 20 ||
		*again.AirQuality.PM25.Micrograms != 8 || *again.Humidity.Percent != 40 || again.Filters[1].Replace ||
		again.AirQuality.Quality != matter.QualityGood {
		t.Fatalf("the home's purifier followed a copy: %+v", again)
	}

	told, cancel, _ := watch(t, &home)
	defer cancel()

	if first := next(t, told); first != "purifier added" {
		t.Fatalf("a watcher's first look: %s", first)
	}

	// What changes is told; what does not is not.
	for _, step := range []struct {
		change matter.Change
		told   bool
	}{
		{matter.TemperatureChanged{Celsius: new(21.5)}, false},
		{matter.TemperatureChanged{Celsius: new(22.0)}, true},
		{matter.TemperatureChanged{}, true},
		{matter.TemperatureChanged{}, false},
		{matter.HumidityChanged{Percent: new(40.0)}, false},
		{matter.HumidityChanged{Percent: new(55.5)}, true},
		{matter.HumidityChanged{}, true},
		{matter.AirQualityChanged{Quality: matter.QualityGood, PM25: new(8.0)}, false},
		{matter.AirQualityChanged{Quality: matter.QualityFair, PM25: new(8.0)}, true},
		{matter.AirQualityChanged{Quality: matter.QualityFair, PM25: new(30.0), PM10: new(41.0)}, true},
		{matter.AirQualityChanged{Quality: matter.QualityFair, PM25: new(30.0), PM10: new(41.0)}, false},
		{matter.AirQualityChanged{Quality: matter.QualityFair}, true},
		{matter.FilterChanged{Filter: 0, Left: new(0.8)}, false},
		{matter.FilterChanged{Filter: 0, Left: new(0.05), Replace: true}, true},
		{matter.FilterChanged{Filter: 1, Replace: true}, true},
		{matter.FilterChanged{Filter: 1, Replace: true}, false},
		{matter.HeaterChanged{Target: 20}, false},
		{matter.HeaterChanged{On: true, Heating: true, Target: 20}, true},
		{matter.HeaterChanged{On: true, Target: 20}, true},
		{matter.HeaterChanged{On: true, Target: 23.5}, true},
		{matter.HeaterChanged{On: true, Target: 23.5}, false},
	} {
		if err := home.Report("purifier", step.change); err != nil {
			t.Fatalf("%#v: %v", step.change, err)
		}

		if step.told {
			next(t, told)
		} else {
			quiet(t, told)
		}
	}

	now, _ := home.Device("purifier")
	if now.Temperature.Celsius != nil || now.Humidity.Percent != nil || now.AirQuality.Quality != matter.QualityFair ||
		now.AirQuality.PM25.Micrograms != nil || now.AirQuality.PM10.Micrograms != nil ||
		*now.Filters[0].Left != 0.05 || !now.Filters[0].Replace || !now.Filters[1].Replace ||
		!now.Heater.On || now.Heater.Heating || now.Heater.Target != 23.5 {
		t.Fatalf("the purifier's state did not follow: %+v, %+v, %+v", now.AirQuality, now.Filters, now.Heater)
	}

	// What the home keeps of a change is its own.
	reading := new(19.0)
	if err := home.Report("purifier", matter.TemperatureChanged{Celsius: reading}); err != nil {
		t.Fatal(err)
	}

	*reading = 90

	if kept, _ := home.Device("purifier"); *kept.Temperature.Celsius != 19 {
		t.Fatal("the home followed the reading a change came with")
	}

	next(t, told)

	// What a device cannot have is refused, and told to no one.
	plain := fan(matter.AirPurifier, nil)
	plain.ID = "plain"

	if err := home.Put(plain); err != nil {
		t.Fatal(err)
	}

	next(t, told)

	for name, row := range map[string]struct {
		device matter.ID
		change matter.Change
	}{
		"a temperature for one that reads none":   {"plain", matter.TemperatureChanged{Celsius: new(20.0)}},
		"a humidity for one that reads none":      {"plain", matter.HumidityChanged{Percent: new(20.0)}},
		"an air quality for one that reads none":  {"plain", matter.AirQualityChanged{}},
		"a filter for one that has none":          {"plain", matter.FilterChanged{}},
		"a heater for one that has none":          {"plain", matter.HeaterChanged{Target: 20}},
		"a temperature Matter has no number for":  {"purifier", matter.TemperatureChanged{Celsius: new(500.0)}},
		"a humidity above all":                    {"purifier", matter.HumidityChanged{Percent: new(101.0)}},
		"a quality that is none":                  {"purifier", matter.AirQualityChanged{Quality: 9}},
		"particulates below zero":                 {"purifier", matter.AirQualityChanged{PM25: new(-2.0)}},
		"a filter it lacks":                       {"purifier", matter.FilterChanged{Filter: 2}},
		"a filter before the first":               {"purifier", matter.FilterChanged{Filter: -1}},
		"a life left for a filter that says none": {"purifier", matter.FilterChanged{Filter: 1, Left: new(0.5)}},
		"none for a filter that says one":         {"purifier", matter.FilterChanged{Filter: 0}},
		"a life left above all":                   {"purifier", matter.FilterChanged{Filter: 0, Left: new(2.0)}},
		"a target outside the heater's range":     {"purifier", matter.HeaterChanged{Target: 38}},
		"a target that is none":                   {"purifier", matter.HeaterChanged{Target: math.NaN()}},
	} {
		if err := home.Report(row.device, row.change); !errors.Is(err, matter.ErrDescription) {
			t.Errorf("%s: %v", name, err)
		}
	}

	quiet(t, told)

	// Particulates a device does not count are refused too.
	onlyFine := aired(&w, func(d *matter.Device) { d.ID, d.AirQuality.PM10 = "fine", nil })
	if err := home.Put(onlyFine); err != nil {
		t.Fatal(err)
	}

	next(t, told)

	err := home.Report("fine", matter.AirQualityChanged{Quality: matter.QualityGood, PM10: new(3.0)})
	if !errors.Is(err, matter.ErrDescription) {
		t.Errorf("coarse particulates for a device that counts none: %v", err)
	}

	// The heater is asked through the home, and no more once the device
	// is unreachable, or another.
	heater := now.Heater

	if err = errors.Join(heater.SetOn(ctx, true), heater.SetTarget(ctx, 25)); err != nil {
		t.Fatal(err)
	}

	if got := w.got(); len(got) != 2 || got[0] != "heat true" || got[1] != "target 25" {
		t.Fatalf("the heater was asked %v", got)
	}

	if err = home.Report("purifier", matter.ReachabilityChanged{Unreachable: true}); err != nil {
		t.Fatal(err)
	}

	if err = heater.SetOn(ctx, false); !errors.Is(err, matter.ErrUnreachable) {
		t.Errorf("a heater unreachable: %v", err)
	}

	if err = home.Put(aired(&w, nil)); err != nil {
		t.Fatal(err)
	}

	if err = heater.SetTarget(ctx, 21); !errors.Is(err, matter.ErrNoDevice) {
		t.Errorf("the heater of a device replaced: %v", err)
	}

	if got := w.got(); len(got) != 2 {
		t.Fatalf("a heater that was not to be asked was: %v", got)
	}
}
