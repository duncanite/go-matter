// Package fabric is this module's one way into the Matter implementation
// underneath, github.com/SukramJ/go-fabric: what package bridge and
// package controller are made of, and what package mattertest runs on a
// network of its own. Nothing of that library is named outside this
// package.
package fabric

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"
	"unicode/utf8"

	fixedlabeldef "github.com/SukramJ/go-fabric/cluster/spec/fixedlabel"
	levelcontroldef "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	switchdef "github.com/SukramJ/go-fabric/cluster/spec/switchcluster"
	"github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/node"

	"github.com/duncanite/go-matter"
)

// The Interaction Model's statuses an error crosses the protocol as.
const (
	statusFailure     uint8 = 0x01
	statusUnsupported uint8 = 0x81
	statusConstraint  uint8 = 0x87
	statusTimeout     uint8 = 0x94
	statusBusy        uint8 = 0x9C
)

const (
	// Matter's level, for a light: 1 to 254.
	levelLeast uint8 = 1
	levelFull  uint8 = 254
	// A transition time is in tenths of a second.
	tenth = 100 * time.Millisecond
	// What a label and a name may be long, in bytes.
	labelBytes = 16
	nameBytes  = 32
	// A button is a switch of two positions: up, and down.
	buttonUp, buttonDown uint8 = 0, 1
	buttonPositions      uint8 = 2
	buttonPresses        uint8 = 3
	// What a device's endpoint is kept under, and a button's: an ID is
	// the source's to choose, so neither is one the other could be. A
	// button's index is before the ID, which may hold anything.
	deviceKey = "d:"
	buttonKey = "b:"
	// How long a source whose Watch failed is left before it is watched
	// again.
	rewatch = 2 * time.Second
)

// errNoSuchState is a change told of a device that cannot have it.
var errNoSuchState = errors.New("the source told a change its device cannot have")

// Serving is a source being served through a node's Aggregator.
type Serving struct {
	node   *node.Node
	source matter.Source
	stop   context.CancelFunc
	done   chan struct{}

	// served is the watching goroutine's alone.
	served map[matter.ID]*served
}

// served is one device of the source as the node has it: one endpoint,
// or for a remote one per button.
type served struct {
	keys    []device.SourceKey
	devices []*device.Device
	onOff   *onoffdef.Server
	level   *levelcontroldef.Server
	buttons []*switchdef.Server

	// mu guards what a controller's commands read: how the device stands
	// and what to call.
	mu      sync.Mutex
	device  matter.Device
	pending matter.Level
}

// Serve watches the source and serves its devices under the node's
// Aggregator until Close. The node is started and stays the caller's.
func Serve(n *node.Node, source matter.Source) *Serving {
	ctx, stop := context.WithCancel(context.Background())

	serving := &Serving{
		node: n, source: source, stop: stop,
		done: make(chan struct{}), served: map[matter.ID]*served{},
	}

	go serving.watch(ctx)

	return serving
}

// Close stops watching and returns when the watching has ended. The
// devices stay on the node as they are.
func (s *Serving) Close() {
	s.stop()
	<-s.done
}

// watch is the one goroutine that changes the Aggregator. A source whose
// Watch fails has its devices shown unreachable and is watched again;
// one whose Watch says it is over has them removed.
func (s *Serving) watch(ctx context.Context) {
	defer close(s.done)

	for {
		err := s.source.Watch(ctx, func(event matter.Event) { s.handle(ctx, event) })

		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, matter.ErrClosed):
			slog.InfoContext(ctx, "the source is over; its devices are removed")

			for id := range s.served {
				s.remove(ctx, id)
			}

			return
		}

		slog.WarnContext(ctx, "watching the source failed; its devices are unreachable until it is watched again",
			"error", err)

		for _, held := range s.served {
			held.reachable(ctx, false)
		}

		if !s.again(ctx) {
			return
		}
	}
}

