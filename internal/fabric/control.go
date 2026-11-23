package fabric

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/go-fabric/client/exchange"
	"github.com/SukramJ/go-fabric/client/handshake"
	"github.com/SukramJ/go-fabric/cluster/spec"
	basicdef "github.com/SukramJ/go-fabric/cluster/spec/basicinformation"
	bridgeddef "github.com/SukramJ/go-fabric/cluster/spec/bridgeddevicebasicinformation"
	descriptordef "github.com/SukramJ/go-fabric/cluster/spec/descriptor"
	fancontroldef "github.com/SukramJ/go-fabric/cluster/spec/fancontrol"
	fixedlabeldef "github.com/SukramJ/go-fabric/cluster/spec/fixedlabel"
	levelcontroldef "github.com/SukramJ/go-fabric/cluster/spec/levelcontrol"
	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	switchdef "github.com/SukramJ/go-fabric/cluster/spec/switchcluster"
	"github.com/SukramJ/go-fabric/controller"
	"github.com/SukramJ/go-fabric/controller/attestation"
	"github.com/SukramJ/go-fabric/controller/client"
	"github.com/SukramJ/go-fabric/controller/commissioner"
	"github.com/SukramJ/go-fabric/controller/dcl/snapshot"
	"github.com/SukramJ/go-fabric/im"

	"github.com/duncanite/go-matter"
)

// The device types the vocabulary has a word for.
const (
	typeOnOffLight      uint32 = 0x0100
	typeDimmableLight   uint32 = 0x0101
	typeColorTempLight  uint32 = 0x010C
	typeExtendedColor   uint32 = 0x010D
	typeOnOffPlug       uint32 = 0x010A
	typeDimmablePlug    uint32 = 0x010B
	typeGenericSwitch   uint32 = 0x000F
	typeFan             uint32 = 0x002B
	typeAirPurifier     uint32 = 0x002D
	controllerStateFile        = "controller.db"
	maxTransition              = math.MaxUint16 * tenth
	seeding                    = 30 * time.Second
	closing                    = 10 * time.Second
)

// Trust is whose devices a controller commissions.
type Trust int

// The trusts.
const (
	TrustCertified Trust = iota
	TrustDevelopment
	TrustAnything
)

// The errors of commissioning.
var (
	ErrNotFound  = errors.New("no device answered to that setup code")
	ErrSetupCode = errors.New("the setup code is not valid")
	ErrUntrusted = errors.New("the device's attestation is not trusted")
)

// Controller is a controller of the library and the devices of the nodes
// it holds, kept in one home.
type Controller struct {
	// over is nil until the library's controller is made: its hooks run
	// from inside the making, and find nothing to follow yet.
	over  atomic.Pointer[controller.Controller]
	db    *sql.DB
	trust Trust
	home  matter.Home

	// life is held, shared, by everything that uses the library's
	// controller, and alone by Close: what is under way when Close begins
	// ends first, and what comes after finds the controller closed and
	// does not touch a registry that is no longer there. It is only ever
	// tried, never waited for: a hook the library calls from inside its
	// own closing returns at once.
	life sync.RWMutex

	mu     sync.Mutex
	closed bool
	// gone ends the watches of a node when it is given up, by its id in
	// the fabric, and under zero those of the controller when it closes.
	// A node's stays, closed, once it is given up: a watch of it that
	// starts after ends at once.
	gone map[uint64]chan struct{}
	// unpaired is the nodes given up, by their id in the fabric.
	unpaired map[uint64]bool

	// following is held while the home is brought to a node's image, and
	// while the devices of a node given up are removed: a following under
	// way does not put back what is removed.
	following sync.Mutex
}

// OpenController makes a controller in dir and puts it on the network.
// On Windows it is errors.ErrUnsupported. To find devices the library
// asks the system to say, of each packet, which interface it came in
// on, before it joins the groups devices announce themselves on;
// golang.org/x/net does not implement that there, and the library gives
// up. A controller that cannot find a device can neither commission one
// nor find it again.
func OpenController(ctx context.Context, name, dir string, trust Trust) (*Controller, error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf(
			"%w: a controller does not run on Windows, where devices cannot be found", errors.ErrUnsupported,
		)
	}

	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("making %s: %w", dir, err)
	}

	store, err := Trusted(trust)
	if err != nil {
		return nil, err
	}

	database, err := controller.OpenDB(filepath.Join(dir, controllerStateFile))
	if err != nil {
		return nil, fmt.Errorf("opening the controller's state: %w", err)
	}

	made, err := NewController(
		ctx,
		controller.Config{DB: database, Attestation: store, Label: cut(name, nameBytes)},
		trust,
	)
	if err != nil {
		_ = database.Close()

		return nil, err
	}

	made.db = database

	return made, nil
}

