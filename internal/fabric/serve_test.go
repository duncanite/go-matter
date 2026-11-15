package fabric_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/clock"
	"github.com/SukramJ/go-fabric/cluster/spec"
	bridgeddef "github.com/SukramJ/go-fabric/cluster/spec/bridgeddevicebasicinformation"
	descriptordef "github.com/SukramJ/go-fabric/cluster/spec/descriptor"
	fixedlabeldef "github.com/SukramJ/go-fabric/cluster/spec/fixedlabel"
	levelcontroldef "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	switchdef "github.com/SukramJ/go-fabric/cluster/spec/switchcluster"
	"github.com/SukramJ/go-fabric/controller/client"
	"github.com/SukramJ/go-fabric/fabrictest"
	"github.com/SukramJ/go-fabric/node"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/fabric"
	"github.com/duncanite/go-matter/mattertest"
)

// rig is a home served on a site, and a controller that commissioned it.
type rig struct {
	site   *fabrictest.Site
	device *fabrictest.Device
	pair   *fabrictest.Pair
}

func serve(t *testing.T, home matter.Source) *rig {
	t.Helper()

	secret, err := node.NewCommissioningCredentials()
	if err != nil {
		t.Fatal(err)
	}

	site := fabrictest.NewSite(t, fabrictest.Options{Clock: clock.System{}})
	device := site.AddNode(fabric.NodeConfig(fabric.Identity{Name: "Test bridge"}, secret, "", nil, ""))

	serving := fabric.Serve(device.Node(), home)
	t.Cleanup(serving.Close)

	pair, err := site.AddController(fabrictest.ControllerOptions{}).
		Commission(t.Context(), device, fabrictest.CommissionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	return &rig{site: site, device: device, pair: pair}
}

// endpoint waits for a device of the home to be served, and gives where.
func (r *rig) endpoint(t *testing.T, key string) uint16 {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if number, served := r.device.EndpointOf(key); served {
			return number
		}
	}

	t.Fatalf("%s was never served", key)

	return 0
}

func (r *rig) invoke(ctx context.Context, endpoint uint16, cluster, command uint32, fields spec.Encodable) error {
	_, err := fabrictest.Invoke(ctx, r.pair, client.CommandAt(endpoint, cluster, command), fields)

	return err
}

// status is the Interaction Model status a command was answered with, 0
// for success.
func (r *rig) status(t *testing.T, endpoint uint16, cluster, command uint32, fields spec.Encodable) uint8 {
	t.Helper()

	err := r.invoke(t.Context(), endpoint, cluster, command, fields)
	if err == nil {
		return 0
	}

	var refused *fabrictest.StatusError
	if !errors.As(err, &refused) {
		t.Fatalf("a command failed with no status: %v", err)
	}

	return uint8(refused.Status)
}

// eventually waits for what is read over the wire to be so.
func (*rig) eventually(t *testing.T, what string, so func() bool) {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if so() {
			return
		}
	}

	t.Fatalf("never so: %s", what)
}