// again waits until a source that failed can be asked what it holds, and
// lets go of what it no longer does: a device removed while its source
// was not watched is told of by no event. What it still holds is told of
// anew by the next Watch. It is false when ctx ended first.
func (s *Serving) again(ctx context.Context) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(rewatch):
		}

		holds, err := s.source.Devices(ctx)
		if err != nil {
			continue
		}

		still := make(map[matter.ID]bool, len(holds))
		for index := range holds {
			still[holds[index].ID] = true
		}

		for held := range s.served {
			if !still[held] {
				s.remove(ctx, held)
			}
		}

		return true
	}
}

// handle takes one event of the source to the node.
func (s *Serving) handle(ctx context.Context, event matter.Event) {
	if added, isAdded := event.Change.(matter.DeviceAdded); isAdded {
		s.remove(ctx, event.Device)
		s.add(ctx, added.Device)

		return
	}

	held, known := s.served[event.Device]
	if !known {
		return
	}

	switch change := event.Change.(type) {
	case matter.DeviceRemoved:
		s.remove(ctx, event.Device)
	case matter.OnOffChanged:
		// A source that is no Home may say what a device cannot have:
		// said, and dropped.
		if held.onOff == nil {
			warn(ctx, event.Device, errNoSuchState)

			return
		}

		held.mu.Lock()
		held.device.OnOff.On = change.On
		held.mu.Unlock()

		warn(ctx, event.Device, held.onOff.Set(onoffdef.Patch{OnOff: &change.On}))
	case matter.LevelChanged:
		if held.level == nil || !(change.Level > 0 && change.Level <= 1) {
			warn(ctx, event.Device, errNoSuchState)

			return
		}

		held.mu.Lock()
		held.device.LevelControl.Level = change.Level
		held.mu.Unlock()

		level := toMatter(change.Level)
		warn(ctx, event.Device, held.level.Set(levelcontroldef.Patch{CurrentLevel: &level}))
	case matter.ReachabilityChanged:
		held.reachable(ctx, !change.Unreachable)
	case matter.ButtonPressed:
		held.press(ctx, change.Button, buttonDown)
	case matter.ButtonReleased:
		held.press(ctx, change.Button, buttonUp)
	}
}

// add serves a device the source holds. One that cannot be served is
// said and left out.
func (s *Serving) add(ctx context.Context, described matter.Device) {
	if err := described.Validate(); err != nil {
		slog.WarnContext(ctx, "a device is left out", "error", err)

		return
	}

	held := &served{device: described}

	endpoints, err := held.build()
	if err != nil {
		slog.WarnContext(ctx, "a device is left out", "device", described.ID, "error", err)

		return
	}

	err = s.node.Aggregator().Batch(ctx, func(batch *node.Batch) error {
		for _, endpoint := range endpoints {
			if added := batch.Add(endpoint); added != nil {
				return fmt.Errorf("adding its endpoint: %w", added)
			}
		}

		return nil
	})
	if err != nil {
		slog.WarnContext(ctx, "a device is left out", "device", described.ID, "error", err)

		return
	}

	s.served[described.ID] = held

	if described.Unreachable {
		held.reachable(ctx, false)
	}
}

// remove takes a device off the node. Its endpoints' numbers stay its
// own, for the day the source holds it again.
func (s *Serving) remove(ctx context.Context, gone matter.ID) {
	held, known := s.served[gone]
	if !known {
		return
	}

	delete(s.served, gone)

	err := s.node.Aggregator().Batch(ctx, func(batch *node.Batch) error {
		for _, key := range held.keys {
			if err := batch.Remove(key); err != nil {
				return fmt.Errorf("removing its endpoint: %w", err)
			}
		}

		return nil
	})
	warn(ctx, gone, err)
}