// NewController makes a controller from a configuration of the
// library's, which a test wires to a network of its own. The hooks and
// the default subscription are this package's.
func NewController(ctx context.Context, cfg controller.Config, trust Trust) (*Controller, error) {
	made := &Controller{trust: trust, gone: map[uint64]chan struct{}{}, unpaired: map[uint64]bool{}}

	// A hook can run before New has returned, from inside it, for a node
	// the controller already held: it finds no controller and returns.
	// Every node is followed once New is back.
	cfg.HooksFor = func(local string) client.Hooks { //nolint:contextcheck // a node's hooks have no context to pass
		follow := func() { made.follow(local) }

		return client.Hooks{
			Seeded:                 follow,
			Changed:                follow,
			ConnectionStateChanged: func(client.ConnectionState) { follow() },
			Event: func(endpoint uint16, cluster, event uint32, _ client.EventData, _ any) {
				made.heard(local, endpoint, cluster, event)
			},
		}
	}

	over, err := controller.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("making the controller: %w", err)
	}

	made.over.Store(over)

	for _, held := range over.Nodes() {
		made.follow(held.ID()) //nolint:contextcheck // as from a node's hook, which has none
	}

	return made, nil
}

// Trusted is the roots a trust accepts. Anything still needs a store: it
// is the pairing that then passes over what the store refuses.
func Trusted(trust Trust) (attestation.PAAStore, error) {
	if trust == TrustCertified {
		store, err := controller.ProductionTrust()
		if err != nil {
			return nil, fmt.Errorf("reading the certified makers' roots: %w", err)
		}

		return store, nil
	}

	embedded, err := snapshot.Embedded()
	if err != nil {
		return nil, fmt.Errorf("reading the certified makers' roots: %w", err)
	}

	return attestation.Trust(attestation.Development, embedded, nil), nil
}

// Close takes the controller off the network. What is under way when it
// is called ends first.
func (c *Controller) Close() error {
	c.life.Lock()
	defer c.life.Unlock()

	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()

		return matter.ErrClosed
	}

	c.closed = true

	for node, gone := range c.gone {
		if !c.unpaired[node] {
			close(gone)
		}
	}

	c.gone = nil
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), closing)
	defer cancel()

	err := c.over.Load().Close(ctx)

	if c.db != nil {
		err = errors.Join(err, c.db.Close())
	}

	if err != nil {
		return fmt.Errorf("closing the controller: %w", err)
	}

	return nil
}

// Pair commissions the node that answers to a setup code.
func (c *Controller) Pair(ctx context.Context, code string) (*client.Node, error) {
	over, leave, err := c.enter()
	if err != nil {
		return nil, err
	}

	defer leave()

	options := controller.PairOptions{}

	switch c.trust {
	case TrustCertified:
	case TrustDevelopment:
		options.OnAttestationFailure = attestation.Policy{Decide: onlyForTests}
	case TrustAnything:
		options.OnAttestationFailure = attestation.Accept()
	}

	paired, err := over.Pair(ctx, strings.TrimSpace(code), options)

	switch {
	case err == nil:
	case errors.Is(err, controller.ErrPairingCode), errors.Is(err, handshake.ErrPasscodeMismatch):
		return nil, fmt.Errorf("%w: %w", ErrSetupCode, err)
	case errors.Is(err, controller.ErrNoDevice):
		return nil, fmt.Errorf("%w: %w", ErrNotFound, err)
	case errors.Is(err, commissioner.ErrAttestationRejected):
		return nil, fmt.Errorf("%w: %w", ErrUntrusted, err)
	default:
		return nil, fmt.Errorf("commissioning: %w", err)
	}

	// A node is waited for until its devices are known and it answers,
	// so that Devices of a node just paired is not empty, nor
	// unreachable, for want of having waited. One that does neither in
	// time is held all the same: it is commissioned, and its devices
	// come when it says them.
	waiting, cancel := context.WithTimeout(ctx, seeding)
	defer cancel()

	for !paired.Seeded() || paired.ConnectionState() != client.Connected {
		select {
		case <-waiting.Done():
			slog.WarnContext(ctx, "a node was commissioned and has not yet said what it holds")

			return paired, nil
		case <-time.After(20 * time.Millisecond):
		}
	}

	c.follow(paired.ID()) //nolint:contextcheck // as from a node's hook, which has none

	return paired, nil
}

