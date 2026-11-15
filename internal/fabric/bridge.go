package fabric

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/SukramJ/go-fabric/node"
	"github.com/SukramJ/go-fabric/store"
	"github.com/forkcloser/xos"
	"github.com/mycophonic/primordium/filesystem"

	"github.com/duncanite/go-matter"
)

const (
	// The files a bridge keeps in its directory.
	secretFile = "secret.json"
	stateFile  = "bridge.db"
	// The directory and the secret are their owner's alone: the secret
	// commissions the bridge.
	dirPermissions    = 0o700
	secretPermissions = 0o600
	// How long a node is given to stop.
	stopping = 10 * time.Second
)

// ErrNoFabric is a fabric the bridge is not held by.
var ErrNoFabric = errors.New("no such fabric")

// Held is one fabric's hold on a bridge.
type Held struct {
	Index    int
	Label    string
	VendorID uint16
}

// Bridge is a node serving a source, and what is asked of it as a
// bridge.
type Bridge struct {
	node    *node.Node
	serving *Serving
	owned   bool

	// unobserve ends the watch on the node's commissioning window.
	unobserve func()

	// closing is the closings of windows no one asked for.
	closing sync.WaitGroup

	mu       sync.Mutex
	closed   bool
	inviting bool
}

// OpenBridge makes the node a bridge is, in dir, starts it and serves
// the source through it. The secret the bridge is commissioned with is
// drawn the first time and kept in dir.
func OpenBridge(
	ctx context.Context, identity Identity, dir string, interfaces []string, source matter.Source,
) (*Bridge, error) {
	if err := os.MkdirAll(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("making %s: %w", dir, err)
	}

	// One that was there already is made its owner's alone too.
	if err := os.Chmod(dir, dirPermissions); err != nil {
		return nil, fmt.Errorf("making %s its owner's alone: %w", dir, err)
	}

	secret, err := secretIn(dir)
	if err != nil {
		return nil, err
	}

	made, err := node.New(ctx, NodeConfig(identity, secret, filepath.Join(dir, stateFile), interfaces))
	if err != nil {
		return nil, fmt.Errorf("making the bridge's node: %w", err)
	}

	if err = made.Start(ctx); err != nil {
		_ = made.Stop(context.WithoutCancel(ctx))

		return nil, fmt.Errorf("starting the bridge's node: %w", err)
	}

	bridge, err := NewBridge(ctx, made, source)
	if err != nil {
		_ = made.Stop(context.WithoutCancel(ctx))

		return nil, err
	}

	bridge.owned = true

	return bridge, nil
}

// NewBridge serves the source through a node that is started and stays
// the caller's. The window a node opens by itself while no fabric holds
// it is closed: a bridge lets an administrator in when it is asked to.
func NewBridge(ctx context.Context, started *node.Node, source matter.Source) (*Bridge, error) {
	bridge := &Bridge{node: started}

	// A node opens its own window whenever no fabric holds it: at its
	// start, and again when its last fabric is removed. One that opens
	// while Invite is not opening it is closed. The hook runs inline, in
	// the call that moved the window, so that an Invite's own opening is
	// known for what it is.
	bridge.unobserve = started.Observe(node.Observers{OnWindowTransition: bridge.windowMoved})

	if err := started.CloseWindow(ctx); err != nil {
		bridge.unobserve()

		return nil, fmt.Errorf("closing the node's own window: %w", err)
	}

	bridge.serving = Serve(started, source) //nolint:contextcheck // serving outlives the opening: Close ends it

	return bridge, nil
}

// Close stops serving, and the node when OpenBridge made it. It returns
// when both have ended.
func (b *Bridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return matter.ErrClosed
	}

	b.closed = true
	b.unobserve()
	b.serving.Close()
	b.mu.Unlock()
	b.closing.Wait()
	b.mu.Lock()

	if !b.owned {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), stopping)
	defer cancel()

	if err := b.node.Stop(ctx); err != nil {
		return fmt.Errorf("stopping the bridge's node: %w", err)
	}

	return nil
}

// Invite opens the bridge to one administrator for the time given and
// returns the manual code and the QR code's text to commission it with:
// its own while no fabric holds it, fresh ones beside others.
func (b *Bridge) Invite(ctx context.Context, within time.Duration) (manual, qr string, err error) {
	if err = b.open(); err != nil {
		return "", "", err
	}

	if within <= 0 {
		return "", "", fmt.Errorf("%w: an invitation of %s", matter.ErrInvalid, within)
	}

	held, err := b.node.FabricInfos(ctx)
	if err != nil {
		return "", "", fmt.Errorf("reading who holds the bridge: %w", err)
	}

	if len(held) == 0 {
		if err = b.asInvitation(func() error { return b.node.OpenOwnWindow(ctx, within) }); err != nil {
			return "", "", fmt.Errorf("opening the bridge: %w", err)
		}

		payload, read := b.node.SetupPayload()
		if read != nil {
			return "", "", fmt.Errorf("reading the bridge's setup code: %w", read)
		}

		return payload.Manual, payload.QR, nil
	}

	fresh, err := node.NewWindowCredentials(within)
	if err != nil {
		return "", "", fmt.Errorf("drawing a setup code: %w", err)
	}

	if err = b.asInvitation(func() error { return b.node.OpenBasicWindow(ctx, fresh) }); err != nil {
		return "", "", fmt.Errorf("opening the bridge: %w", err)
	}

	if qr, manual, err = b.node.PairingCodesFor(fresh); err != nil {
		return "", "", fmt.Errorf("writing the setup code: %w", err)
	}

	return manual, qr, nil
}