// build makes the endpoints a device is served as.
func (h *served) build() ([]node.Endpoint, error) {
	described := &h.device

	if described.Kind == matter.Remote {
		return h.buildButtons()
	}

	options := device.Options{
		StableKey: device.StringKey(deviceKey + string(described.ID)),
		Name:      cut(described.Name, nameBytes),
		Vendor:    cut(described.Vendor, nameBytes),
	}

	declared := []device.Declaration{
		onoffdef.Declare{
			Initial:      onoffdef.Patch{OnOff: &described.OnOff.On},
			HandleOn:     func(ctx context.Context, _ onoffdef.OnRequest) (struct{}, error) { return h.switchTo(ctx, true) },
			HandleOff:    func(ctx context.Context, _ onoffdef.OffRequest) (struct{}, error) { return h.switchTo(ctx, false) },
			HandleToggle: func(ctx context.Context, _ onoffdef.ToggleRequest) (struct{}, error) { return h.toggle(ctx) },
			// The three a light may also be sent: off whatever the effect,
			// on whatever the scene; on for a time is not served, since
			// nothing here would switch the device off again.
			HandleOffWithEffect: func(ctx context.Context, _ onoffdef.OffWithEffectRequest) (struct{}, error) {
				return h.switchTo(ctx, false)
			},
			HandleOnWithRecallGlobalScene: func(
				ctx context.Context, _ onoffdef.OnWithRecallGlobalSceneRequest,
			) (struct{}, error) {
				return h.switchTo(ctx, true)
			},
			HandleOnWithTimedOff: func(context.Context, onoffdef.OnWithTimedOffRequest) (struct{}, error) {
				return struct{}{}, contract.StatusError{
					Status:  statusUnsupported,
					Message: "on for a time is not served",
				}
			},
		},
	}

	if described.Room != "" {
		labels := []fixedlabeldef.LabelStruct{{Label: "room", Value: cut(described.Room, labelBytes)}}
		declared = append(declared, fixedlabeldef.Declare{Initial: fixedlabeldef.Patch{LabelList: &labels}})
	}

	if described.LevelControl != nil {
		declared = append(declared, h.declareLevel())
	}

	var (
		made *device.Device
		err  error
	)

	switch {
	case described.Kind == matter.Light && described.LevelControl != nil:
		var light *device.DimmableLight

		if light, err = device.NewDimmableLight(options, declared...); err == nil {
			made, h.onOff, h.level = light.Device, light.OnOff(), light.LevelControl()
		}
	case described.Kind == matter.Light:
		var light *device.OnOffLight

		if light, err = device.NewOnOffLight(options, declared...); err == nil {
			made, h.onOff = light.Device, light.OnOff()
		}
	case described.LevelControl != nil:
		var plug *device.DimmablePlugInUnit

		if plug, err = device.NewDimmablePlugInUnit(options, declared...); err == nil {
			made, h.onOff, h.level = plug.Device, plug.OnOff(), plug.LevelControl()
		}
	default:
		var plug *device.OnOffPlugInUnit

		if plug, err = device.NewOnOffPlugInUnit(options, declared...); err == nil {
			made, h.onOff = plug.Device, plug.OnOff()
		}
	}

	if err != nil {
		return nil, fmt.Errorf("making its endpoint: %w", err)
	}

	h.keys = []device.SourceKey{options.StableKey}
	h.devices = []*device.Device{made}

	return []node.Endpoint{made}, nil
}

// buildButtons makes a remote's endpoints: one switch of two positions
// for each button, named by the remote and the button. The switch itself
// makes of down and up the long and the repeated presses Matter has
// events for.
func (h *served) buildButtons() ([]node.Endpoint, error) {
	described := &h.device
	endpoints := make([]node.Endpoint, 0, len(described.Buttons))
	positions, presses := buttonPositions, buttonPresses

	for index, button := range described.Buttons {
		options := device.Options{
			StableKey: device.StringKey(fmt.Sprintf("%s%d:%s", buttonKey, index, described.ID)),
			Name:      buttonName(described.Name, button.Label),
			Vendor:    cut(described.Vendor, nameBytes),
		}

		made, err := device.NewGenericSwitch(options, switchdef.Declare{
			Features: switchdef.FeatureMomentarySwitch | switchdef.FeatureMomentarySwitchRelease |
				switchdef.FeatureMomentarySwitchLongPress | switchdef.FeatureMomentarySwitchMultiPress,
			Initial: switchdef.Patch{NumberOfPositions: &positions, MultiPressMax: &presses},
		})
		if err != nil {
			return nil, fmt.Errorf("making the endpoint of its button %d: %w", index, err)
		}

		h.keys = append(h.keys, options.StableKey)
		h.devices = append(h.devices, made.Device)
		h.buttons = append(h.buttons, made.Switch())
		endpoints = append(endpoints, made)
	}

	return endpoints, nil
}