// Nodes is the nodes the controller holds.
func (c *Controller) Nodes() []*client.Node {
	over, leave, err := c.enter()
	if err != nil {
		return nil
	}

	defer leave()

	return over.Nodes()
}

// onlyForTests lets a pairing go on when all that was found is that the
// device is certified for testing: what Matter's development identity is,
// and nothing a product would show.
func onlyForTests(findings []attestation.Finding) (proceed bool, reason string, err error) {
	for _, found := range findings {
		if found.Check != attestation.CertificationTypeTest && found.Check != attestation.TrustedAsTestCertificate {
			return false, "", nil
		}
	}

	return true, "", nil
}

// NodeID is a node's id in the controller's fabric; zero for a node not
// commissioned.
func NodeID(held *client.Node) uint64 {
	address, _ := held.PeerAddress()

	return address.NodeID
}

// Described is what a node says of itself.
type Described struct{ Name, Vendor, Product, Serial, Firmware string }

// Describe is what a node says of itself.
func Describe(held *client.Node) Described {
	root := held.Endpoint(0)
	if root == nil {
		return Described{}
	}

	said, _ := basicdef.StateFrom(root.Cluster(basicdef.ClusterID))

	return Described{
		Name: said.NodeLabel, Vendor: said.VendorName, Product: said.ProductName,
		Serial: said.SerialNumber, Firmware: said.SoftwareVersionString,
	}
}

// Invite opens a node to another administrator.
func (c *Controller) Invite(
	ctx context.Context,
	held *client.Node,
	within time.Duration,
) (manual, qr string, err error) {
	_, leave, err := c.enter()
	if err != nil {
		return "", "", err
	}

	defer leave()

	if within <= 0 {
		return "", "", fmt.Errorf("%w: an invitation of %s", matter.ErrInvalid, within)
	}

	window, err := held.OpenCommissioningWindow(ctx, within, client.WindowOptions{})
	if err != nil {
		return "", "", fmt.Errorf("opening the node: %w", answered(err))
	}

	return window.ManualCode, window.QRCode, nil
}

// Unpair gives a node up: told to forget this controller when it can be,
// forgotten here either way.
func (c *Controller) Unpair(ctx context.Context, held *client.Node, given uint64) error {
	over, leave, err := c.enter()
	if err != nil {
		return err
	}

	defer leave()

	prefix := prefixFor(given)

	// Marked first, and by the one that marks alone: the node's last
	// words, said inside the call that gives it up, are not followed, and
	// a node is given up once. The call itself is made with no lock of
	// this package's held, since those words are said from inside it.
	c.mu.Lock()

	if c.unpaired[given] {
		c.mu.Unlock()

		return matter.ErrClosed
	}

	c.unpaired[given] = true
	c.mu.Unlock()

	if err = over.Registry().Decommission(ctx, held); err != nil {
		slog.WarnContext(ctx, "a node could not be told it is given up, and is forgotten", "error", err)

		// Forgetting is this side's alone, and has its own time: the
		// caller's may have run out waiting for the node.
		forgetting, cancel := context.WithTimeout(context.WithoutCancel(ctx), closing)
		defer cancel()

		if err = over.Registry().Forget(forgetting, held); err != nil {
			c.mu.Lock()
			delete(c.unpaired, given)
			c.mu.Unlock()

			return fmt.Errorf("forgetting the node: %w", err)
		}
	}

	c.mu.Lock()

	gone, watched := c.gone[given]
	if !watched {
		gone = make(chan struct{})
		c.gone[given] = gone
	}

	close(gone)
	c.mu.Unlock()

	// A following that was under way has ended once this lock is had,
	// and none starts after the mark: what is removed stays removed.
	c.following.Lock()
	defer c.following.Unlock()

	devices, _ := c.home.Devices(ctx)
	for index := range devices {
		if strings.HasPrefix(string(devices[index].ID), prefix) {
			c.home.Remove(devices[index].ID)
		}
	}

	return nil
}

