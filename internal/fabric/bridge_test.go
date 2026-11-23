package fabric_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/clock"
	"github.com/SukramJ/go-fabric/controller"
	"github.com/SukramJ/go-fabric/controller/attestation"
	"github.com/SukramJ/go-fabric/controller/client"
	"github.com/SukramJ/go-fabric/fabrictest"
	"github.com/SukramJ/go-fabric/node"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/fabric"
	"github.com/duncanite/go-matter/mattertest"
)

var errHub = errors.New("the hub went away")

// fickle is a source over a home whose Watch can be made to fail, or to
// say that the source is over.
type fickle struct {
	*matter.Home

	mu      sync.Mutex
	ending  error
	end     context.CancelFunc
	watches int
	// down, while it is open, is the source not answering at all.
	down chan struct{}
}

// Devices fails while the source is down.
func (f *fickle) Devices(ctx context.Context) ([]matter.Device, error) {
	f.mu.Lock()
	down := f.down
	f.mu.Unlock()

	if down != nil {
		select {
		case <-down:
		default:
			return nil, errHub
		}
	}

	return f.Home.Devices(ctx)
}

func (f *fickle) Watch(ctx context.Context, handle func(matter.Event)) error {
	watching, end := context.WithCancel(ctx)
	defer end()

	f.mu.Lock()
	f.end = end
	f.watches++
	f.mu.Unlock()

	err := f.Home.Watch(watching, handle)

	f.mu.Lock()
	defer f.mu.Unlock()

	if ctx.Err() == nil && f.ending != nil {
		return f.ending
	}

	return err
}

// fail ends the Watch in flight with err. With down, the source answers
// nothing until it is closed.
func (f *fickle) fail(err error, down chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ending = err
	f.down = down
	f.end()
}

func (f *fickle) watched() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.watches
}

// TestASourceInTrouble: a source whose Watch fails has its devices
// unreachable and is watched again; one that is over has them removed.
func TestASourceInTrouble(t *testing.T) {
	t.Parallel()

	source := &fickle{Home: &matter.Home{}}
	lamp := mattertest.Light(source.Home, "lamp")
	mattertest.Plug(source.Home, "plug")

	served := serve(t, source)
	endpoint, plug := served.endpoint(t, "d:lamp"), served.endpoint(t, "d:plug")

	if !served.reachable(t, endpoint) {
		t.Fatal("the lamp starts unreachable")
	}

	// Down: unreachable for as long as it is, and not watched.
	down := make(chan struct{})
	source.fail(errHub, down)

	served.eventually(t, "the lamp unreachable while its source is down", func() bool {
		return !served.reachable(t, endpoint)
	})

	// Longer than it is left before it is asked again: asked, it does
	// not answer, and is not watched.
	time.Sleep(3 * time.Second)

	if source.watched() != 1 || served.reachable(t, plug) || served.reachable(t, endpoint) {
		t.Fatalf(
			"a source that is down: watched %d times, the plug reachable %t",
			source.watched(),
			served.reachable(t, plug),
		)
	}

	// What it lets go of while it is down goes when it is back; what it
	// still holds is told of anew, reachable, where it was.
	source.Remove("plug")
	close(down)

	served.eventually(t, "the source watched again", func() bool {
		return source.watched() > 1 && served.reachable(t, endpoint)
	})

	served.eventually(t, "the plug gone", func() bool {
		_, still := served.device.EndpointOf("d:plug")

		return !still
	})

	if again := served.endpoint(t, "d:lamp"); again != endpoint {
		t.Fatalf("the lamp came back at endpoint %d, from %d", again, endpoint)
	}

	if status := served.status(t, endpoint, onOff, 1, nil); status != 0 || len(lamp.Asked()) != 1 {
		t.Fatalf("the lamp after its source came back: status %#x, asked %+v", status, lamp.Asked())
	}

	// Over for good: removed, and watched no more.
	watched := source.watched()

	source.fail(matter.ErrClosed, nil)

	served.eventually(t, "the lamp gone with its source", func() bool {
		_, still := served.device.EndpointOf("d:lamp")

		return !still
	})

	time.Sleep(3 * time.Second)

	if source.watched() != watched {
		t.Fatal("a source that is over was watched again")
	}
}

// described is a source that tells of devices no Home would hold.
type described []matter.Device

func (d described) Devices(context.Context) ([]matter.Device, error) { return d, nil }

func (d described) Watch(ctx context.Context, handle func(matter.Event)) error {
	for _, device := range d {
		handle(matter.Event{Device: device.ID, Change: matter.DeviceAdded{Device: device}})
	}

	<-ctx.Done()

	return ctx.Err()
}

// TestADeviceThatCannotBeServed is left out, and the others are served.
func TestADeviceThatCannotBeServed(t *testing.T) {
	t.Parallel()

	set := func(context.Context, bool) error { return nil }

	served := serve(t, described{
		{ID: "nothing", Kind: matter.Light},
		{ID: "lamp", Kind: matter.Light, OnOff: &matter.OnOff{Set: set}},
	})

	served.endpoint(t, "d:lamp")

	if _, is := served.device.EndpointOf("d:nothing"); is {
		t.Fatal("a light with no OnOff is served")
	}
}

