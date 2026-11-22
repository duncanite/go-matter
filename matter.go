// Package matter is the vocabulary of a home's devices, shaped after
// Matter's own: what a device is, how it stands, what can be asked of it,
// and what happens to it. The same vocabulary is spoken on both sides of
// the protocol. Package bridge serves a Source of devices as Matter
// devices; package controller commissions Matter nodes and gives each
// back as a Source. Nothing here knows which side it is on, and nothing
// here names an endpoint, a cluster or an attribute.
//
// A device's state is data and its commands are functions: a Device is a
// value that says how the device stood when it was read, and carries what
// can be asked of it. What changes afterwards comes as events: a command
// returns when the device has taken the request, not when it has done
// it, and what it then does is an event like any other. A Source
// is where devices are read and watched; Home is the Source to write
// them into, and the one implementation of a Source's promises that a
// maker's adapter, a test and this module all share.
package matter

import (
	"context"
	"fmt"
	"time"
)

// ID names a device within its Source. It is the Source's to choose,
// stable for as long as the device is the same device, and opaque to
// everything else.
type ID string

// Kind is what a device is to a person. It decides how a controller
// shows it.
type Kind int

// The kinds.
const (
	// Light is a light, or the switch or dimmer wired to one.
	Light Kind = iota + 1
	// Plug is an outlet that switches whatever is plugged into it.
	Plug
	// Remote is a set of buttons that controls nothing by itself.
	Remote
	// Fan is a fan.
	Fan
	// AirPurifier is a fan that cleans the air it moves.
	AirPurifier
)

// Info describes a device: what does not change while it runs.
type Info struct {
	ID   ID
	Name string
	Kind Kind

	// Room is where its maker's hub says it is; empty when it does not.
	Room string

	// What the maker says of it; any may be empty.
	Vendor   string
	Model    string
	Serial   string
	Firmware string
}

// Device is one device: its description, how it stood when it was read,
// and what can be asked of it. A capability the device lacks is nil, or
// for Buttons empty. It is a value: what a Source hands out is the
// caller's own copy, which nothing changes behind its back.
type Device struct {
	Info

	// Unreachable is the source having lost the device. Its state is then
	// the last that was known, and its commands answer ErrUnreachable
	// without the device being tried: that is the Source's to see to,
	// not the command's own.
	Unreachable bool

	OnOff        *OnOff
	LevelControl *LevelControl
	FanControl   *FanControl
	Buttons      []Button

	// What an AirPurifier may have beside its fan: what it reads of the
	// air it moves, the filters it moves it through, and a heater.
	Temperature *Temperature
	Humidity    *Humidity
	AirQuality  *AirQuality
	Filters     []Filter
	Heater      *Heater
}

// OnOff is a device that is on or off.
type OnOff struct {
	// On is whether the device is on.
	On bool

	// Set switches it. Switching on a device with a level returns it to
	// the level it last had.
	Set func(ctx context.Context, on bool) error
}

// LevelControl is a device with a level: a dimmer's brightness.
type LevelControl struct {
	// Level is where the device stands, or where it is heading during a
	// transition. A device that is off keeps the level it comes back to.
	Level Level

	// Set brings the device on at level, over the duration given; zero is
	// at once. A duration the device cannot keep is ErrInvalid, never
	// rounded to one it can.
	Set func(ctx context.Context, level Level, over time.Duration) error

	// Move starts the level rising or falling at the device's own pace,
	// until Stop or an end of its range, and Stop holds it where it is: a
	// button held, and let go. Both are nil for a device that cannot, and
	// neither is set without the other.
	Move func(ctx context.Context, direction Direction) error
	Stop func(ctx context.Context) error
}

// Level is a fraction of full: above 0, up to 1. Off is not a level; it
// is OnOff's.
type Level float64

// Direction is which way a level moves.
type Direction int

// The directions.
const (
	Up Direction = iota + 1
	Down
)