// Devices is the devices of every node, or of one.
func (c *Controller) Devices(ctx context.Context, node *client.Node) ([]matter.Device, error) {
	if err := c.open(); err != nil {
		return nil, err
	}

	all, err := c.home.Devices(ctx)
	if err != nil || node == nil {
		return all, err //nolint:wrapcheck // the home's answer is this Source's own
	}

	prefix := prefixOf(node)
	theirs := all[:0]

	for index := range all {
		if strings.HasPrefix(string(all[index].ID), prefix) {
			theirs = append(theirs, all[index])
		}
	}

	return theirs, nil
}

// Watch is a Source's Watch over every node, or over the one of an id.
// It ends with matter.ErrClosed when the controller closes, or that node
// is given up, before or after it started.
func (c *Controller) Watch(ctx context.Context, node uint64, handle func(matter.Event)) error {
	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()

		return matter.ErrClosed
	}

	prefix := ""
	if node != 0 {
		prefix = prefixFor(node)
	}

	gone, known := c.gone[node]
	if !known {
		gone = make(chan struct{})
		c.gone[node] = gone
	}

	c.mu.Unlock()

	watching, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-gone:
			cancel()
		case <-watching.Done():
		}
	}()

	err := c.home.Watch(watching, func(event matter.Event) {
		if strings.HasPrefix(string(event.Device), prefix) {
			handle(event)
		}
	})

	if ctx.Err() == nil {
		return matter.ErrClosed
	}

	return err //nolint:wrapcheck // the context's own error is what Watch promises
}

// PairNode commissions the node that answers to a setup code, and holds
// it.
func (c *Controller) PairNode(ctx context.Context, code string) (*Node, error) {
	held, err := c.Pair(ctx, code)
	if err != nil {
		return nil, err
	}

	return &Node{of: c, held: held, id: NodeID(held)}, nil
}

// HeldNodes is the nodes the controller holds.
func (c *Controller) HeldNodes() []*Node {
	held := c.Nodes()
	nodes := make([]*Node, 0, len(held))

	for _, one := range held {
		nodes = append(nodes, &Node{of: c, held: one, id: NodeID(one)})
	}

	return nodes
}

// enter is the library's controller, for as long as leave is not
// called: Close waits for it. It is matter.ErrClosed once Close has
// begun, and before the library's controller is made.
func (c *Controller) enter() (over *controller.Controller, leave func(), err error) {
	if !c.life.TryRLock() {
		return nil, nil, matter.ErrClosed
	}

	over = c.over.Load()

	if err = c.open(); err != nil || over == nil {
		c.life.RUnlock()

		return nil, nil, matter.ErrClosed
	}

	return over, c.life.RUnlock, nil
}

// open says whether the controller can still be asked.
func (c *Controller) open() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return matter.ErrClosed
	}

	return nil
}

// prefixOf is what the IDs of a node's devices begin with: the node's
// id, which no other node has.
func prefixOf(held *client.Node) string {
	return prefixFor(NodeID(held))
}

// prefixFor is prefixOf for a node's id.
func prefixFor(node uint64) string {
	return fmt.Sprintf("%016x/", node)
}

// follow brings the home to what a node's image says: its devices, how
// each stands, and whether the node answers.
func (c *Controller) follow(local string) {
	over, leave, err := c.enter()
	if err != nil {
		return
	}

	defer leave()

	c.following.Lock()
	defer c.following.Unlock()

	held := over.Registry().Get(local)
	if held == nil || !held.Seeded() {
		return
	}

	// A node with no id is one being given up: the library says its last
	// words from inside that call, after it has taken the id away and
	// before it lets go of the node. Followed, its devices would be put
	// back under no node's name, where nothing removes them. The id is
	// read once: asked again, it may be gone by then.
	node := NodeID(held)
	if node == 0 {
		return
	}

	prefix := prefixFor(node)
	answering := held.ConnectionState() == client.Connected
	seen := map[matter.ID]bool{}

	for _, endpoint := range held.Endpoints() {
		described, is := c.describe(held, endpoint, prefix)
		if !is {
			continue
		}

		described.Unreachable = described.Unreachable || !answering
		seen[described.ID] = true

		c.keep(described)
	}

	devices, _ := c.home.Devices(context.Background())
	for index := range devices {
		if id := devices[index].ID; strings.HasPrefix(string(id), prefix) && !seen[id] {
			c.home.Remove(id)
		}
	}
}

