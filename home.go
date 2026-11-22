package matter

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Home is a Source held in memory, for whatever knows the devices to
// write into: a maker's adapter, several of them side by side, a test.
// It keeps every promise a Source makes, so that none of them has to:
// the devices as they stand, watchers told in order and none of them
// held up by another, nothing lost between a watcher's first look and
// what follows, a device it holds unreachable answered for with
// ErrUnreachable and its commands not called. The zero Home is empty and
// ready, and safe to use from several goroutines. It is not to be
// copied.
type Home struct {
	mu       sync.Mutex
	devices  map[ID]*Device
	watchers map[*watcher]struct{}
}

// watcher is one Watch: what it has yet to be told, in order, and how it
// is woken. The queue is the home's lock's.
type watcher struct {
	queue []Event
	wake  chan struct{}
}

// Put adds a device, or replaces the one of the same ID, as it is
// described: its state as given, its commands the ones to call from now
// on, while it is reachable. The home takes its own copy. A description
// that does not Validate is refused with its error and changes nothing.
// Watchers are told a new device added, and a replaced one removed and
// added.
//
// A Device the home hands out is bound to what was put: its commands
// call the commands that device was put with, for as long as the home
// holds it. Once it is removed, or replaced by another Put, they answer
// ErrNoDevice, and what the home hands out from then on is the new one.
// So a device read from this home and put back into it does not work:
// its commands are the home's own for the device it replaces, and answer
// ErrNoDevice. A device is put with commands of the caller's making.
func (h *Home) Put(device Device) error {
	if err := device.Validate(); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.devices == nil {
		h.devices = map[ID]*Device{}
	}

	if _, held := h.devices[device.ID]; held {
		h.tell(Event{Device: device.ID, Change: DeviceRemoved{}})
	}

	kept := device.clone()
	h.devices[device.ID] = &kept

	h.tell(Event{Device: device.ID, Change: DeviceAdded{Device: h.handOut(&kept)}})

	return nil
}

// Remove takes a device out. Removing one that is not there does
// nothing.
func (h *Home) Remove(id ID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, held := h.devices[id]; !held {
		return
	}

	delete(h.devices, id)
	h.tell(Event{Device: id, Change: DeviceRemoved{}})
}

// Report says what happened to a device: its state follows, and watchers
// are told. A change that changes nothing is told to no one, so an
// adapter may report what its hub says as often as its hub says it; a
// button is always told. ErrNoDevice is a device that is not there, and
// ErrDescription a change the device cannot have: a level for one with
// no LevelControl or outside a level's range, a fan for one with no
// FanControl, in no mode, at a speed outside a level's range, or doing
// what it cannot, a reading for a device that reads no such thing or
// outside what Validate holds it to, a filter it lacks or one whose life
// left is said where it is not or not said where it is, a heater for one
// that has none or a target outside its range, a button it lacks, a
// device added or removed, which are Put's and Remove's to say. Changes
// reported together are taken together, or none of them is.
func (h *Home) Report(id ID, changes ...Change) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	device, held := h.devices[id]
	if !held {
		return fmt.Errorf("%w: %q", ErrNoDevice, id)
	}

	for _, change := range changes {
		if err := device.takes(change); err != nil {
			return err
		}
	}

	for _, change := range changes {
		if device.apply(change) {
			h.tell(Event{Device: id, Change: change})
		}
	}

	return nil
}

// Device is one device as it stands, and whether it is there.
func (h *Home) Device(id ID) (Device, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	device, held := h.devices[id]
	if !held {
		return Device{}, false
	}

	return h.handOut(device), true
}

// Devices is Source's: the devices in the order of their IDs. It never
// fails.
func (h *Home) Devices(context.Context) ([]Device, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.all(), nil
}