// TestABridgeIsHeld: closed to everyone until Invite, then held, by
// several, and let go.
func TestABridgeIsHeld(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()

	secret, err := node.NewCommissioningCredentials()
	if err != nil {
		t.Fatal(err)
	}

	site := fabrictest.NewSite(t, fabrictest.Options{Clock: clock.System{}})
	device := site.AddNode(fabric.NodeConfig(fabric.Identity{Name: "Test bridge"}, secret, "", nil, ""))

	bridge := fabric.NewBridge(device.Node(), &home)

	// Open to no one: its node opens no window by itself.
	if device.Node().Window().Open {
		t.Fatal("a bridge is commissionable before it is asked to be")
	}

	held, err := bridge.Fabrics(ctx)
	if err != nil || len(held) != 0 {
		t.Fatalf("a bridge no one commissioned is held by %+v, %v", held, err)
	}

	for _, within := range []time.Duration{0, -time.Minute} {
		if _, _, err = bridge.Invite(ctx, within); !errors.Is(err, matter.ErrInvalid) {
			t.Fatalf("an invitation of %s: %v", within, err)
		}
	}

	// Invited: its own code, the same each time, and it can be held.
	manual, qr, err := bridge.Invite(ctx, 10*time.Minute)
	if err != nil || len(manual) != 11 || len(qr) < 4 || qr[:3] != "MT:" {
		t.Fatalf("the first invitation: %q, %q, %v", manual, qr, err)
	}

	if again, _, _ := bridge.Invite(ctx, 10*time.Minute); again != manual {
		t.Fatalf("the bridge's own code changed: %s, then %s", manual, again)
	}

	if !device.Node().Window().Open {
		t.Fatal("an invited bridge is not open")
	}

	pair, err := site.AddController(fabrictest.ControllerOptions{}).
		Commission(ctx, device, fabrictest.CommissionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// An administrator that holds the bridge lets another in by itself:
	// its window is its own to open, and is left open.
	holder, err := pair.Node()
	if err != nil {
		t.Fatal(err)
	}

	if _, err = holder.OpenCommissioningWindow(ctx, 3*time.Minute, client.WindowOptions{}); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	if !device.Node().Window().Open {
		t.Fatal("the window an administrator opened was closed")
	}

	if err = device.Node().CloseWindow(ctx); err != nil {
		t.Fatal(err)
	}

	held, err = bridge.Fabrics(ctx)
	if err != nil || len(held) != 1 || held[0].Index != 1 {
		t.Fatalf("a commissioned bridge is held by %+v, %v", held, err)
	}

	// Beside one that holds it: a code of the occasion, not its own.
	other, otherQR, err := bridge.Invite(ctx, 5*time.Minute)
	if err != nil || len(other) != 11 || other == manual || otherQR == qr {
		t.Fatalf("an invitation beside a holder: %q, %v", other, err)
	}

	// And that code lets a second administrator in.
	second, err := controller.New(ctx, site.ControllerConfig(fabrictest.ControllerOptions{}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = second.Close(context.WithoutCancel(ctx)) })

	if _, err = second.Pair(
		ctx,
		other,
		controller.PairOptions{OnAttestationFailure: attestation.Accept()},
	); err != nil {
		t.Fatalf("a second administrator, with the code of the occasion: %v", err)
	}

	if held, err = bridge.Fabrics(ctx); err != nil || len(held) != 2 {
		t.Fatalf("a bridge two commissioned is held by %+v, %v", held, err)
	}

	if err = bridge.Remove(ctx, 2); err != nil {
		t.Fatal(err)
	}

	// Let go.
	for _, absent := range []int{0, 2, 255, -1} {
		if err = bridge.Remove(ctx, absent); !errors.Is(err, fabric.ErrNoFabric) {
			t.Fatalf("removing fabric %d: %v", absent, err)
		}
	}

	if err = bridge.Remove(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if held, err = bridge.Fabrics(ctx); err != nil || len(held) != 0 {
		t.Fatalf("after its one fabric was removed: %+v, %v", held, err)
	}

	// Held by no one again, it is open to no one, and stays so.
	for deadline := time.Now().Add(5 * time.Second); device.Node().Window().Open; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a bridge let go is commissionable without being asked")
		}
	}

	time.Sleep(200 * time.Millisecond)

	if device.Node().Window().Open {
		t.Fatal("a bridge let go opened again without being asked")
	}

	// Closed.
	if err = bridge.Close(); err != nil {
		t.Fatal(err)
	}

	_, _, inviteErr := bridge.Invite(ctx, time.Minute)
	_, fabricsErr := bridge.Fabrics(ctx)

	for name, err := range map[string]error{
		"Close": bridge.Close(), "Invite": inviteErr, "Fabrics": fabricsErr, "Remove": bridge.Remove(ctx, 1),
	} {
		if !errors.Is(err, matter.ErrClosed) {
			t.Errorf("%s of a closed bridge: %v", name, err)
		}
	}
}
