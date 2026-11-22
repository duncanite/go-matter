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
// other.
func (d *Device) Validate() error {
	switch {
	case d.ID == "":
		return fmt.Errorf("%w: it has no ID", ErrDescription)
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
		return d.validateFan()
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
