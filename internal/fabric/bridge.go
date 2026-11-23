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
	"syscall"
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
	// A port of the system's choosing, on every address.
	anyPort = ":0"
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
	// announcer is what the node announces itself through when the
	// bridge was lent a network, and nil when it has its own.
	announcer *announcer

	mu     sync.Mutex
	closed bool
}

// OpenBridge makes the node a bridge is, in dir, starts it and serves
// the source through it. The secret the bridge is commissioned with is
// drawn the first time and kept in dir.
//
// With lent nil the node speaks DNS-SD by itself, on the interfaces
// named. With one, it announces and finds through it and opens no socket
// for DNS-SD: which interfaces is then the lender's to say.
func OpenBridge(
	ctx context.Context, identity Identity, dir string, interfaces []string, source matter.Source, lent Lent,
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

	// On Matter's own port, or, when another bridge of this machine has
	// it, on one the system picks: a node says in its announcement which
	// port it answers on, so a controller finds it either way.
	var through *announcer
	if lent != nil {
		through = newAnnouncer(lent)
	}

	configured := func(listen string) node.Config {
		cfg := NodeConfig(identity, secret, filepath.Join(dir, stateFile), interfaces, listen)
		if through != nil {
			cfg.Advertiser = through
		}

		return cfg
	}

	made, err := started(ctx, configured(""), lent)
	if taken(err) {
		made, err = started(ctx, configured(anyPort), lent)
	}

	if err != nil {
		return nil, err
	}

	bridge := NewBridge(made, source) //nolint:contextcheck // serving outlives the opening: Close ends it
	bridge.owned, bridge.announcer = true, through

	return bridge, nil
}

// taken says whether an error is that of an address another has: by its
// name on Unix, and by Windows' own number, which Go does not read as
// that name.
func taken(err error) bool {
	const windowsInUse = syscall.Errno(10048)

	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, windowsInUse)
}

// started is a node made and started, or neither.
func started(ctx context.Context, cfg node.Config, lent Lent) (*node.Node, error) {
	made, err := node.New(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("making the bridge's node: %w", err)
	}

	// A node made is given a resolver of its own, which opens sockets for
	// DNS-SD when it is asked. It is asked at Start, for the controllers
	// that were subscribed: what was lent has to be in its place by then.
	if lent != nil {
		made.Bridge().AttachOperationalResolver(resolver{lent})
	}

	if err = made.Start(ctx); err != nil {
		_ = made.Stop(context.WithoutCancel(ctx))

		return nil, fmt.Errorf("starting the bridge's node: %w", err)
	}

	return made, nil
}

// NewBridge serves the source through a node that is started and stays
// the caller's. The node is one that opens no window by itself, as
// NodeConfig makes it: a bridge lets an administrator in when it is
// asked to, and is found as open to one only then.
func NewBridge(started *node.Node, source matter.Source) *Bridge {
	return &Bridge{node: started, serving: Serve(started, source)}
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
	b.serving.Close()

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
		if err = b.node.OpenOwnWindow(ctx, within); err != nil {
			return "", "", fmt.Errorf("opening the bridge: %w", err)
		}

		if err = b.announced(ctx); err != nil {
			return "", "", err
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

	if err = b.node.OpenBasicWindow(ctx, fresh); err != nil {
		return "", "", fmt.Errorf("opening the bridge: %w", err)
	}

	if err = b.announced(ctx); err != nil {
		return "", "", err
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
// running ends too: held by no one, the bridge is open to no one until
// it is invited.
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

// Invited says whether the bridge is open to an administrator now: an
// invitation stands, or an administrator that holds the bridge opened it
// to another.
func (b *Bridge) Invited() bool {
	return b.node.Window().Open
}

// announced says whether the window just opened is announced on the
// network the bridge was lent. A node opens its window whatever becomes
// of the announcement, and a bridge no controller can find is not
// invited: the window is closed again, and why is the error.
func (b *Bridge) announced(ctx context.Context) error {
	if b.announcer == nil {
		return nil
	}

	refused := b.announcer.failed(commissionable)
	if refused == nil {
		return nil
	}

	if err := b.node.CloseWindow(ctx); err != nil {
		slog.WarnContext(ctx, "the bridge could not close a window it could not announce", "error", err)
	}

	return fmt.Errorf("opening the bridge: %w", refused)
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
