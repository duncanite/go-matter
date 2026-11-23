// Package bridge serves a matter.Source as one Matter node: a bridge,
// with each device of the source behind it as a Matter device. A
// controller's commands become calls of the device's commands, and their
// errors become the controller's; the source's events become reports to
// every controller that subscribed.
//
// A bridge is found over DNS-SD. It has a part of its own in it, or is
// lent its host's: Config.Network, for a program that also looks for
// devices, since a host has one such part.
//
// What it has to say that is no one's answer goes to log/slog's default
// logger, with the context it was said in.
package bridge

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/duncanite/go-matter"
	over "github.com/duncanite/go-matter/internal/fabric"
)

// Config is what a bridge needs to be one.
type Config struct {
	// Name is what a controller shows for the bridge itself.
	Name string
	// Dir is a directory the bridge keeps as its own, and makes its
	// owner's alone: who holds it, the secret it is commissioned with,
	// which device has which place. Two bridges never share one. The
	// same directory is the same bridge after a restart.
	Dir string
	// Product is who the bridge says made it. The zero value is the
	// identity Matter reserves for development. A controller takes it
	// only if it is set to: package controller's does under Development
	// and refuses it under Certified, and a phone's home app warns. No
	// shipped product may use it.
	Product Product
	// Interface is the network interface the bridge is found on: where
	// it announces itself. Empty is all of them. It answers on every
	// interface whichever is named, on Matter's own port, or on another
	// when a bridge of the same machine already has that one.
	Interface string
	// Network is the host's part in DNS-SD, lent to the bridge: with it
	// the bridge announces itself and finds its controllers through it,
	// and opens no socket for DNS-SD. A host has one such part, and a
	// program that looks for devices and is found as a bridge shares it.
	// Nil, the bridge has its own. With one, Interface is the lender's
	// to say and is not to be set.
	Network Network
}

// Network is the host's part in DNS-SD (RFC 6763, over Multicast DNS,
// RFC 6762) as a bridge asks for it. Whatever has that part satisfies it
// with a few lines. Its methods are called from several goroutines. The
// bridge never closes it: it is its lender's, for as long as the bridge
// is open and after.
type Network interface {
	// Announce announces a service, in place of the one of the same Name
	// and Type when there is one. It returns when the service is
	// announced.
	//
	// The instance is announced under the Name given, and never under
	// another: a controller finds a node by it, and one that is taken is
	// an error. Every record lives 120 seconds.
	//
	// The service's host is the lender's own, under the one name it has,
	// which has to be one HostName gives: whoever has the host's part in
	// DNS-SD says what the host is called, and Matter says of what form.
	// Its addresses are those this host has on the interfaces the
	// service is announced on, and only those a controller there can
	// reach: its routable IPv4 and IPv6 addresses, and an interface's
	// link-local IPv6 address only when the interface has no routable
	// IPv6 one; never a loopback address, nor one of a point-to-point
	// interface or of a bridge a container runtime made. A phone's
	// controller was seen to go through the addresses it is given in
	// their order and to give up at one it cannot reach.
	//
	// Announcing again a service that is as it was says nothing: the
	// bridge does not, and a lender need not check.
	Announce(ctx context.Context, service Service) error
	// Withdraw says the service of that name and type is gone, with a
	// goodbye. The bridge withdraws only what it announced. Once it has
	// asked, it has no more to do: a service that could not be withdrawn
	// stands announced until its lender clears it, and a bridge that
	// closes says why in the log and closes all the same.
	Withdraw(ctx context.Context, name, kind string) error
	// Resolve is where the instance of that name and type is now: every
	// address it is found at, with its port, an IPv6 link-local address
	// with its zone. An instance that is not found is no error, and no
	// address.
	Resolve(ctx context.Context, name, kind string) ([]netip.AddrPort, error)
}

// Service is one service a bridge announces: itself, open to be
// commissioned, or as a node of a fabric that holds it.
type Service struct {
	// Name is the instance's name, its first label.
	Name string
	// Type is the service's type: "_matterc._udp" or "_matter._tcp".
	Type string
	// Subtypes are the subtypes the instance is also found under, each
	// its own label: "_L3840", "_CM".
	Subtypes []string
	// Port is the port the bridge answers on.
	Port uint16
	// Text is the service's text, in the order it is to be written.
	Text []Entry
}

// Entry is one key of a service's text, and its value.
type Entry struct {
	Key   string
	Value string
}

// HostName is the name Matter has a node's host take: its hardware
// address, the six bytes of a MAC address or the eight of an extended
// one, as twelve or sixteen hexadecimal digits in capitals. It is the
// first label of the name, with no domain. An address of another length,
// or all zeros, is matter.ErrInvalid: it names no host.
//
// It is for whoever lends a Network, which announces a bridge under its
// own host name and has to have one of this form before any bridge is
// opened. A host with no hardware address has no name of this form, and
// none is made up for it here: what such a host is called is its own to
// decide.
func HostName(hardware net.HardwareAddr) (string, error) {
	const mac, extended = 6, 8

	if len(hardware) != mac && len(hardware) != extended {
		return "", fmt.Errorf(
			"%w: a hardware address of %d bytes, where a host name is made of %d or %d",
			matter.ErrInvalid, len(hardware), mac, extended,
		)
	}

	if !slices.ContainsFunc(hardware, func(octet byte) bool { return octet != 0 }) {
		return "", fmt.Errorf("%w: a hardware address that is all zeros", matter.ErrInvalid)
	}

	return strings.ToUpper(hex.EncodeToString(hardware)), nil
}