// FanControl is a device that moves air: a Fan's, or an AirPurifier's.
type FanControl struct {
	// Mode is what the fan is set to do.
	Mode FanMode
	// Speed is the speed the fan is set to, as a fraction of its fastest.
	// A fan that is off, or that chooses its own, keeps the speed it
	// comes back to.
	Speed Level
	// Running is whether the fan turns now. One left to itself may not.
	Running bool
	// Steps is how many speeds the fan has, and zero for one that takes
	// any. It does not change while the device runs.
	Steps int
	// Oscillating is whether the fan turns from side to side, and Sleep
	// whether it runs in its quiet mode, for the night. Each is nil for a
	// fan that cannot.
	Oscillating *bool
	Sleep       *bool

	// SetMode switches the fan off, on at the speed it has, or leaves it
	// to itself. A mode the fan does not have is ErrInvalid.
	SetMode func(ctx context.Context, mode FanMode) error
	// SetSpeed brings the fan on at a speed. A fan with steps takes the
	// nearest of them.
	SetSpeed func(ctx context.Context, speed Level) error
	// SetOscillating and SetSleep set what Oscillating and Sleep say.
	// Each is nil exactly when its state is.
	SetOscillating func(ctx context.Context, on bool) error
	SetSleep       func(ctx context.Context, on bool) error
}

// FanMode is what a fan is set to do.
type FanMode int

// The modes.
const (
	// FanOff is the fan off.
	FanOff FanMode = iota + 1
	// FanOn is the fan on, at the speed it is set to.
	FanOn
	// FanAuto is the fan left to itself: on when it finds it has to be,
	// at the speed it chooses.
	FanAuto
)

// maxSteps is the most speeds a fan has: Matter counts them in a byte,
// and lets them be no more than a hundred.
const maxSteps = 100

// Temperature is a device that reads how warm the air is.
type Temperature struct {
	// Celsius is what it reads; nil while it reads nothing, a sensor
	// that is off or starting.
	Celsius *float64
}

// Humidity is a device that reads how damp the air is.
type Humidity struct {
	// Percent is the relative humidity it reads, 0 to 100; nil while it
	// reads nothing.
	Percent *float64
}

// AirQuality is a device that reads how clean the air is.
type AirQuality struct {
	// Quality is what the device makes of the air, on Matter's scale.
	Quality Quality
	// PM25 and PM10 are the fine and the coarse particles it counts.
	// Each is nil for a device that does not count them, which does not
	// change while the device runs. Nothing here is a number with no
	// unit: an index of its maker's own is not a reading.
	PM25 *Particulates
	PM10 *Particulates
}

// Particulates is particles in the air, by weight.
type Particulates struct {
	// Micrograms is how much a cubic metre of air holds; nil while the
	// device reads nothing.
	Micrograms *float64
}

// Quality is how clean the air is: Matter's scale, from a device that
// cannot say to air that is not to be breathed.
type Quality int

// The qualities. The zero Quality is one that is not known.
const (
	QualityUnknown Quality = iota
	QualityGood
	QualityFair
	QualityModerate
	QualityPoor
	QualityVeryPoor
	QualityExtremelyPoor
)

// Filter is one filter of an AirPurifier. Its position in Device.Filters
// is what FilterChanged names.
type Filter struct {
	// Kind is what the filter is; a device has one of a kind at most.
	Kind FilterKind
	// Left is how much of the filter's life is left, 0 to 1. It is nil
	// for a filter that does not say, which does not change while the
	// device runs.
	Left *float64
	// Replace is the filter being due to be replaced.
	Replace bool
}

// FilterKind is what a filter is made to take out of the air.
type FilterKind int

// The kinds of filter.
const (
	// HEPA takes out particles.
	HEPA FilterKind = iota + 1
	// Carbon, activated, takes out gases.
	Carbon
)

// Heater is a device that warms the air it moves, up to a temperature.
type Heater struct {
	// On is whether it is set to heat, and Heating whether it heats now:
	// set to heat, it stops when the room is at its target.
	On      bool
	Heating bool
	// Target is the temperature it heats to, in Celsius, between Least
	// and Most, which do not change while the device runs.
	Target float64
	Least  float64
	Most   float64

	// SetOn sets it to heat, to the target it has, or to heat no more.
	SetOn func(ctx context.Context, on bool) error
	// SetTarget sets the temperature it heats to. One outside Least to
	// Most is ErrInvalid.
	SetTarget func(ctx context.Context, celsius float64) error
}

// The temperatures Matter has a number for, in Celsius.
const (
	coldest = -273.15
	hottest = 327.67
)

// Button is one button of a Remote. Its position in Device.Buttons is
// what ButtonPressed and ButtonReleased name.
type Button struct {
	// Label is what is printed on it, or what it does: "On", "Raise".
	Label string
}