// keep puts a device in the home, or says how it now stands when the
// home holds it as it is described.
func (c *Controller) keep(described matter.Device) {
	held, known := c.home.Device(described.ID)

	// A remote is one button on this side, always: what can differ is
	// what is said of a device, whether it has a level, and what its fan
	// can do.
	if !known || held.Info != described.Info || (held.LevelControl == nil) != (described.LevelControl == nil) ||
		fanShape(&held) != fanShape(&described) {
		if err := c.home.Put(described); err != nil {
			slog.Warn("a device of a node is left out", "error", err)
		}

		return
	}

	changes := []matter.Change{matter.ReachabilityChanged{Unreachable: described.Unreachable}}

	if described.OnOff != nil {
		changes = append(changes, matter.OnOffChanged{On: described.OnOff.On})
	}

	if described.LevelControl != nil {
		changes = append(changes, matter.LevelChanged{Level: described.LevelControl.Level})
	}

	if fan := described.FanControl; fan != nil {
		changes = append(changes, matter.FanChanged{
			Mode: fan.Mode, Speed: fan.Speed, Running: fan.Running,
			Oscillating: fan.Oscillating != nil && *fan.Oscillating, Sleep: fan.Sleep != nil && *fan.Sleep,
		})
	}

	if err := c.home.Report(described.ID, changes...); err != nil {
		slog.Warn("a device of a node could not be followed", "error", err)
	}
}

// describe is an endpoint as a device, when it is of a kind the
// vocabulary has a word for.
func (c *Controller) describe(held *client.Node, endpoint *client.Endpoint, prefix string) (matter.Device, bool) {
	said, _ := descriptordef.StateFrom(endpoint.Cluster(descriptordef.ClusterID))

	kind, dims, known := kindOf(said.DeviceTypeList)
	if !known {
		return matter.Device{}, false
	}

	number := endpoint.Number()
	described := matter.Device{ID: matter.ID(fmt.Sprintf("%s%d", prefix, number)), Kind: kind}

	// Behind a bridge a device says of itself; alone, its node does.
	if bridged := endpoint.Cluster(bridgeddef.ClusterID); bridged != nil {
		about, _ := bridgeddef.StateFrom(bridged)
		described.Name, described.Vendor, described.Model = about.NodeLabel, about.VendorName, about.ProductName
		described.Serial, described.Firmware = about.SerialNumber, about.SoftwareVersionString
		described.Unreachable = !about.Reachable
	} else {
		about := Describe(held)
		described.Name, described.Vendor, described.Model = about.Name, about.Vendor, about.Product
		described.Serial, described.Firmware = about.Serial, about.Firmware
	}

	labels, _ := fixedlabeldef.StateFrom(endpoint.Cluster(fixedlabeldef.ClusterID))
	for _, label := range labels.LabelList {
		if label.Label == "room" {
			described.Room = label.Value
		}
	}

	if kind == matter.Remote {
		described.Buttons = []matter.Button{{Label: described.Name}}

		return described, true
	}

	if kind == matter.Fan || kind == matter.AirPurifier {
		described.FanControl = c.fan(held, endpoint, described.ID)

		return described, true
	}

	stands, _ := onoffdef.StateFrom(endpoint.Cluster(onoffdef.ClusterID))
	described.OnOff = &matter.OnOff{On: stands.OnOff, Set: c.setOn(held, number)}

	if level := endpoint.Cluster(levelcontroldef.ClusterID); dims && level != nil {
		at, _ := levelcontroldef.StateFrom(level)

		current := levelFull
		if at.CurrentLevel != nil {
			current = *at.CurrentLevel
		}

		described.LevelControl = &matter.LevelControl{
			Level: fromMatter(current),
			Set:   c.setLevel(held, number),
			Move:  c.move(held, number),
			Stop:  c.stop(held, number),
		}
	}

	return described, true
}

