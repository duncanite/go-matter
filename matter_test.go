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