// declareLevel is a device's Level Control, every command of it handled
// here: the device's own level changes when the source says it did.
func (h *served) declareLevel() levelcontroldef.Declare {
	level := toMatter(h.device.LevelControl.Level)

	return levelcontroldef.Declare{
		Initial: levelcontroldef.Patch{CurrentLevel: &level},
		HandleMoveToLevel: func(ctx context.Context, req levelcontroldef.MoveToLevelRequest) (struct{}, error) {
			return h.moveTo(ctx, req.Level, req.TransitionTime.Null, req.TransitionTime.Value, false)
		},
		HandleMoveToLevelWithOnOff: func(
			ctx context.Context, req levelcontroldef.MoveToLevelWithOnOffRequest,
		) (struct{}, error) {
			return h.moveTo(ctx, req.Level, req.TransitionTime.Null, req.TransitionTime.Value, true)
		},
		HandleMove: func(ctx context.Context, req levelcontroldef.MoveRequest) (struct{}, error) {
			return h.move(ctx, req.MoveMode)
		},
		HandleMoveWithOnOff: func(ctx context.Context, req levelcontroldef.MoveWithOnOffRequest) (struct{}, error) {
			return h.move(ctx, req.MoveMode)
		},
		HandleStop: func(ctx context.Context, _ levelcontroldef.StopRequest) (struct{}, error) {
			return h.halt(ctx)
		},
		HandleStopWithOnOff: func(ctx context.Context, _ levelcontroldef.StopWithOnOffRequest) (struct{}, error) {
			return h.halt(ctx)
		},
		HandleStep: func(context.Context, levelcontroldef.StepRequest) (struct{}, error) {
			return struct{}{}, contract.StatusError{Status: statusUnsupported, Message: "Step is not served"}
		},
		HandleStepWithOnOff: func(context.Context, levelcontroldef.StepWithOnOffRequest) (struct{}, error) {
			return struct{}{}, contract.StatusError{Status: statusUnsupported, Message: "Step is not served"}
		},
	}
}

// now is how the device stands, and what to call: a copy of its own,
// its capabilities too, which the watching goroutine writes into.
func (h *served) now() matter.Device {
	h.mu.Lock()
	defer h.mu.Unlock()

	described := h.device

	if h.device.OnOff != nil {
		onOff := *h.device.OnOff
		described.OnOff = &onOff
	}

	if h.device.LevelControl != nil {
		level := *h.device.LevelControl
		described.LevelControl = &level
	}

	return described
}

// switchTo is On and Off. Switched on, a device that was sent a level
// while it was off comes on at that level.
func (h *served) switchTo(ctx context.Context, on bool) (struct{}, error) {
	described := h.now()

	h.mu.Lock()
	pending := h.pending
	h.pending = 0
	h.mu.Unlock()

	if on && pending != 0 && described.LevelControl != nil {
		return struct{}{}, crossing(described.LevelControl.Set(ctx, pending, 0))
	}

	return struct{}{}, crossing(described.OnOff.Set(ctx, on))
}

// toggle is Toggle: the other of how the device stands.
func (h *served) toggle(ctx context.Context) (struct{}, error) {
	return h.switchTo(ctx, !h.now().OnOff.On)
}