// kindOf is the kind of device an endpoint's device types make it,
// whether it has a level, and whether the vocabulary has a word for it
// at all.
func kindOf(types []descriptordef.DeviceTypeStruct) (kind matter.Kind, dims, known bool) {
	for _, listed := range types {
		switch listed.DeviceType {
		case typeOnOffLight:
			kind, known = matter.Light, true
		case typeDimmableLight, typeColorTempLight, typeExtendedColor:
			kind, dims, known = matter.Light, true, true
		case typeOnOffPlug:
			kind, known = matter.Plug, true
		case typeDimmablePlug:
			kind, dims, known = matter.Plug, true, true
		case typeGenericSwitch:
			kind, known = matter.Remote, true
		case typeFan:
			kind, known = matter.Fan, true
		case typeAirPurifier:
			kind, known = matter.AirPurifier, true
		}
	}

	return kind, dims, known
}

// fanShape is what a device's fan can do, which does not change while
// the device is the same: whether it has one, its steps, and whether it
// oscillates and sleeps.
func fanShape(described *matter.Device) [4]int {
	fan := described.FanControl
	if fan == nil {
		return [4]int{}
	}

	shape := [4]int{1, fan.Steps, 0, 0}

	if fan.Oscillating != nil {
		shape[2] = 1
	}

	if fan.Sleep != nil {
		shape[3] = 1
	}

	return shape
}

// fan is an endpoint's fan. Matter says no speed of a fan that is off or
// left to itself: it then has the speed it last said here, and full when
// it said none.
func (c *Controller) fan(held *client.Node, endpoint *client.Endpoint, device matter.ID) *matter.FanControl {
	said, _ := fancontroldef.StateFrom(endpoint.Cluster(fancontroldef.ClusterID))
	number, steps := endpoint.Number(), int(said.SpeedMax)

	fan := &matter.FanControl{
		Mode: matter.FanOn, Speed: 1, Running: said.PercentCurrent > 0, Steps: steps,
		SetMode: c.setFanMode(held, number), SetSpeed: c.setFanSpeed(held, number, steps),
	}

	switch said.FanMode {
	case fancontroldef.FanModeOff:
		fan.Mode = matter.FanOff
	case fancontroldef.FanModeAuto:
		fan.Mode = matter.FanAuto
	case fancontroldef.FanModeLow, fancontroldef.FanModeMedium, fancontroldef.FanModeHigh,
		fancontroldef.FanModeOn, fancontroldef.FanModeSmart:
	}

	switch {
	case steps > 0 && said.SpeedSetting != nil && *said.SpeedSetting > 0:
		fan.Speed = matter.Level(*said.SpeedSetting) / matter.Level(steps)
	case said.PercentSetting != nil && *said.PercentSetting > 0:
		fan.Speed = matter.Level(*said.PercentSetting) / percentFull
	case steps > 0 && said.SpeedCurrent > 0:
		fan.Speed = matter.Level(said.SpeedCurrent) / matter.Level(steps)
	case said.PercentCurrent > 0:
		fan.Speed = matter.Level(said.PercentCurrent) / percentFull
	default:
		if was, known := c.home.Device(device); known && was.FanControl != nil {
			fan.Speed = was.FanControl.Speed
		}
	}

	fan.Speed = min(fan.Speed, 1)

	if said.RockSupport&fancontroldef.RockRockLeftRight != 0 {
		oscillating := said.RockSetting&fancontroldef.RockRockLeftRight != 0
		fan.Oscillating = &oscillating
		fan.SetOscillating = c.setFanFlag(
			held,
			number,
			fancontroldef.AttrRockSetting,
			uint8(fancontroldef.RockRockLeftRight),
		)
	}

	if said.WindSupport&fancontroldef.WindSleepWind != 0 {
		sleep := said.WindSetting&fancontroldef.WindSleepWind != 0
		fan.Sleep = &sleep
		fan.SetSleep = c.setFanFlag(held, number, fancontroldef.AttrWindSetting, uint8(fancontroldef.WindSleepWind))
	}

	return fan
}

func (c *Controller) setFanMode(held *client.Node, endpoint uint16) func(context.Context, matter.FanMode) error {
	return func(ctx context.Context, mode matter.FanMode) error {
		wanted := fancontroldef.FanModeHigh

		switch mode {
		case matter.FanOff:
			wanted = fancontroldef.FanModeOff
		case matter.FanOn:
		case matter.FanAuto:
			wanted = fancontroldef.FanModeAuto
		default:
			return fmt.Errorf("%w: the fan mode %d", matter.ErrInvalid, mode)
		}

		return c.writeFan(ctx, held, endpoint, fancontroldef.AttrFanMode, spec.Uint(wanted))
	}
}

