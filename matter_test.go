package matter_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
)

func setOn(context.Context, bool) error                           { return nil }
func setLevel(context.Context, matter.Level, time.Duration) error { return nil }
func move(context.Context, matter.Direction) error                { return nil }
func stop(context.Context) error                                  { return nil }
func setMode(context.Context, matter.FanMode) error               { return nil }
func setSpeed(context.Context, matter.Level) error                { return nil }

// fan is a fan that can be served, as a test then has it be.
func fan(kind matter.Kind, change func(*matter.Device)) matter.Device {
	var still, awake bool

	device := matter.Device{ID: "a", Kind: kind, FanControl: &matter.FanControl{
		Mode: matter.FanOff, Speed: 1, Steps: 10, Oscillating: &still, Sleep: &awake,
		SetMode: setMode, SetSpeed: setSpeed, SetOscillating: setOn, SetSleep: setOn,
	}}

	if change != nil {
		change(&device)
	}

	return device
}

func TestValidate(t *testing.T) {
	t.Parallel()

	onOff := &matter.OnOff{Set: setOn}
	level := &matter.LevelControl{Level: 1, Set: setLevel}
	moving := &matter.LevelControl{Level: 0.5, Set: setLevel, Move: move, Stop: stop}
	buttons := []matter.Button{{Label: "On"}}

	for name, row := range map[string]struct {
		device matter.Device
		served bool
	}{
		"a light":    {matter.Device{ID: "a", Kind: matter.Light, OnOff: onOff}, true},
		"a dimmer":   {matter.Device{ID: "a", Kind: matter.Light, OnOff: onOff, LevelControl: level}, true},
		"one moving": {matter.Device{ID: "a", Kind: matter.Light, OnOff: onOff, LevelControl: moving}, true},
		"a plug":     {matter.Device{ID: "a", Kind: matter.Plug, OnOff: onOff}, true},
		"a remote":   {matter.Device{ID: "a", Kind: matter.Remote, Buttons: buttons}, true},
		"unreachable": {
			matter.Device{ID: "a", Kind: matter.Plug, OnOff: onOff, Unreachable: true}, true,
		},

		"a fan":      {fan(matter.Fan, nil), true},
		"a purifier": {fan(matter.AirPurifier, nil), true},
		"a fan of any speed, that only turns": {
			fan(matter.Fan, func(d *matter.Device) {
				d.FanControl.Steps, d.FanControl.Oscillating, d.FanControl.SetOscillating = 0, nil, nil
				d.FanControl.Sleep, d.FanControl.SetSleep = nil, nil
			}), true,
		},
		"a fan of a hundred speeds": {fan(matter.Fan, func(d *matter.Device) { d.FanControl.Steps = 100 }), true},

		"no ID":                    {matter.Device{Kind: matter.Light, OnOff: onOff}, false},
		"no kind":                  {matter.Device{ID: "a", OnOff: onOff}, false},
		"a kind that is not one":   {matter.Device{ID: "a", Kind: 99, OnOff: onOff}, false},
		"a light without OnOff":    {matter.Device{ID: "a", Kind: matter.Light}, false},
		"a level without OnOff":    {matter.Device{ID: "a", Kind: matter.Light, LevelControl: level}, false},
		"an OnOff without Set":     {matter.Device{ID: "a", Kind: matter.Light, OnOff: &matter.OnOff{}}, false},
		"a plug with buttons":      {matter.Device{ID: "a", Kind: matter.Plug, OnOff: onOff, Buttons: buttons}, false},
		"a remote without buttons": {matter.Device{ID: "a", Kind: matter.Remote}, false},
		"a remote that switches":   {matter.Device{ID: "a", Kind: matter.Remote, Buttons: buttons, OnOff: onOff}, false},
		"a remote that dims": {
			matter.Device{ID: "a", Kind: matter.Remote, Buttons: buttons, LevelControl: level}, false,
		},
		"a level without Set": {
			matter.Device{ID: "a", Kind: matter.Light, OnOff: onOff, LevelControl: &matter.LevelControl{Level: 1}}, false,
		},
		"a level of zero": {
			matter.Device{ID: "a", Kind: matter.Light, OnOff: onOff, LevelControl: &matter.LevelControl{Set: setLevel}}, false,
		},
		"a level above full": {
			matter.Device{
				ID: "a", Kind: matter.Light, OnOff: onOff,
				LevelControl: &matter.LevelControl{Level: 1.01, Set: setLevel},
			}, false,
		},
		"a level that is not a number": {
			matter.Device{
				ID: "a", Kind: matter.Light, OnOff: onOff,
				LevelControl: &matter.LevelControl{Level: matter.Level(math.NaN()), Set: setLevel},
			}, false,
		},
		"Move without Stop": {
			matter.Device{
				ID: "a", Kind: matter.Light, OnOff: onOff,
				LevelControl: &matter.LevelControl{Level: 1, Set: setLevel, Move: move},
			}, false,
		},
		"Stop without Move": {
			matter.Device{
				ID: "a", Kind: matter.Light, OnOff: onOff,
				LevelControl: &matter.LevelControl{Level: 1, Set: setLevel, Stop: stop},
			}, false,
		},
		"a fan without FanControl": {matter.Device{ID: "a", Kind: matter.Fan}, false},
		"a purifier without one":   {matter.Device{ID: "a", Kind: matter.AirPurifier}, false},
		"a fan that switches":      {fan(matter.Fan, func(d *matter.Device) { d.OnOff = onOff }), false},
		"a fan that dims":          {fan(matter.Fan, func(d *matter.Device) { d.LevelControl = level }), false},
		"a fan with buttons":       {fan(matter.Fan, func(d *matter.Device) { d.Buttons = buttons }), false},
		"a light with a fan": {
			fan(matter.Light, func(d *matter.Device) { d.OnOff = onOff }), false,
		},
		"a remote with a fan": {
			fan(matter.Remote, func(d *matter.Device) { d.Buttons = buttons }), false,
		},
		"a fan without SetMode":  {fan(matter.Fan, func(d *matter.Device) { d.FanControl.SetMode = nil }), false},
		"a fan without SetSpeed": {fan(matter.Fan, func(d *matter.Device) { d.FanControl.SetSpeed = nil }), false},
		"a fan in no mode":       {fan(matter.Fan, func(d *matter.Device) { d.FanControl.Mode = 0 }), false},
		"a fan in a mode that is not one": {
			fan(matter.Fan, func(d *matter.Device) { d.FanControl.Mode = matter.FanAuto + 1 }), false,
		},
		"a fan speed of zero":    {fan(matter.Fan, func(d *matter.Device) { d.FanControl.Speed = 0 }), false},
		"a fan speed above full": {fan(matter.Fan, func(d *matter.Device) { d.FanControl.Speed = 1.01 }), false},
		"a fan speed that is not a number": {
			fan(matter.Fan, func(d *matter.Device) { d.FanControl.Speed = matter.Level(math.NaN()) }), false,
		},
		"fan speeds below none":      {fan(matter.Fan, func(d *matter.Device) { d.FanControl.Steps = -1 }), false},
		"fan speeds above a hundred": {fan(matter.Fan, func(d *matter.Device) { d.FanControl.Steps = 101 }), false},
		"Oscillating without SetOscillating": {
			fan(matter.Fan, func(d *matter.Device) { d.FanControl.SetOscillating = nil }), false,
		},
		"SetOscillating without Oscillating": {
			fan(matter.Fan, func(d *matter.Device) { d.FanControl.Oscillating = nil }), false,
		},
		"Sleep without SetSleep": {fan(matter.Fan, func(d *matter.Device) { d.FanControl.SetSleep = nil }), false},
		"SetSleep without Sleep": {fan(matter.Fan, func(d *matter.Device) { d.FanControl.Sleep = nil }), false},
	} {
		err := row.device.Validate()
		if row.served && err != nil {
			t.Errorf("%s: %v", name, err)
		}

		if !row.served && !errors.Is(err, matter.ErrDescription) {
			t.Errorf("%s: served, or refused otherwise: %v", name, err)
		}
	}
}
