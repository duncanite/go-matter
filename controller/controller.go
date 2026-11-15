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
	"errors"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/todo"
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
	ErrNotFound = errors.New("no device answered to that setup code")
	// ErrSetupCode: the code is not a setup code, or the device refused
	// it.
	ErrSetupCode = errors.New("the setup code is not valid")
	// ErrUntrusted: the device's attestation is not one Config.Trust
	// accepts.
	ErrUntrusted = errors.New("the device's attestation is not trusted")
)

// Controller is a Matter controller and the fabric it administers, on
// the network.
type Controller struct{}

// Open makes the controller and puts it on the network: when it returns,
// every other method works. ctx bounds the opening alone. It reads and,
// the first time, writes cfg.Dir.
//
// While it is open the controller keeps a watch on every node it holds
// and finds again a node that went away. A node that does not answer is
// its devices unreachable, never the controller's end.
func Open(ctx context.Context, cfg Config) (*Controller, error) {
	return nil, todo.ImplementMe(ctx, cfg)
}

// Close takes the controller off the network and returns when everything
// it started has ended. The nodes it holds it still holds, at the next
// Open of the same Dir. After Close every call that can fail fails with
// matter.ErrClosed, a Watch in flight returns it, and the others answer
// as they last did.
func (c *Controller) Close() error {
	return todo.ImplementMe(c)
}

// Pair commissions the node that answers to code, a manual code or a QR
// code's text, and holds it from then on.
func (c *Controller) Pair(ctx context.Context, code string) (*Node, error) {
	return nil, todo.ImplementMe(c, ctx, code)
}

// Nodes is the nodes the controller holds.
func (c *Controller) Nodes() []*Node {
	_ = todo.ImplementMe(c)

	return nil
}

// Node is one of them, and whether the controller holds it.
func (c *Controller) Node(id NodeID) (*Node, bool) {
	_ = todo.ImplementMe(c, id)

	return nil, false
}

// Devices is matter.Source's, over every node the controller holds. A
// device has the ID it has through its Node, which names the node as
// well, so no two nodes' devices share one.
func (c *Controller) Devices(ctx context.Context) ([]matter.Device, error) {
	return nil, todo.ImplementMe(c, ctx)
}

// Watch is matter.Source's, over every node. A node paired or unpaired
// while it runs arrives as its devices added or removed.
func (c *Controller) Watch(ctx context.Context, handle func(matter.Event)) error {
	return todo.ImplementMe(c, ctx, handle)
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
type Node struct{}

// ID is the node's id.
func (n *Node) ID() NodeID {
	_ = todo.ImplementMe(n)

	return 0
}

// Info is what the node said of itself when last heard.
func (n *Node) Info() NodeInfo {
	_ = todo.ImplementMe(n)

	return NodeInfo{}
}

// Devices is matter.Source's, over this node. A device's ID holds from
// one run to the next and across the node's restarts. A device of a kind
// this module has no word for is not listed.
func (n *Node) Devices(ctx context.Context) ([]matter.Device, error) {
	return nil, todo.ImplementMe(n, ctx)
}

// Watch is matter.Source's, over this node. A node that stops answering
// is every one of its devices unreachable, not an error; Watch goes on
// and says when they are back. A node unpaired ends it, with
// matter.ErrClosed.
func (n *Node) Watch(ctx context.Context, handle func(matter.Event)) error {
	return todo.ImplementMe(n, ctx, handle)
}

// Invite lets another administrator, a phone's home app for one,
// commission the node within the duration given, with the code it
// returns.
func (n *Node) Invite(ctx context.Context, within time.Duration) (matter.SetupCode, error) {
	return matter.SetupCode{}, todo.ImplementMe(n, ctx, within)
}

// Unpair gives the node up: it is told to forget this controller, and is
// forgotten here whether or not it could be told. From then on the
// node's calls that can fail, and its devices' commands, fail with
// matter.ErrClosed.
func (n *Node) Unpair(ctx context.Context) error {
	return todo.ImplementMe(n, ctx)
}

// Both are sources.
var (
	_ matter.Source = (*Controller)(nil)
	_ matter.Source = (*Node)(nil)
)