// setFanSpeed writes a speed as one of the fan's steps when it has them,
// which the other side reads back exactly, and as a percent otherwise.
func (c *Controller) setFanSpeed(
	held *client.Node, endpoint uint16, steps int,
) func(context.Context, matter.Level) error {
	return func(ctx context.Context, speed matter.Level) error {
		if !(speed > 0 && speed <= 1) {
			return fmt.Errorf("%w: the fan speed %v", matter.ErrInvalid, speed)
		}

		if steps > 0 {
			return c.writeFan(ctx, held, endpoint, fancontroldef.AttrSpeedSetting,
				spec.Uint(stepOf(speed, steps)))
		}

		return c.writeFan(ctx, held, endpoint, fancontroldef.AttrPercentSetting,
			spec.Uint(percentOf(speed)))
	}
}

// setFanFlag writes one bit of a fan's rocking or of its wind: the one
// this vocabulary has a word for.
func (c *Controller) setFanFlag(
	held *client.Node, endpoint uint16, attribute uint32, bit uint8,
) func(context.Context, bool) error {
	return func(ctx context.Context, on bool) error {
		wanted := uint8(0)
		if on {
			wanted = bit
		}

		return c.writeFan(ctx, held, endpoint, attribute, spec.Uint(wanted))
	}
}

func (c *Controller) setOn(held *client.Node, endpoint uint16) func(context.Context, bool) error {
	return func(ctx context.Context, on bool) error {
		if on {
			return c.invoke(ctx, held, endpoint, onoffdef.ClusterID, onoffdef.CmdOn, onoffdef.OnRequest{})
		}

		return c.invoke(ctx, held, endpoint, onoffdef.ClusterID, onoffdef.CmdOff, onoffdef.OffRequest{})
	}
}

func (c *Controller) setLevel(
	held *client.Node,
	endpoint uint16,
) func(context.Context, matter.Level, time.Duration) error {
	return func(ctx context.Context, level matter.Level, over time.Duration) error {
		if !(level > 0 && level <= 1) || over < 0 || over > maxTransition || over%tenth != 0 {
			return fmt.Errorf("%w: the level %v over %s", matter.ErrInvalid, level, over)
		}

		return c.invoke(ctx, held, endpoint, levelcontroldef.ClusterID, levelcontroldef.CmdMoveToLevelWithOnOff,
			levelcontroldef.MoveToLevelWithOnOffRequest{
				Level:          toMatter(level),
				TransitionTime: spec.ValueOf(uint16(over / tenth)),
			})
	}
}

func (c *Controller) move(held *client.Node, endpoint uint16) func(context.Context, matter.Direction) error {
	return func(ctx context.Context, direction matter.Direction) error {
		request := levelcontroldef.MoveWithOnOffRequest{Rate: spec.NullOf[uint8]()}

		switch direction {
		case matter.Up:
			request.MoveMode = levelcontroldef.MoveModeUp
		case matter.Down:
			request.MoveMode = levelcontroldef.MoveModeDown
		default:
			return fmt.Errorf("%w: the direction %d", matter.ErrInvalid, direction)
		}

		return c.invoke(ctx, held, endpoint, levelcontroldef.ClusterID, levelcontroldef.CmdMoveWithOnOff, request)
	}
}

func (c *Controller) stop(held *client.Node, endpoint uint16) func(context.Context) error {
	return func(ctx context.Context) error {
		return c.invoke(ctx, held, endpoint, levelcontroldef.ClusterID, levelcontroldef.CmdStopWithOnOff,
			levelcontroldef.StopWithOnOffRequest{})
	}
}

// invoke sends a command and gives its answer as the vocabulary's error.
func (c *Controller) invoke(
	ctx context.Context, held *client.Node, endpoint uint16, cluster, command uint32, fields spec.Encodable,
) error {
	_, leave, err := c.enter()
	if err != nil {
		return err
	}

	defer leave()

	_, err = held.Invoke(ctx, client.CommandAt(endpoint, cluster, command), fields, client.InvokeOptions{})

	return answered(err)
}