// Watch is Source's. It ends only with ctx.
func (h *Home) Watch(ctx context.Context, handle func(Event)) error {
	watching := &watcher{wake: make(chan struct{}, 1)}

	// The devices as they stand and the watcher's place among those told
	// what follows are taken under one lock: nothing falls between.
	h.mu.Lock()

	for _, device := range h.all() {
		watching.queue = append(watching.queue, Event{Device: device.ID, Change: DeviceAdded{Device: device}})
	}

	if h.watchers == nil {
		h.watchers = map[*watcher]struct{}{}
	}

	h.watchers[watching] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.watchers, watching)
		h.mu.Unlock()
	}()

	for {
		h.mu.Lock()
		pending := watching.queue
		watching.queue = nil
		h.mu.Unlock()

		for _, event := range pending {
			if ctx.Err() != nil {
				return ctx.Err() //nolint:wrapcheck // the context's own error is what Watch promises
			}

			handle(event)
		}

		if len(pending) > 0 {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // the context's own error is what Watch promises
		case <-watching.wake:
		}
	}
}

// all is every device, handed out, in the order of their IDs. The lock
// is held.
func (h *Home) all() []Device {
	devices := make([]Device, 0, len(h.devices))

	for _, device := range h.devices {
		devices = append(devices, h.handOut(device))
	}

	slices.SortFunc(devices, func(a, b Device) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})

	return devices
}

// tell queues an event for every watcher, each its own copy, and wakes
// them. The lock is held, and no watcher's handler is run under it: a
// slow one holds up only itself.
func (h *Home) tell(event Event) {
	for watching := range h.watchers {
		told := event

		if added, isAdded := event.Change.(DeviceAdded); isAdded {
			told.Change = DeviceAdded{Device: added.Device.clone()}
		}

		watching.queue = append(watching.queue, told)

		select {
		case watching.wake <- struct{}{}:
		default:
		}
	}
}

// handOut is the caller's own copy of a device the home holds, its
// commands bound to that holding: each asks the home whether it still
// holds this very device, reachable, and then calls the command it was
// put with. A command never looks up what to call, so none can come back
// to itself, whatever is put into the home. The lock is held.
func (h *Home) handOut(held *Device) Device {
	out := held.clone()

	if out.OnOff != nil {
		set := held.OnOff.Set

		out.OnOff.Set = func(ctx context.Context, on bool) error {
			if err := h.holds(held); err != nil {
				return err
			}

			return set(ctx, on)
		}
	}

	if out.LevelControl != nil {
		h.handOutLevel(held, out.LevelControl)
	}

	if out.FanControl != nil {
		h.handOutFan(held, out.FanControl)
	}

	if out.Heater != nil {
		setOn, setTarget := held.Heater.SetOn, held.Heater.SetTarget

		out.Heater.SetOn = func(ctx context.Context, on bool) error {
			if err := h.holds(held); err != nil {
				return err
			}

			return setOn(ctx, on)
		}

		out.Heater.SetTarget = func(ctx context.Context, celsius float64) error {
			if err := h.holds(held); err != nil {
				return err
			}

			return setTarget(ctx, celsius)
		}
	}

	return out
}

// handOutLevel is handOut for a LevelControl. Move and Stop stay nil for
// a device put without them.
func (h *Home) handOutLevel(held *Device, out *LevelControl) {
	set, move, stop := held.LevelControl.Set, held.LevelControl.Move, held.LevelControl.Stop

	out.Set = func(ctx context.Context, level Level, over time.Duration) error {
		if err := h.holds(held); err != nil {
			return err
		}

		return set(ctx, level, over)
	}

	if move == nil {
		return
	}

	out.Move = func(ctx context.Context, direction Direction) error {
		if err := h.holds(held); err != nil {
			return err
		}

		return move(ctx, direction)
	}

	out.Stop = func(ctx context.Context) error {
		if err := h.holds(held); err != nil {
			return err
		}

		return stop(ctx)
	}
}