// moveTo is both MoveToLevel commands. With on and off, it is the
// device's Set, which brings it on; a level at the least is the device
// switched off, as Matter has it. Without, a device that is on is set,
// and one that is off keeps the level for when it is switched on.
func (h *served) moveTo(ctx context.Context, level uint8, null bool, tenths uint16, withOnOff bool) (struct{}, error) {
	described := h.now()

	if level > levelFull {
		return struct{}{}, contract.StatusError{Status: statusConstraint, Message: "the level is above full"}
	}

	over := time.Duration(0)
	if !null {
		over = time.Duration(tenths) * tenth
	}

	switch {
	case withOnOff && level <= levelLeast:
		return struct{}{}, crossing(described.OnOff.Set(ctx, false))
	case !withOnOff && !described.OnOff.On:
		h.mu.Lock()
		h.pending = fromMatter(level)
		h.mu.Unlock()

		return struct{}{}, nil
	default:
		return struct{}{}, crossing(described.LevelControl.Set(ctx, fromMatter(level), over))
	}
}

// move is both Move commands, for a device that moves.
func (h *served) move(ctx context.Context, mode levelcontroldef.MoveModeEnum) (struct{}, error) {
	described := h.now()

	if described.LevelControl.Move == nil {
		return struct{}{}, contract.StatusError{Status: statusUnsupported, Message: "the device does not move"}
	}

	direction := matter.Up
	if mode == levelcontroldef.MoveModeDown {
		direction = matter.Down
	}

	return struct{}{}, crossing(described.LevelControl.Move(ctx, direction))
}

// halt is both Stop commands, for a device that moves.
func (h *served) halt(ctx context.Context) (struct{}, error) {
	described := h.now()

	if described.LevelControl.Stop == nil {
		return struct{}{}, contract.StatusError{Status: statusUnsupported, Message: "the device does not move"}
	}

	return struct{}{}, crossing(described.LevelControl.Stop(ctx))
}

// reachable says of every endpoint of the device whether it is.
func (h *served) reachable(ctx context.Context, reachable bool) {
	h.mu.Lock()
	h.device.Unreachable = !reachable
	h.mu.Unlock()

	for _, made := range h.devices {
		warn(ctx, h.device.ID, made.SetReachable(ctx, reachable))
	}
}

// press moves a button's switch.
func (h *served) press(ctx context.Context, button int, position uint8) {
	if button < 0 || button >= len(h.buttons) {
		return
	}

	warn(ctx, h.device.ID, h.buttons[button].Host().SetCurrentPosition(position))
}

// crossing is a command's error as the status a controller is answered
// with. An error that is none of the vocabulary's crosses as a failure.
func crossing(err error) error {
	status := statusFailure

	switch {
	case err == nil:
		return nil
	case errors.Is(err, matter.ErrInvalid):
		status = statusConstraint
	case errors.Is(err, matter.ErrBusy):
		status = statusBusy
	case errors.Is(err, matter.ErrUnreachable):
		status = statusTimeout
	}

	return contract.StatusError{Status: status, Message: err.Error()}
}

// toMatter is a level as Matter has it, 1 to 254.
func toMatter(level matter.Level) uint8 {
	scaled := math.Round(float64(level) * float64(levelFull))

	return uint8(min(max(scaled, float64(levelLeast)), float64(levelFull)))
}

// fromMatter is Matter's level as a fraction of full, never zero.
func fromMatter(level uint8) matter.Level {
	return matter.Level(max(level, levelLeast)) / matter.Level(levelFull)
}

// buttonName is a remote's name and its button's label, within what a
// name may be: the remote's name gives way, so that two buttons whose
// labels differ are not named alike. A label that leaves it no room is
// the whole name, and a button with none is named as its remote.
func buttonName(remote, label string) string {
	label = cut(label, nameBytes)

	switch {
	case label == "":
		return cut(remote, nameBytes)
	case len(label) >= nameBytes-1:
		return label
	default:
		return cut(remote, nameBytes-len(label)-1) + " " + label
	}
}

// cut is text no longer than Matter lets it be, cut between characters.
func cut(text string, limit int) string {
	for len(text) > limit {
		_, size := utf8.DecodeLastRuneInString(text)
		text = text[:len(text)-size]
	}

	return text
}

// warn says an error of the node's that no one is waiting for.
func warn(ctx context.Context, id matter.ID, err error) {
	if err != nil {
		slog.WarnContext(ctx, "the bridge could not follow a device", "device", id, "error", err)
	}
}