// writeFan writes one attribute of a fan and gives its answer as the
// vocabulary's error.
func (c *Controller) writeFan(
	ctx context.Context, held *client.Node, endpoint uint16, attribute uint32, value spec.Encodable,
) error {
	_, leave, err := c.enter()
	if err != nil {
		return err
	}

	defer leave()

	statuses, err := held.Write(ctx, []client.WriteRequest{{
		Path: im.ConcreteAttributePath{
			Endpoint: endpoint, Cluster: fancontroldef.ClusterID, Attribute: attribute,
			HasEndpoint: true, HasCluster: true, HasAttribute: true,
		},
		Value: value,
	}}, client.WriteOptions{})
	if err == nil {
		err = statuses.Err()
	}

	return answered(err)
}

// answered is a command's answer as the error that was returned behind
// the bridge: the status it crossed as, read back.
func answered(err error) error {
	var refused *client.StatusError

	switch {
	case err == nil:
		return nil
	case errors.As(err, &refused):
		switch uint8(refused.Status) {
		case statusConstraint:
			return fmt.Errorf("%w: %w", matter.ErrInvalid, err)
		case statusBusy:
			return fmt.Errorf("%w: %w", matter.ErrBusy, err)
		case statusTimeout:
			return fmt.Errorf("%w: %w", matter.ErrUnreachable, err)
		default:
			return fmt.Errorf("%w: %w", matter.ErrFailed, err)
		}
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, exchange.ErrTransient), errors.Is(err, exchange.ErrNetwork),
		errors.Is(err, exchange.ErrSessionClosed), errors.Is(err, client.ErrNoSession):
		return fmt.Errorf("%w: %w", matter.ErrUnreachable, err)
	case errors.Is(err, client.ErrClosed):
		return fmt.Errorf("%w: %w", matter.ErrClosed, err)
	default:
		return fmt.Errorf("%w: %w", matter.ErrFailed, err)
	}
}

// heard is an event of a node: a switch going down or coming up.
func (c *Controller) heard(local string, endpoint uint16, cluster, event uint32) {
	if cluster != switchdef.ClusterID {
		return
	}

	over, leave, err := c.enter()
	if err != nil {
		return
	}

	defer leave()

	// An event of a node being given up names a device the home does not
	// hold, and the home says so to no one.
	held := over.Registry().Get(local)
	if held == nil {
		return
	}

	button := matter.ID(fmt.Sprintf("%s%d", prefixOf(held), endpoint))

	switch event {
	case switchdef.EventInitialPress:
		_ = c.home.Report(button, matter.ButtonPressed{})
	case switchdef.EventShortRelease, switchdef.EventLongRelease:
		_ = c.home.Report(button, matter.ButtonReleased{})
	}
}

// Node is one node a controller holds.
type Node struct {
	of   *Controller
	held *client.Node
	// id is the node's id in the fabric, kept: a node given up has none.
	id uint64
}

// ID is the node's id in the controller's fabric.
func (n *Node) ID() uint64 { return n.id }

// Describe is what the node says of itself.
func (n *Node) Describe() Described { return Describe(n.held) }

// Devices is the node's devices.
func (n *Node) Devices(ctx context.Context) ([]matter.Device, error) {
	if err := n.kept(); err != nil {
		return nil, err
	}

	return n.of.Devices(ctx, n.held)
}

// Watch is a Source's Watch over the node.
func (n *Node) Watch(ctx context.Context, handle func(matter.Event)) error {
	if err := n.kept(); err != nil {
		return err
	}

	return n.of.Watch(ctx, n.id, handle)
}

// Invite opens the node to another administrator.
func (n *Node) Invite(ctx context.Context, within time.Duration) (manual, qr string, err error) {
	if err = n.kept(); err != nil {
		return "", "", err
	}

	return n.of.Invite(ctx, n.held, within)
}

// Unpair gives the node up. That it is given up once is the
// controller's to see to: two that ask at once both get here.
func (n *Node) Unpair(ctx context.Context) error {
	return n.of.Unpair(ctx, n.held, n.id)
}

// kept says whether the node is still held: matter.ErrClosed once it was
// given up.
func (n *Node) kept() error {
	n.of.mu.Lock()
	defer n.of.mu.Unlock()

	if n.of.unpaired[n.id] {
		return matter.ErrClosed
	}

	return nil
}