// handOutFan is handOut for a FanControl. SetOscillating and SetSleep
// stay nil for a device put without them.
func (h *Home) handOutFan(held *Device, out *FanControl) {
	setMode, setSpeed := held.FanControl.SetMode, held.FanControl.SetSpeed
	setOscillating, setSleep := held.FanControl.SetOscillating, held.FanControl.SetSleep

	out.SetMode = func(ctx context.Context, mode FanMode) error {
		if err := h.holds(held); err != nil {
			return err
		}

		return setMode(ctx, mode)
	}

	out.SetSpeed = func(ctx context.Context, speed Level) error {
		if err := h.holds(held); err != nil {
			return err
		}

		return setSpeed(ctx, speed)
	}

	if setOscillating != nil {
		out.SetOscillating = func(ctx context.Context, on bool) error {
			if err := h.holds(held); err != nil {
				return err
			}

			return setOscillating(ctx, on)
		}
	}

	if setSleep != nil {
		out.SetSleep = func(ctx context.Context, on bool) error {
			if err := h.holds(held); err != nil {
				return err
			}

			return setSleep(ctx, on)
		}
	}
}

// holds says whether the home still holds this very device, and
// reachable: ErrNoDevice once it is removed or replaced, ErrUnreachable
// while the home holds it unreachable. The command is called after, with
// no lock held.
func (h *Home) holds(held *Device) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	switch {
	case h.devices[held.ID] != held:
		return fmt.Errorf("%w: %q is no longer the device this was read from", ErrNoDevice, held.ID)
	case held.Unreachable:
		return fmt.Errorf("%w: %q", ErrUnreachable, held.ID)
	default:
		return nil
	}
}

// clone is a copy that shares nothing with d but its commands.
func (d *Device) clone() Device {
	out := *d
	out.Buttons = slices.Clone(d.Buttons)

	if d.OnOff != nil {
		onOff := *d.OnOff
		out.OnOff = &onOff
	}

	if d.LevelControl != nil {
		level := *d.LevelControl
		out.LevelControl = &level
	}

	if d.FanControl != nil {
		fan := *d.FanControl
		fan.Oscillating, fan.Sleep = copied(fan.Oscillating), copied(fan.Sleep)
		out.FanControl = &fan
	}

	out.cloneAir(d)

	return out
}

// cloneAir is clone for what an AirPurifier has beside its fan.
func (d *Device) cloneAir(from *Device) {
	d.Filters = slices.Clone(from.Filters)

	for index := range d.Filters {
		d.Filters[index].Left = read(d.Filters[index].Left)
	}

	if from.Temperature != nil {
		d.Temperature = &Temperature{Celsius: read(from.Temperature.Celsius)}
	}

	if from.Humidity != nil {
		d.Humidity = &Humidity{Percent: read(from.Humidity.Percent)}
	}

	if from.AirQuality != nil {
		air := *from.AirQuality
		air.PM25, air.PM10 = air.PM25.clone(), air.PM10.clone()
		d.AirQuality = &air
	}

	if from.Heater != nil {
		heater := *from.Heater
		d.Heater = &heater
	}
}

func (p *Particulates) clone() *Particulates {
	if p == nil {
		return nil
	}

	return &Particulates{Micrograms: read(p.Micrograms)}
}

// read is a reading of the caller's own, or nil for none.
func read(reading *float64) *float64 {
	if reading == nil {
		return nil
	}

	own := *reading

	return &own
}

// same says whether two readings are one: both none, or both the same.
func same(a, b *float64) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// copied is a flag of the caller's own, or nil for none.
func copied(flag *bool) *bool {
	if flag == nil {
		return nil
	}

	own := *flag

	return &own
}