// Validate reports a description that cannot be served, as
// ErrDescription with which of these it is: no ID; no Kind, or one that
// is not a kind; a Light or Plug without OnOff, or with Buttons or a
// FanControl; an OnOff without Set; a LevelControl without Set, with a
// Level outside its range, or with one of Move and Stop and not the
// other; a Remote without Buttons, or with anything else; a Fan or an
// AirPurifier without FanControl, or with anything else; a FanControl
// without SetMode or SetSpeed, with a Mode that is none, a Speed outside
// a level's range, Steps below zero or above a hundred, or with one of
// Oscillating and SetOscillating, or of Sleep and SetSleep, and not the
// other; anything but an AirPurifier with a Temperature, a Humidity, an
// AirQuality, Filters or a Heater; a Temperature outside what Matter has
// a number for, a Humidity outside 0 to 100, an AirQuality with a
// Quality that is none or particulates below zero, a Filter of no kind,
// of a kind the device has already, or with a Left outside 0 to 1; a
// Heater without SetOn or SetTarget, with a Least that is not below its
// Most, either outside what Matter has a number for, or a Target outside
// the two.
func (d *Device) Validate() error {
	switch {
	case d.ID == "":
		return fmt.Errorf("%w: it has no ID", ErrDescription)
	case d.Kind != AirPurifier && d.hasAir():
		return fmt.Errorf("%w: %q is no air purifier, and has what only one has", ErrDescription, d.ID)
	case d.Kind == Light || d.Kind == Plug:
		return d.validateSwitched()
	case d.Kind == Remote:
		if len(d.Buttons) == 0 {
			return fmt.Errorf("%w: %q is a remote and has no buttons", ErrDescription, d.ID)
		}

		if d.OnOff != nil || d.LevelControl != nil || d.FanControl != nil {
			return fmt.Errorf("%w: %q is a remote and has more than buttons", ErrDescription, d.ID)
		}

		return nil
	case d.Kind == Fan || d.Kind == AirPurifier:
		if err := d.validateFan(); err != nil {
			return err
		}

		return d.validateAir()
	default:
		return fmt.Errorf("%w: %q has no kind, or one that is not a kind", ErrDescription, d.ID)
	}
}

// validateSwitched is Validate for a Light or a Plug.
func (d *Device) validateSwitched() error {
	switch {
	case d.OnOff == nil:
		return fmt.Errorf("%w: %q is a light or a plug and has no OnOff", ErrDescription, d.ID)
	case d.OnOff.Set == nil:
		return fmt.Errorf("%w: %q has an OnOff that cannot be set", ErrDescription, d.ID)
	case len(d.Buttons) > 0:
		return fmt.Errorf("%w: %q is a light or a plug and has buttons", ErrDescription, d.ID)
	case d.FanControl != nil:
		return fmt.Errorf("%w: %q is a light or a plug and has a FanControl", ErrDescription, d.ID)
	case d.LevelControl == nil:
		return nil
	case d.LevelControl.Set == nil:
		return fmt.Errorf("%w: %q has a LevelControl that cannot be set", ErrDescription, d.ID)
	case !(d.LevelControl.Level > 0 && d.LevelControl.Level <= 1): // not a number is outside too
		return fmt.Errorf("%w: %q has the level %v, outside above 0 to 1", ErrDescription, d.ID, d.LevelControl.Level)
	case (d.LevelControl.Move == nil) != (d.LevelControl.Stop == nil):
		return fmt.Errorf("%w: %q has one of Move and Stop without the other", ErrDescription, d.ID)
	default:
		return nil
	}
}

