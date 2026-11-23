package matter

import "context"

// Source is a set of devices that share a way in: a Home that adapters
// write into, one Matter node, every node a controller holds.
type Source interface {
	// Devices is the devices as they stand now.
	Devices(ctx context.Context) ([]Device, error)

	// Watch tells handle first every device as it stands, each as a
	// DeviceAdded, and then everything that happens, for as long as ctx
	// lasts: nothing falls between the two. Events come one at a time
	// and in order, never two at once. handle may take its time and may
	// ask things of the devices; later events wait for it, and no other
	// watcher does. Several may watch at once, each told everything.
	//
	// Watch returns ctx's error when ctx ends. Sooner, it returns
	// ErrClosed when the source is over for good and there is nothing
	// left to watch, or any other error when it failed and may be
	// watched again. A device that is gone for now is neither: it is
	// unreachable, and Watch goes on.
	Watch(ctx context.Context, handle func(Event)) error
}

// Event is something that happened to one device.
type Event struct {
	Device ID
	Change Change
}

// Change is what happened: one of the types below, and no other. A
// change of state carries the new value whole, so applying one twice
// does no harm. A button is an occurrence: two are two.
type Change interface{ change() }

type (
	// OnOffChanged is the device having gone on or off, whoever asked.
	OnOffChanged struct{ On bool }
	// LevelChanged is the device's level having changed, as
	// LevelControl.Level has it.
	LevelChanged struct{ Level Level }
	// FanChanged is how the device's fan stands, whole, as FanControl
	// has it. Oscillating and Sleep are false for a fan that cannot.
	FanChanged struct {
		Mode        FanMode
		Speed       Level
		Running     bool
		Oscillating bool
		Sleep       bool
	}
	// TemperatureChanged is what the device reads of the temperature, as
	// Temperature has it: nil when it reads nothing.
	TemperatureChanged struct{ Celsius *float64 }
	// HumidityChanged is what the device reads of the humidity, as
	// Humidity has it: nil when it reads nothing.
	HumidityChanged struct{ Percent *float64 }
	// AirQualityChanged is what the device reads of the air, whole, as
	// AirQuality has it. PM25 and PM10 are nil when it reads nothing,
	// and for a device that counts none.
	AirQualityChanged struct {
		Quality Quality
		PM25    *float64
		PM10    *float64
	}
	// FilterChanged is how a filter stands, as Filter has it. Left is nil
	// exactly for a filter that does not say how much of it is left.
	FilterChanged struct {
		Filter  int
		Left    *float64
		Replace bool
	}
	// HeaterChanged is how the device's heater stands, whole, as Heater
	// has it.
	HeaterChanged struct {
		On      bool
		Heating bool
		Target  float64
	}
	// ReachabilityChanged is the source having lost the device, or
	// found it again.
	ReachabilityChanged struct{ Unreachable bool }
	// ButtonPressed is a button having gone down.
	ButtonPressed struct{ Button int }
	// ButtonReleased is a button having come up. What a long press or a
	// double press is, is for whoever hears these to say.
	ButtonReleased struct{ Button int }
	// DeviceAdded is a device the watcher had not been told of, as it
	// stands.
	DeviceAdded struct{ Device Device }
	// DeviceRemoved is the source no longer holding the device.
	DeviceRemoved struct{}
)

func (OnOffChanged) change()        {}
func (LevelChanged) change()        {}
func (FanChanged) change()          {}
func (TemperatureChanged) change()  {}
func (HumidityChanged) change()     {}
func (AirQualityChanged) change()   {}
func (FilterChanged) change()       {}
func (HeaterChanged) change()       {}
func (ReachabilityChanged) change() {}
func (ButtonPressed) change()       {}
func (ButtonReleased) change()      {}
func (DeviceAdded) change()         {}
func (DeviceRemoved) change()       {}