func (r *rig) on(t *testing.T, endpoint uint16) bool {
	t.Helper()

	values, err := fabrictest.ReadCluster(t.Context(), r.pair, endpoint, onoffdef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	state, _ := onoffdef.StateFrom(values)

	return state.OnOff
}

func (r *rig) level(t *testing.T, endpoint uint16) uint8 {
	t.Helper()

	values, err := fabrictest.ReadCluster(t.Context(), r.pair, endpoint, levelcontroldef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	state, _ := levelcontroldef.StateFrom(values)
	if state.CurrentLevel == nil {
		return 0
	}

	return *state.CurrentLevel
}

func (r *rig) reachable(t *testing.T, endpoint uint16) bool {
	t.Helper()

	values, err := fabrictest.ReadCluster(t.Context(), r.pair, endpoint, bridgeddef.ClusterID)
	if err != nil {
		return false
	}

	state, _ := bridgeddef.StateFrom(values)

	return state.Reachable
}

func (r *rig) kinds(t *testing.T, endpoint uint16) []uint32 {
	t.Helper()

	values, err := fabrictest.ReadCluster(t.Context(), r.pair, endpoint, descriptordef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	state, _ := descriptordef.StateFrom(values)
	kinds := make([]uint32, 0, len(state.DeviceTypeList))

	for _, kind := range state.DeviceTypeList {
		kinds = append(kinds, kind.DeviceType)
	}

	return kinds
}

func level(value uint8, tenths uint16) levelcontroldef.MoveToLevelRequest {
	return levelcontroldef.MoveToLevelRequest{Level: value, TransitionTime: spec.ValueOf(tenths)}
}

func levelOn(value uint8, tenths uint16) levelcontroldef.MoveToLevelWithOnOffRequest {
	return levelcontroldef.MoveToLevelWithOnOffRequest{Level: value, TransitionTime: spec.ValueOf(tenths)}
}

var errFuse = errors.New("a fuse")

const (
	onOff  = onoffdef.ClusterID
	levels = levelcontroldef.ClusterID
)

// TestCommandsReachTheDevice: each of Matter's commands as the device's
// own, and the device's state as Matter's attributes.
func TestCommandsReachTheDevice(t *testing.T) {
	t.Parallel()

	var home matter.Home

	lamp := mattertest.Dimmer(&home, "lamp")
	served := serve(t, &home)
	endpoint := served.endpoint(t, "d:lamp")

	if served.on(t, endpoint) || served.level(t, endpoint) != 254 {
		t.Fatalf("a dimmer off and at full is served on %t at %d", served.on(t, endpoint), served.level(t, endpoint))
	}

	for _, step := range []struct {
		name    string
		cluster uint32
		command uint32
		fields  spec.Encodable
		asked   mattertest.Ask
		on      bool
		level   uint8
	}{
		{"On", onOff, onoffdef.CmdOn, nil, mattertest.SetOn{On: true}, true, 254},
		{"a level, on", levels, levelcontroldef.CmdMoveToLevel, level(127, 20), mattertest.SetLevel{Level: 0.5, Over: 2 * time.Second}, true, 127},
		{"Toggle, on", onOff, onoffdef.CmdToggle, nil, mattertest.SetOn{On: false}, false, 127},
		{"a level, off, is kept", levels, levelcontroldef.CmdMoveToLevel, level(64, 0), nil, false, 127},
		{"On, at the level kept", onOff, onoffdef.CmdOn, nil, mattertest.SetLevel{Level: matter.Level(64) / 254}, true, 64},
		{"Off", onOff, onoffdef.CmdOff, nil, mattertest.SetOn{On: false}, false, 64},
		{"a level that switches on", levels, levelcontroldef.CmdMoveToLevelWithOnOff, levelOn(254, 5), mattertest.SetLevel{Level: 1, Over: 500 * time.Millisecond}, true, 254},
		{"the least level, switching, is off", levels, levelcontroldef.CmdMoveToLevelWithOnOff, levelOn(1, 0), mattertest.SetOn{On: false}, false, 254},
		{"Toggle, off", onOff, onoffdef.CmdToggle, nil, mattertest.SetOn{On: true}, true, 254},
		{"Move down", levels, levelcontroldef.CmdMove, levelcontroldef.MoveRequest{MoveMode: levelcontroldef.MoveModeDown}, mattertest.Move{Direction: matter.Down}, true, 3},
		{"Stop", levels, levelcontroldef.CmdStop, levelcontroldef.StopRequest{}, mattertest.Stop{}, true, 3},
		{"Move up, switching", levels, levelcontroldef.CmdMoveWithOnOff, levelcontroldef.MoveWithOnOffRequest{MoveMode: levelcontroldef.MoveModeUp}, mattertest.Move{Direction: matter.Up}, true, 254},
		{"Stop, switching", levels, levelcontroldef.CmdStopWithOnOff, levelcontroldef.StopWithOnOffRequest{}, mattertest.Stop{}, true, 254},
		{"no level, switching, is off", levels, levelcontroldef.CmdMoveToLevelWithOnOff, levelOn(0, 0), mattertest.SetOn{On: false}, false, 254},
		{"a level, off, is kept again", levels, levelcontroldef.CmdMoveToLevel, level(64, 0), nil, false, 254},
		{"Off forgets the level kept", onOff, onoffdef.CmdOff, nil, mattertest.SetOn{On: false}, false, 254},
		{"On, at the level it had", onOff, onoffdef.CmdOn, nil, mattertest.SetOn{On: true}, true, 254},
		{"off, whatever the effect", onOff, onoffdef.CmdOffWithEffect, onoffdef.OffWithEffectRequest{}, mattertest.SetOn{On: false}, false, 254},
		{"on, whatever the scene", onOff, onoffdef.CmdOnWithRecallGlobalScene, onoffdef.OnWithRecallGlobalSceneRequest{}, mattertest.SetOn{On: true}, true, 254},
	} {
		before := len(lamp.Asked())

		if status := served.status(t, endpoint, step.cluster, step.command, step.fields); status != 0 {
			t.Fatalf("%s: status %#x", step.name, status)
		}

		asked := lamp.Asked()

		switch {
		case step.asked == nil && len(asked) != before:
			t.Fatalf("%s: the lamp was asked %+v", step.name, asked[before:])
		case step.asked != nil && (len(asked) != before+1 || asked[before] != step.asked):
			t.Fatalf("%s: the lamp was asked %+v", step.name, asked[before:])
		}

		served.eventually(t, step.name, func() bool {
			return served.on(t, endpoint) == step.on && served.level(t, endpoint) == step.level
		})
	}

	// What no level is, and what is not served.
	for name, row := range map[string]struct {
		cluster uint32
		command uint32
		fields  spec.Encodable
		want    uint8
	}{
		"a level above full": {levels, levelcontroldef.CmdMoveToLevel, level(255, 0), 0x87},
		"Step":               {levels, levelcontroldef.CmdStep, levelcontroldef.StepRequest{StepSize: 10}, 0x81},
		"Step, switching":    {levels, levelcontroldef.CmdStepWithOnOff, levelcontroldef.StepWithOnOffRequest{StepSize: 10}, 0x81},
		"on for a time":      {onOff, onoffdef.CmdOnWithTimedOff, onoffdef.OnWithTimedOffRequest{OnTime: 20}, 0x81},
	} {
		before := len(lamp.Asked())

		if status := served.status(t, endpoint, row.cluster, row.command, row.fields); status != row.want {
			t.Errorf("%s: status %#x", name, status)
		}

		if len(lamp.Asked()) != before {
			t.Errorf("%s reached the lamp", name)
		}
	}

	// And none of them moved what is served: the lamp is on, as it was.
	time.Sleep(200 * time.Millisecond)

	if !served.on(t, endpoint) {
		t.Fatal("a command that was refused switched what is served")
	}
}

// TestAnErrorCrosses: what the device refuses is the status the
// controller is answered with.
func TestAnErrorCrosses(t *testing.T) {
	t.Parallel()

	var home matter.Home

	lamp := mattertest.Dimmer(&home, "lamp")
	served := serve(t, &home)
	endpoint := served.endpoint(t, "d:lamp")

	for name, row := range map[string]struct {
		fail error
		want uint8
	}{
		"invalid":     {matter.ErrInvalid, 0x87},
		"busy":        {matter.ErrBusy, 0x9C},
		"unreachable": {matter.ErrUnreachable, 0x94},
		"failed":      {matter.ErrFailed, 0x01},
		"its own":     {errFuse, 0x01},
		"wrapped":     {errors.Join(errFuse, matter.ErrBusy), 0x9C},
	} {
		lamp.Fail(row.fail)

		for command, fields := range map[uint32]spec.Encodable{
			onoffdef.CmdOn: nil, onoffdef.CmdOff: nil, onoffdef.CmdToggle: nil,
		} {
			if status := served.status(t, endpoint, onOff, command, fields); status != row.want {
				t.Errorf("%s, OnOff command %d: status %#x", name, command, status)
			}
		}

		for command, fields := range map[uint32]spec.Encodable{
			levelcontroldef.CmdMoveToLevelWithOnOff: levelOn(100, 0),
			levelcontroldef.CmdMove:                 levelcontroldef.MoveRequest{},
			levelcontroldef.CmdStop:                 levelcontroldef.StopRequest{},
		} {
			if status := served.status(t, endpoint, levels, command, fields); status != row.want {
				t.Errorf("%s, LevelControl command %d: status %#x", name, command, status)
			}
		}
	}

	// Held unreachable by the home: the same status, and the lamp not
	// asked.
	lamp.Fail(nil)

	if err := home.Report("lamp", matter.ReachabilityChanged{Unreachable: true}); err != nil {
		t.Fatal(err)
	}

	before := len(lamp.Asked())

	if status := served.status(t, endpoint, onOff, onoffdef.CmdOn, nil); status != 0x94 || len(lamp.Asked()) != before {
		t.Fatalf("an unreachable lamp: status %#x, asked %+v", status, lamp.Asked()[before:])
	}

	served.eventually(t, "the lamp is shown unreachable", func() bool {
		values, err := fabrictest.ReadCluster(t.Context(), served.pair, endpoint, bridgeddef.ClusterID)
		if err != nil {
			t.Fatal(err)
		}

		state, _ := bridgeddef.StateFrom(values)

		return !state.Reachable
	})
}

// TestWhatHappensAtTheWall: the source's events as Matter's attributes
// and events.
func TestWhatHappensAtTheWall(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Dimmer(&home, "lamp")

	if err := home.Put(matter.Device{
		ID: "pico", Name: "Pico", Kind: matter.Remote, Room: "Hall",
		Buttons: []matter.Button{{Label: "On"}, {Label: "Off"}},
	}); err != nil {
		t.Fatal(err)
	}

	served := serve(t, &home)
	lamp := served.endpoint(t, "d:lamp")

	if err := home.Report("lamp", matter.OnOffChanged{On: true}, matter.LevelChanged{Level: 0.25}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the lamp on at a quarter", func() bool {
		return served.on(t, lamp) && served.level(t, lamp) == 64
	})

	// The least a level can be is Matter's least, never its zero.
	if err := home.Report("lamp", matter.LevelChanged{Level: 0.0001}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the lamp at the least level", func() bool { return served.level(t, lamp) == 1 })

	// A remote is a switch for each button, named by both.
	first, second := served.endpoint(t, "b:0:pico"), served.endpoint(t, "b:1:pico")

	for endpoint, name := range map[uint16]string{first: "Pico On", second: "Pico Off"} {
		values, err := fabrictest.ReadCluster(t.Context(), served.pair, endpoint, bridgeddef.ClusterID)
		if err != nil {
			t.Fatal(err)
		}

		if state, _ := bridgeddef.StateFrom(values); state.NodeLabel != name || !state.Reachable {
			t.Fatalf("a button is served as %+v", state)
		}
	}

	if err := home.Report("pico", matter.ButtonPressed{Button: 1}, matter.ButtonReleased{Button: 1}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the second button's press and release", func() bool {
		pressed, err := fabrictest.Events(
			t.Context(),
			served.pair,
			client.EventAt(second, switchdef.ClusterID, switchdef.EventInitialPress),
		)
		if err != nil {
			t.Fatal(err)
		}

		released, err := fabrictest.Events(
			t.Context(),
			served.pair,
			client.EventAt(second, switchdef.ClusterID, switchdef.EventShortRelease),
		)
		if err != nil {
			t.Fatal(err)
		}

		return len(pressed) == 1 && len(released) == 1
	})

	untouched, err := fabrictest.Events(
		t.Context(),
		served.pair,
		client.EventAt(first, switchdef.ClusterID, switchdef.EventInitialPress),
	)
	if err != nil || len(untouched) != 0 {
		t.Fatalf("the first button heard the second's press: %v, %v", untouched, err)
	}
}

// TestDevicesComeAndGo: what each kind is served as, a device added and
// removed while the bridge serves, and what is said of it.
func TestDevicesComeAndGo(t *testing.T) {
	t.Parallel()

	var home matter.Home

	served := serve(t, &home)

	mattertest.Light(&home, "light")
	mattertest.Dimmer(&home, "dimmer")
	mattertest.Plug(&home, "plug")

	set := func(context.Context, bool) error { return nil }
	setLevel := func(context.Context, matter.Level, time.Duration) error { return nil }

	if err := home.Put(matter.Device{
		ID: "outlet", Name: "A name that is far longer than Matter lets a name be", Kind: matter.Plug,
		Room: "The conservatory, east", Vendor: "Maker", Unreachable: true,
		OnOff: &matter.OnOff{On: true, Set: set}, LevelControl: &matter.LevelControl{Level: 0.5, Set: setLevel},
	}); err != nil {
		t.Fatal(err)
	}

	for key, kind := range map[string]uint32{"light": 0x0100, "dimmer": 0x0101, "plug": 0x010A, "outlet": 0x010B} {
		endpoint := served.endpoint(t, "d:"+key)

		kinds := served.kinds(t, endpoint)
		if len(kinds) != 2 || (kinds[0] != kind && kinds[1] != kind) || (kinds[0] != 0x0013 && kinds[1] != 0x0013) {
			t.Errorf("%s is served as %#x", key, kinds)
		}
	}

	// As it is put: unreachable, on, at half, named and placed within
	// Matter's bounds.
	outlet := served.endpoint(t, "d:outlet")

	served.eventually(t, "a device put unreachable is shown so", func() bool { return !served.reachable(t, outlet) })

	if err := home.Report("outlet", matter.ReachabilityChanged{}); err != nil {
		t.Fatal(err)
	}

	served.eventually(t, "the outlet back", func() bool { return served.reachable(t, outlet) })

	if !served.on(t, outlet) || served.level(t, outlet) != 127 {
		t.Fatalf("the outlet is served on %t at %d", served.on(t, outlet), served.level(t, outlet))
	}

	values, err := fabrictest.ReadCluster(t.Context(), served.pair, outlet, bridgeddef.ClusterID)
	if err != nil {
		t.Fatal(err)
	}

	if state, _ := bridgeddef.StateFrom(
		values,
	); state.NodeLabel != "A name that is far longer than M" ||
		state.VendorName != "Maker" {
		t.Fatalf("the outlet is said to be %+v", state)
	}

	if values, err = fabrictest.ReadCluster(t.Context(), served.pair, outlet, fixedlabeldef.ClusterID); err != nil {
		t.Fatal(err)
	}

	labels, _ := fixedlabeldef.StateFrom(values)
	if len(labels.LabelList) != 1 || labels.LabelList[0].Label != "room" ||
		labels.LabelList[0].Value != "The conservatory" {
		t.Fatalf("the outlet's room is %+v", labels.LabelList)
	}

	// A device without Move and Stop answers that it has none.
	for _, command := range []uint32{levelcontroldef.CmdMove, levelcontroldef.CmdStop} {
		if status := served.status(t, outlet, levels, command, levelcontroldef.MoveRequest{}); status != 0x81 {
			t.Errorf("command %d of a device that does not move: status %#x", command, status)
		}
	}

	// Removed, it is served no more; put again, it is where it was.
	home.Remove("outlet")

	served.eventually(t, "the outlet gone", func() bool {
		_, still := served.device.EndpointOf("d:outlet")

		return !still
	})

	mattertest.Plug(&home, "outlet")

	if again := served.endpoint(t, "d:outlet"); again != outlet {
		t.Fatalf("the outlet came back at endpoint %d, from %d", again, outlet)
	}

	if kinds := served.kinds(t, outlet); kinds[0] != 0x010A && kinds[1] != 0x010A {
		t.Fatalf("the outlet came back as %#x", kinds)
	}
}

// TestIDsAreTheSources: an ID may be anything, a button's name is never
// another's, and a change a device cannot have is dropped.
func TestIDsAreTheSources(t *testing.T) {
	t.Parallel()

	set := func(context.Context, bool) error { return nil }
	long := "A remote whose name is all the room there is"

	served := serve(t, told{
		devices: []matter.Device{
			{ID: "r", Name: long, Kind: matter.Remote, Buttons: []matter.Button{{Label: "On"}, {Label: "Off"}}},
			{ID: "r#0", Name: "light", Kind: matter.Light, OnOff: &matter.OnOff{Set: set}},
			{ID: "b:0:r", Name: "plug", Kind: matter.Plug, OnOff: &matter.OnOff{Set: set}},
			{
				ID: "dim", Name: "dimmer", Kind: matter.Light, OnOff: &matter.OnOff{Set: set},
				LevelControl: &matter.LevelControl{
					Level: 0.5,
					Set:   func(context.Context, matter.Level, time.Duration) error { return nil },
				},
			},
			{
				ID: "long", Name: "Remote", Kind: matter.Remote,
				Buttons: []matter.Button{{Label: "A label that is as long as a name may be"}, {}},
			},
		},
		// None of these is a change its device can have.
		then: []matter.Event{
			{Device: "r", Change: matter.OnOffChanged{On: true}},
			{Device: "b:0:r", Change: matter.LevelChanged{Level: 0.5}},
			{Device: "r#0", Change: matter.LevelChanged{Level: 0.5}},
			{Device: "r", Change: matter.ButtonPressed{Button: 7}},
			{Device: "dim", Change: matter.LevelChanged{Level: 7}},
			{Device: "dim", Change: matter.LevelChanged{}},
			{Device: "nobody", Change: matter.OnOffChanged{On: true}},
			{Device: "r#0", Change: matter.OnOffChanged{On: true}},
		},
	})

	// Three devices, four endpoints, none taking another's place.
	first, second := served.endpoint(t, "b:0:r"), served.endpoint(t, "b:1:r")
	light, plug := served.endpoint(t, "d:r#0"), served.endpoint(t, "d:b:0:r")

	if first == second || first == light || first == plug || second == light || second == plug || light == plug {
		t.Fatalf("endpoints %d %d %d %d", first, second, light, plug)
	}

	names := map[string]bool{}

	for _, endpoint := range []uint16{first, second} {
		values, err := fabrictest.ReadCluster(t.Context(), served.pair, endpoint, bridgeddef.ClusterID)
		if err != nil {
			t.Fatal(err)
		}

		state, _ := bridgeddef.StateFrom(values)
		names[state.NodeLabel] = true

		if len(state.NodeLabel) > 32 {
			t.Fatalf("a button is named %q", state.NodeLabel)
		}
	}

	if !names["A remote whose name is all th On"] || !names["A remote whose name is all t Off"] {
		t.Fatalf("the buttons are named %v", names)
	}

	// The last change, one its device can have, arrived: the others were
	// dropped, and the watching went on.
	served.eventually(t, "the light on", func() bool { return served.on(t, light) })

	if served.on(t, plug) {
		t.Fatal("a change told of no device switched the plug")
	}

	// A level that is none left the dimmer's as it was.
	if at := served.level(t, served.endpoint(t, "d:dim")); at != 127 {
		t.Fatalf("a level that is none moved the dimmer to %d", at)
	}

	// A label as long as a name is the whole of it, and a button with
	// none is named as its remote.
	for key, want := range map[string]string{"b:0:long": "A label that is as long as a nam", "b:1:long": "Remote"} {
		values, err := fabrictest.ReadCluster(t.Context(), served.pair, served.endpoint(t, key), bridgeddef.ClusterID)
		if err != nil {
			t.Fatal(err)
		}

		if state, _ := bridgeddef.StateFrom(values); state.NodeLabel != want {
			t.Errorf("%s is named %q", key, state.NodeLabel)
		}
	}
}

// told is a source that tells of devices, then of events, as given.
type told struct {
	devices []matter.Device
	then    []matter.Event
}

func (s told) Devices(context.Context) ([]matter.Device, error) { return s.devices, nil }

func (s told) Watch(ctx context.Context, handle func(matter.Event)) error {
	for _, device := range s.devices {
		handle(matter.Event{Device: device.ID, Change: matter.DeviceAdded{Device: device}})
	}

	for _, event := range s.then {
		handle(event)
	}

	<-ctx.Done()

	return ctx.Err()
}

// TestCommandsWhileTheWallMoves: commands that read how a device stands,
// while its source says it changes, are each answered.
func TestCommandsWhileTheWallMoves(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Dimmer(&home, "lamp")

	served := serve(t, &home)
	endpoint := served.endpoint(t, "d:lamp")

	moving, stop := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		for round := 0; moving.Err() == nil; round++ {
			_ = home.Report("lamp",
				matter.OnOffChanged{On: round%2 == 0},
				matter.LevelChanged{Level: matter.Level(round%100+1) / 100},
			)

			time.Sleep(time.Millisecond)
		}
	}()

	for range 40 {
		if status := served.status(t, endpoint, onOff, onoffdef.CmdToggle, nil); status != 0 {
			t.Fatalf("Toggle while the wall moves: status %#x", status)
		}

		if status := served.status(t, endpoint, levels, levelcontroldef.CmdMoveToLevel, level(100, 0)); status != 0 {
			t.Fatalf("a level while the wall moves: status %#x", status)
		}
	}

	stop()
	<-done
}