// takes says whether the device can have a change, as ErrDescription
// when it cannot.
func (d *Device) takes(change Change) error {
	switch change := change.(type) {
	case OnOffChanged:
		if d.OnOff == nil {
			return fmt.Errorf("%w: %q is not switched, and was reported on or off", ErrDescription, d.ID)
		}
	case LevelChanged:
		if d.LevelControl == nil {
			return fmt.Errorf("%w: %q has no level, and was reported one", ErrDescription, d.ID)
		}

		if !(change.Level > 0 && change.Level <= 1) { // not a number is outside too
			return fmt.Errorf(
				"%w: %q was reported the level %v, outside above 0 to 1",
				ErrDescription,
				d.ID,
				change.Level,
			)
		}
	case FanChanged:
		return d.takesFan(change)
	case TemperatureChanged, HumidityChanged, AirQualityChanged, FilterChanged, HeaterChanged:
		return d.takesAir(change)
	case ReachabilityChanged:
	case ButtonPressed:
		return d.hasButton(change.Button)
	case ButtonReleased:
		return d.hasButton(change.Button)
	case DeviceAdded, DeviceRemoved:
		return fmt.Errorf("%w: %q added or removed is Put's and Remove's to say", ErrDescription, d.ID)
	default:
		return fmt.Errorf("%w: %q was reported no change, or one that is not a change", ErrDescription, d.ID)
	}

	return nil
}

// takesFan is takes for a fan.
func (d *Device) takesFan(change FanChanged) error {
	fan := d.FanControl

	switch {
	case fan == nil:
		return fmt.Errorf("%w: %q has no fan, and was reported one", ErrDescription, d.ID)
	case change.Mode < FanOff || change.Mode > FanAuto:
		return fmt.Errorf("%w: %q was reported the fan mode %d, which is none", ErrDescription, d.ID, change.Mode)
	case !(change.Speed > 0 && change.Speed <= 1): // not a number is outside too
		return fmt.Errorf(
			"%w: %q was reported the fan speed %v, outside above 0 to 1", ErrDescription, d.ID, change.Speed,
		)
	case change.Oscillating && fan.Oscillating == nil:
		return fmt.Errorf("%w: %q does not oscillate, and was reported to", ErrDescription, d.ID)
	case change.Sleep && fan.Sleep == nil:
		return fmt.Errorf("%w: %q has no sleep mode, and was reported in it", ErrDescription, d.ID)
	default:
		return nil
	}
}

// takesAir is takes for what an AirPurifier has beside its fan.
func (d *Device) takesAir(change Change) error {
	switch change := change.(type) {
	case TemperatureChanged:
		if d.Temperature == nil || !within(change.Celsius, coldest, hottest) {
			return fmt.Errorf("%w: %q was reported a temperature it cannot have", ErrDescription, d.ID)
		}
	case HumidityChanged:
		if d.Humidity == nil || !within(change.Percent, 0, percent) {
			return fmt.Errorf("%w: %q was reported a humidity it cannot have", ErrDescription, d.ID)
		}
	case AirQualityChanged:
		return d.takesAirQuality(change)
	case FilterChanged:
		return d.takesFilter(change)
	case HeaterChanged:
		if d.Heater == nil || !(change.Target >= d.Heater.Least && change.Target <= d.Heater.Most) {
			return fmt.Errorf("%w: %q was reported a heater it cannot have", ErrDescription, d.ID)
		}
	}

	return nil
}

// takesFilter is takes for a filter.
func (d *Device) takesFilter(change FilterChanged) error {
	if change.Filter < 0 || change.Filter >= len(d.Filters) {
		return fmt.Errorf("%w: %q has no filter %d", ErrDescription, d.ID, change.Filter)
	}

	if (change.Left == nil) != (d.Filters[change.Filter].Left == nil) || !within(change.Left, 0, 1) {
		return fmt.Errorf(
			"%w: %q was reported a filter %d with a life left it cannot have", ErrDescription, d.ID, change.Filter,
		)
	}

	return nil
}

