// Package controller commissions Matter nodes and drives them. A node it
// holds is a matter.Source, and so is the controller, for the devices of
// all its nodes: a caller reads, commands and watches them without
// knowing they are Matter's.
//
// What it has to say that is no one's answer goes to log/slog's default
// logger, with the context it was said in.
package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/duncanite/go-matter"
	over "github.com/duncanite/go-matter/internal/fabric"
)

// Config is what a controller needs to be one.
type Config struct {
	// Name is what the controller calls itself to the nodes it holds: its
	// fabric's label.
	Name string
	// Dir is a directory the controller keeps as its own: its authority's
	// keys and the nodes it holds. Whoever reads it controls those nodes.
	Dir string
	// Trust is whose devices may be commissioned.
	Trust Trust
	// Interface is the network interface to use; empty is all of them.
	// The first implementation does not honour it: it uses them all.
	Interface string
}

// Trust is which makers' attestations a controller accepts when it
// commissions.
type Trust int

// The trusts. The zero value is the strictest.
const (
	// Certified accepts devices whose attestation chains to a root in
	// Matter's ledger of certified makers.
	Certified Trust = iota
	// Development also accepts Matter's test roots: what a home-made
	// bridge, bridge.Config's zero Product, presents.
	Development
	// Anything accepts every device and checks nothing.
	Anything
)

// The errors of commissioning, to test with errors.Is.
var (
	// ErrNotFound: nothing on the network answered to the setup code.
	ErrNotFound = over.ErrNotFound
	// ErrSetupCode: the code is not a setup code, or the device refused
	// it.
	ErrSetupCode = over.ErrSetupCode
	// ErrUntrusted: the device's attestation is not one Config.Trust
	// accepts.
	ErrUntrusted = over.ErrUntrusted
)

// Controller is a Matter controller and the fabric it administers, on
// the network.
type Controller struct{ held *over.Controller }

// Open makes the controller and puts it on the network: when it returns,
// every other method works. ctx bounds the opening alone. It reads and,
// the first time, writes cfg.Dir, which it makes its owner's alone.
//
// On Windows the first implementation cannot find devices, and Open is
// errors.ErrUnsupported.
//
// While it is open the controller keeps a watch on every node it holds
// and looks again for a node that went away. A node that does not answer
// is its devices unreachable, never the controller's end.
func Open(ctx context.Context, cfg Config) (*Controller, error) {
	if cfg.Name == "" || cfg.Dir == "" {
		return nil, fmt.Errorf("%w: a controller needs a name and a directory", matter.ErrInvalid)
	}

	trust := over.TrustCertified

	switch cfg.Trust {
	case Certified:
	case Development:
		trust = over.TrustDevelopment
	case Anything:
		trust = over.TrustAnything
	default:
		return nil, fmt.Errorf("%w: the trust %d is not one", matter.ErrInvalid, cfg.Trust)
	}

	held, err := over.OpenController(ctx, cfg.Name, cfg.Dir, trust)
	if err != nil {
		return nil, err
	}

	return &Controller{held: held}, nil
}

// Close takes the controller off the network and returns when everything
// it started has ended. The nodes it holds it still holds, at the next
// Open of the same Dir. After Close every call that returns an error
// returns matter.ErrClosed, a Watch in flight too, and the others answer
// that there is nothing. Close waits for what is under way: a host that
// wants it prompt bounds its own calls, an Unpair of a node that does
// not answer among them.
func (c *Controller) Close() error {
	return c.held.Close()
}

// Pair commissions the node that answers to code, a manual code or a QR
// code's text, and holds it from then on. It waits for the node to say
// what devices it has and to answer; a node that takes more than half a
// minute over it is held all the same, and its devices come when it
// says them.
func (c *Controller) Pair(ctx context.Context, code string) (*Node, error) {
	paired, err := c.held.PairNode(ctx, code)
	if err != nil {
		return nil, err
	}

	return &Node{held: paired}, nil
}

// Nodes is the nodes the controller holds.
func (c *Controller) Nodes() []*Node {
	held := c.held.HeldNodes()
	nodes := make([]*Node, 0, len(held))

	for _, one := range held {
		nodes = append(nodes, &Node{held: one})
	}

	return nodes
}

// Node is one of them, and whether the controller holds it.
func (c *Controller) Node(id NodeID) (*Node, bool) {
	for _, one := range c.held.HeldNodes() {
		if NodeID(one.ID()) == id {
			return &Node{held: one}, true
		}
	}

	return nil, false
}

// Devices is matter.Source's, over every node the controller holds. A
// device has the ID it has through its Node, which names the node as
// well, so no two nodes' devices share one.
func (c *Controller) Devices(ctx context.Context) ([]matter.Device, error) {
	return c.held.Devices(ctx, nil)
}

// Watch is matter.Source's, over every node. A node paired or unpaired
// while it runs arrives as its devices added or removed.
func (c *Controller) Watch(ctx context.Context, handle func(matter.Event)) error {
	return c.held.Watch(ctx, 0, handle)
}

// NodeID names a node in the controller's fabric.
type NodeID uint64

// NodeInfo is what a node says of itself.
type NodeInfo struct {
	Name     string
	Vendor   string
	Product  string
	Serial   string
	Firmware string
}

// Node is a Matter node the controller holds: a single device, or a
// bridge with many behind it.
type Node struct{ held *over.Node }

// ID is the node's id.
func (n *Node) ID() NodeID {
	return NodeID(n.held.ID())
}

// Info is what the node said of itself when last heard.
func (n *Node) Info() NodeInfo {
	return NodeInfo(n.held.Describe())
}

// Devices is matter.Source's, over this node. A device's ID holds from
// one run to the next and across the node's restarts. A device of a kind
// this module has no word for is not listed. A switch is a Remote of one
// button, named as the switch is.
func (n *Node) Devices(ctx context.Context) ([]matter.Device, error) {
	return n.held.Devices(ctx)
}

// Watch is matter.Source's, over this node. A node that stops answering
// is every one of its devices unreachable, not an error; Watch goes on,
// and says they are back when the node is found again. A node unpaired ends it, with
// matter.ErrClosed.
func (n *Node) Watch(ctx context.Context, handle func(matter.Event)) error {
	return n.held.Watch(ctx, handle)
}

// Invite lets another administrator, a phone's home app for one,
// commission the node within the duration given, with the code it
// returns. A duration that is not above zero is matter.ErrInvalid.
func (n *Node) Invite(ctx context.Context, within time.Duration) (matter.SetupCode, error) {
	manual, qr, err := n.held.Invite(ctx, within)
	if err != nil {
		return matter.SetupCode{}, err
	}

	return matter.SetupCode{Manual: manual, QR: qr}, nil
}

// Unpair gives the node up: it is told to forget this controller, and is
// forgotten here whether or not it could be told. From then on the
// node's calls that return an error return matter.ErrClosed, and its
// devices are gone: their commands answer matter.ErrNoDevice.
func (n *Node) Unpair(ctx context.Context) error {
	return n.held.Unpair(ctx)
}

// Both are sources.
var (
	_ matter.Source = (*Controller)(nil)
	_ matter.Source = (*Node)(nil)
)
