// Package bridge serves a matter.Source as one Matter node: a bridge,
// with each device of the source behind it as a Matter device. A
// controller's commands become calls of the device's commands, and their
// errors become the controller's; the source's events become reports to
// every controller that subscribed.
//
// What it has to say that is no one's answer goes to log/slog's default
// logger, with the context it was said in.
package bridge

import (
	"context"
	"fmt"
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

	var interfaces []string
	if cfg.Interface != "" {
		interfaces = []string{cfg.Interface}
	}

	serving, err := over.OpenBridge(ctx, over.Identity{
		Name:      cfg.Name,
		VendorID:  cfg.Product.VendorID,
		ProductID: cfg.Product.ProductID,
		Vendor:    cfg.Product.Vendor,
		Product:   cfg.Product.Name,
		Serial:    cfg.Product.Serial,
	}, cfg.Dir, interfaces, source)
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