// takesAirQuality is takes for the quality of the air.
func (d *Device) takesAirQuality(change AirQualityChanged) error {
	air := d.AirQuality

	switch {
	case air == nil:
		return fmt.Errorf("%w: %q reads no air quality, and was reported one", ErrDescription, d.ID)
	case change.Quality < QualityUnknown || change.Quality > QualityExtremelyPoor:
		return fmt.Errorf("%w: %q was reported the air quality %d, which is none", ErrDescription, d.ID, change.Quality)
	case (change.PM25 != nil && air.PM25 == nil) || (change.PM10 != nil && air.PM10 == nil):
		return fmt.Errorf("%w: %q was reported particulates it does not count", ErrDescription, d.ID)
	case !within(change.PM25, 0, mostParticulates) || !within(change.PM10, 0, mostParticulates):
		return fmt.Errorf("%w: %q was reported particulates below zero, or beyond counting", ErrDescription, d.ID)
	default:
		return nil
	}
}

// hasButton is takes for a button.
func (d *Device) hasButton(button int) error {
	if button < 0 || button >= len(d.Buttons) {
		return fmt.Errorf("%w: %q has no button %d", ErrDescription, d.ID, button)
	}

	return nil
}

// apply makes a change the device takes its state, and says whether
// watchers are to be told: a state that moved, or a button.
func (d *Device) apply(change Change) bool {
	switch change := change.(type) {
	case OnOffChanged:
		if d.OnOff.On == change.On {
			return false
		}

		d.OnOff.On = change.On
	case LevelChanged:
		if d.LevelControl.Level == change.Level {
			return false
		}

		d.LevelControl.Level = change.Level
	case FanChanged:
		return d.FanControl.apply(change)
	case TemperatureChanged, HumidityChanged, AirQualityChanged, FilterChanged, HeaterChanged:
		return d.applyAir(change)
	case ReachabilityChanged:
		if d.Unreachable == change.Unreachable {
			return false
		}

		d.Unreachable = change.Unreachable
	}

	return true
}

// apply is Device.apply for a fan.
func (f *FanControl) apply(change FanChanged) bool {
	oscillating := f.Oscillating != nil && *f.Oscillating
	sleep := f.Sleep != nil && *f.Sleep

	if f.Mode == change.Mode && f.Speed == change.Speed && f.Running == change.Running &&
		oscillating == change.Oscillating && sleep == change.Sleep {
		return false
	}

	f.Mode, f.Speed, f.Running = change.Mode, change.Speed, change.Running

	if f.Oscillating != nil {
		f.Oscillating = &change.Oscillating
	}

	if f.Sleep != nil {
		f.Sleep = &change.Sleep
	}

	return true
}

// applyAir is apply for what an AirPurifier has beside its fan. What is
// kept is the device's own: never the reading the change came with.
func (d *Device) applyAir(change Change) bool {
	switch change := change.(type) {
	case TemperatureChanged:
		if same(d.Temperature.Celsius, change.Celsius) {
			return false
		}

		d.Temperature.Celsius = read(change.Celsius)
	case HumidityChanged:
		if same(d.Humidity.Percent, change.Percent) {
			return false
		}

		d.Humidity.Percent = read(change.Percent)
	case AirQualityChanged:
		return d.AirQuality.apply(change)
	case FilterChanged:
		filter := &d.Filters[change.Filter]
		if same(filter.Left, change.Left) && filter.Replace == change.Replace {
			return false
		}

		filter.Left, filter.Replace = read(change.Left), change.Replace
	case HeaterChanged:
		heater := d.Heater
		if heater.On == change.On && heater.Heating == change.Heating && heater.Target == change.Target {
			return false
		}

		heater.On, heater.Heating, heater.Target = change.On, change.Heating, change.Target
	}

	return true
}

// apply is Device.apply for the quality of the air.
func (a *AirQuality) apply(change AirQualityChanged) bool {
	moved := a.Quality != change.Quality

	a.Quality = change.Quality

	for _, counted := range []struct {
		held *Particulates
		now  *float64
	}{{a.PM25, change.PM25}, {a.PM10, change.PM10}} {
		if counted.held != nil && !same(counted.held.Micrograms, counted.now) {
			counted.held.Micrograms, moved = read(counted.now), true
		}
	}

	return moved
}