// lent is a Network as what serves the bridge asks for one.
type lent struct{ network Network }

func (l lent) Announce(ctx context.Context, service over.Announced) error {
	text := make([]Entry, 0, len(service.Text))
	for _, entry := range service.Text {
		text = append(text, Entry{Key: entry[0], Value: entry[1]})
	}

	//nolint:wrapcheck // the lender's own word, handed on as it is
	return l.network.Announce(ctx, Service{
		Name: service.Name, Type: service.Type, Subtypes: service.Subtypes, Port: service.Port, Text: text,
	})
}

func (l lent) Withdraw(ctx context.Context, name, kind string) error {
	//nolint:wrapcheck // the lender's own word, handed on as it is
	return l.network.Withdraw(ctx, name, kind)
}

func (l lent) Resolve(ctx context.Context, name, kind string) ([]netip.AddrPort, error) {
	//nolint:wrapcheck // the lender's own word, handed on as it is
	return l.network.Resolve(ctx, name, kind)
}

// Product is a certified product's identity.
type Product struct {
	VendorID  uint16
	ProductID uint16
	Vendor    string
	Name      string
	Serial    string
}

// Fabric is one administrator's hold on the bridge: a controller, or a
// whole ecosystem of them, that commissioned it. Several can hold it at
// once.
type Fabric struct {
	// Index names the fabric on this bridge.
	Index int
	// Label is what the administrator called itself; often empty.
	Label string
	// VendorID is who made the administrator, as Matter numbers vendors.
	VendorID uint16
}

// ErrNoFabric is Remove's: no fabric by that index holds the bridge.
var ErrNoFabric = over.ErrNoFabric

// Bridge is a Matter bridge over one source, serving.
type Bridge struct{ serving *over.Bridge }

// Open makes the bridge and starts it serving: when it returns, the
// bridge is on the network, and every other method works. ctx bounds the
// opening alone. It reads and, the first time, writes cfg.Dir.
//
// The bridge watches the source for as long as it is open. A device the
// source adds or removes is added or removed for every controller. A
// source whose Watch fails is not the bridge's end: its devices are
// shown unreachable, and it is watched again. One whose Watch returns
// matter.ErrClosed is over: its devices are removed, and the bridge
// serves on with none.
//
// A bridge that is opened is held by whoever held it, and does not open
// to anyone else by itself: Invite opens it, and so can an administrator
// that holds it, to let another in, as Matter gives every administrator
// to do.
func Open(ctx context.Context, cfg Config, source matter.Source) (*Bridge, error) {
	if cfg.Name == "" || cfg.Dir == "" || source == nil {
		return nil, fmt.Errorf("%w: a bridge needs a name, a directory and a source", matter.ErrInvalid)
	}

	if cfg.Interface != "" && cfg.Network != nil {
		return nil, fmt.Errorf(
			"%w: a bridge that is lent a network is found on the interfaces its lender chooses, and names none",
			matter.ErrInvalid,
		)
	}

	var interfaces []string
	if cfg.Interface != "" {
		interfaces = []string{cfg.Interface}
	}

	// A nil Network in an interface would not be nil.
	var lender over.Lent
	if cfg.Network != nil {
		lender = lent{cfg.Network}
	}

	serving, err := over.OpenBridge(ctx, over.Identity{
		Name:      cfg.Name,
		VendorID:  cfg.Product.VendorID,
		ProductID: cfg.Product.ProductID,
		Vendor:    cfg.Product.Vendor,
		Product:   cfg.Product.Name,
		Serial:    cfg.Product.Serial,
	}, cfg.Dir, interfaces, source, lender)
	if err != nil {
		return nil, err
	}

	return &Bridge{serving: serving}, nil
}

// Close takes the bridge off the network and returns when everything it
// started has ended. Whoever holds it still does, and finds it again at
// the next Open of the same Dir. After Close every call that can fail
// fails with matter.ErrClosed.
func (b *Bridge) Close() error {
	return b.serving.Close()
}

// Invite lets one administrator commission the bridge within the
// duration given, with the code it returns: the first to hold it, or one
// more beside those that do. While no one holds the bridge the code is
// its own, the same at every Invite, and can be printed once and kept;
// beside others it is made for the occasion, and takes the place of the
// one before it, whose code no longer opens anything. A duration that is not
// above zero is matter.ErrInvalid, and one the bridge cannot keep is
// refused with why.
func (b *Bridge) Invite(ctx context.Context, within time.Duration) (matter.SetupCode, error) {
	manual, qr, err := b.serving.Invite(ctx, within)
	if err != nil {
		return matter.SetupCode{}, err
	}

	return matter.SetupCode{Manual: manual, QR: qr}, nil
}

// Fabrics is who holds the bridge.
func (b *Bridge) Fabrics(ctx context.Context) ([]Fabric, error) {
	held, err := b.serving.Fabrics(ctx)
	if err != nil {
		return nil, err
	}

	fabrics := make([]Fabric, 0, len(held))
	for _, one := range held {
		fabrics = append(fabrics, Fabric(one))
	}

	return fabrics, nil
}

// Remove ends a fabric's hold, by its Index; ErrNoFabric when none has
// it. Removing the last leaves the bridge as it was before any.
func (b *Bridge) Remove(ctx context.Context, fabric int) error {
	return b.serving.Remove(ctx, fabric)
}