// Fabrics is who holds the bridge.
func (b *Bridge) Fabrics(ctx context.Context) ([]Held, error) {
	if err := b.open(); err != nil {
		return nil, err
	}

	infos, err := b.node.FabricInfos(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading who holds the bridge: %w", err)
	}

	held := make([]Held, 0, len(infos))
	for _, info := range infos {
		held = append(held, Held{Index: int(info.FabricIndex), Label: info.Label, VendorID: info.VendorID})
	}

	return held, nil
}

// Remove ends a fabric's hold. With the last, an invitation still
// running ends too; and a node with no fabric left opens its own window
// again by itself, which windowMoved closes.
func (b *Bridge) Remove(ctx context.Context, fabric int) error {
	if err := b.open(); err != nil {
		return err
	}

	if fabric < 1 || fabric > 254 {
		return fmt.Errorf("%w: %d", ErrNoFabric, fabric)
	}

	err := b.node.RemoveFabric(ctx, uint8(fabric))

	switch {
	case errors.Is(err, store.ErrFabricNotFound):
		return fmt.Errorf("%w: %d", ErrNoFabric, fabric)
	case err != nil:
		return fmt.Errorf("removing fabric %d: %w", fabric, err)
	}

	held, err := b.node.FabricInfos(ctx)
	if err != nil {
		return fmt.Errorf("reading who holds the bridge: %w", err)
	}

	// Held by no one, the bridge is as it was before anyone: an
	// invitation still running ends with the last fabric.
	if len(held) == 0 {
		if err = b.node.CloseWindow(ctx); err != nil {
			return fmt.Errorf("closing the bridge: %w", err)
		}
	}

	return nil
}

// open says whether the bridge can still be asked.
func (b *Bridge) open() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return matter.ErrClosed
	}

	return nil
}

// windowMoved closes a window that opened without an invitation. It is
// called inside the node, so the closing is done after it returns.
func (b *Bridge) windowMoved(window node.WindowSnapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Only the node's own window, which it opens by itself: one an
	// administrator opened, or Invite beside one, was asked for.
	if !window.Open || window.Kind != node.WindowOwn || b.inviting || b.closed {
		return
	}

	b.closing.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), stopping)
		defer cancel()

		if err := b.node.CloseWindow(ctx); err != nil {
			slog.WarnContext(ctx, "the bridge could not close a window no one asked for", "error", err)
		}
	})
}

// asInvitation runs an opening of the window as an invitation's: what it
// opens is left open.
func (b *Bridge) asInvitation(opening func() error) error {
	b.mu.Lock()
	b.inviting = true
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		b.inviting = false
		b.mu.Unlock()
	}()

	return opening()
}

// kept is the secret as it is kept in the directory.
type kept struct {
	Passcode      uint32 `json:"passcode"`
	Discriminator uint16 `json:"discriminator"`
	Iterations    uint32 `json:"iterations"`
	Salt          []byte `json:"salt"`
}

// secretIn is the secret a bridge in dir is commissioned with: read, or
// the first time drawn and written, its owner's alone.
func secretIn(dir string) (node.Commissioning, error) {
	path := filepath.Join(dir, secretFile)

	data, err := xos.ReadFile(path)

	switch {
	case err == nil:
		var read kept
		if err = json.Unmarshal(data, &read); err != nil {
			return node.Commissioning{}, fmt.Errorf("reading %s: %w", path, err)
		}

		return node.Commissioning{
			Passcode: read.Passcode, Discriminator: read.Discriminator,
			PBKDFIterations: read.Iterations, Salt: read.Salt,
		}, nil
	case !errors.Is(err, os.ErrNotExist):
		return node.Commissioning{}, fmt.Errorf("reading %s: %w", path, err)
	}

	drawn, err := node.NewCommissioningCredentials()
	if err != nil {
		return node.Commissioning{}, fmt.Errorf("drawing the bridge's secret: %w", err)
	}

	data, err = json.Marshal(kept{
		Passcode: drawn.Passcode, Discriminator: drawn.Discriminator,
		Iterations: drawn.PBKDFIterations, Salt: drawn.Salt,
	})
	if err != nil {
		return node.Commissioning{}, fmt.Errorf("writing %s: %w", path, err)
	}

	if err = filesystem.WriteFile(path, data, secretPermissions); err != nil {
		return node.Commissioning{}, fmt.Errorf("writing %s: %w", path, err)
	}

	return drawn, nil
}