// validateFan is Validate for a Fan or an AirPurifier.
func (d *Device) validateFan() error {
	fan := d.FanControl

	switch {
	case fan == nil:
		return fmt.Errorf("%w: %q is a fan and has no FanControl", ErrDescription, d.ID)
	case d.OnOff != nil || d.LevelControl != nil || len(d.Buttons) > 0:
		return fmt.Errorf("%w: %q is a fan and has more than a FanControl", ErrDescription, d.ID)
	case fan.SetMode == nil || fan.SetSpeed == nil:
		return fmt.Errorf("%w: %q has a FanControl whose mode or speed cannot be set", ErrDescription, d.ID)
	case fan.Mode < FanOff || fan.Mode > FanAuto:
		return fmt.Errorf("%w: %q has the fan mode %d, which is none", ErrDescription, d.ID, fan.Mode)
	case !(fan.Speed > 0 && fan.Speed <= 1): // not a number is outside too
		return fmt.Errorf("%w: %q has the fan speed %v, outside above 0 to 1", ErrDescription, d.ID, fan.Speed)
	case fan.Steps < 0 || fan.Steps > maxSteps:
		return fmt.Errorf("%w: %q has %d fan speeds, outside 0 to %d", ErrDescription, d.ID, fan.Steps, maxSteps)
	case (fan.Oscillating == nil) != (fan.SetOscillating == nil):
		return fmt.Errorf("%w: %q has one of Oscillating and SetOscillating without the other", ErrDescription, d.ID)
	case (fan.Sleep == nil) != (fan.SetSleep == nil):
		return fmt.Errorf("%w: %q has one of Sleep and SetSleep without the other", ErrDescription, d.ID)
	default:
		return nil
	}
}

// hasAir says whether the device has anything of what an AirPurifier
// has beside its fan.
func (d *Device) hasAir() bool {
	return d.Temperature != nil || d.Humidity != nil || d.AirQuality != nil || len(d.Filters) > 0 || d.Heater != nil
}

// validateAir is Validate for what an AirPurifier has beside its fan.
func (d *Device) validateAir() error {
	switch {
	case d.Temperature != nil && !within(d.Temperature.Celsius, coldest, hottest):
		return fmt.Errorf("%w: %q reads a temperature Matter has no number for", ErrDescription, d.ID)
	case d.Humidity != nil && !within(d.Humidity.Percent, 0, percent):
		return fmt.Errorf("%w: %q reads a humidity outside 0 to 100", ErrDescription, d.ID)
	case d.AirQuality != nil:
		if err := d.AirQuality.validate(d.ID); err != nil {
			return err
		}
	}

	if err := d.validateFilters(); err != nil {
		return err
	}

	if d.Heater != nil {
		return d.Heater.validate(d.ID)
	}

	return nil
}

// validateFilters is Validate for an AirPurifier's filters.
func (d *Device) validateFilters() error {
	seen := map[FilterKind]bool{}

	for index, filter := range d.Filters {
		switch {
		case filter.Kind != HEPA && filter.Kind != Carbon:
			return fmt.Errorf("%w: %q has a filter %d of no kind", ErrDescription, d.ID, index)
		case seen[filter.Kind]:
			return fmt.Errorf("%w: %q has two filters of a kind", ErrDescription, d.ID)
		case !within(filter.Left, 0, 1):
			return fmt.Errorf("%w: %q has a filter %d with a life left outside 0 to 1", ErrDescription, d.ID, index)
		}

		seen[filter.Kind] = true
	}

	return nil
}

func (a *AirQuality) validate(device ID) error {
	switch {
	case a.Quality < QualityUnknown || a.Quality > QualityExtremelyPoor:
		return fmt.Errorf("%w: %q has the air quality %d, which is none", ErrDescription, device, a.Quality)
	case a.PM25 != nil && !within(a.PM25.Micrograms, 0, mostParticulates),
		a.PM10 != nil && !within(a.PM10.Micrograms, 0, mostParticulates):
		return fmt.Errorf("%w: %q counts particulates below zero, or beyond counting", ErrDescription, device)
	default:
		return nil
	}
}

func (h *Heater) validate(device ID) error {
	switch {
	case h.SetOn == nil || h.SetTarget == nil:
		return fmt.Errorf("%w: %q has a Heater that cannot be set", ErrDescription, device)
	case !(h.Least < h.Most) || !(h.Least >= coldest) || !(h.Most <= hottest): // not a number is outside too
		return fmt.Errorf("%w: %q has a Heater whose least and most are no range", ErrDescription, device)
	case !(h.Target >= h.Least && h.Target <= h.Most):
		return fmt.Errorf("%w: %q heats to %v, outside %v to %v", ErrDescription, device, h.Target, h.Least, h.Most)
	default:
		return nil
	}
}

// What a reading is held to.
const (
	percent          = 100
	mostParticulates = 1e9
)

// within says whether a reading that may not be known is one that is
// not, or one from least to most. Not a number is outside.
func within(reading *float64, least, most float64) bool {
	return reading == nil || (*reading >= least && *reading <= most)
}
