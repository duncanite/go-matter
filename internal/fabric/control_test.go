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
	"github.com/SukramJ/go-fabric/fabrictest"
	"github.com/SukramJ/go-fabric/node"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/fabric"
	"github.com/duncanite/go-matter/mattertest"
)

// town is a site with bridges on it, each over a home, and the
// configuration of a controller for it.
type town struct {
	site *fabrictest.Site
}

func newTown(t *testing.T) *town {
	t.Helper()

	return &town{site: fabrictest.NewSite(t, fabrictest.Options{Clock: clock.System{}})}
}

// bridge serves a home on the site, invited and not commissioned.
func (w *town) bridge(t *testing.T, name string, home *matter.Home) (*fabrictest.Device, *fabric.Bridge, string) {
	t.Helper()

	secret, err := node.NewCommissioningCredentials()
	if err != nil {
		t.Fatal(err)
	}

	served := w.site.AddNode(fabric.NodeConfig(fabric.Identity{Name: name, Serial: "B-1"}, secret, "", nil, ""))

	bridge := fabric.NewBridge(served.Node(), home)

	t.Cleanup(func() { _ = bridge.Close() })

	manual, _, err := bridge.Invite(t.Context(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	return served, bridge, manual
}

// controller is a controller on the site, under a trust and with that
// trust's own roots.
func (w *town) controller(t *testing.T, trust fabric.Trust, edit func(*controller.Config)) *fabric.Controller {
	t.Helper()

	cfg := w.site.ControllerConfig(fabrictest.ControllerOptions{})

	roots, err := fabric.Trusted(trust)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Attestation = roots

	if edit != nil {
		edit(&cfg)
	}

	control, err := fabric.NewController(t.Context(), cfg, trust)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = control.Close() })

	return control
}

func soon(t *testing.T, what string, so func() bool) {
	t.Helper()

	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if so() {
			return
		}
	}

	t.Fatalf("never so: %s", what)
}

// A bridge is found, by what would pair with it, as a bridge: not as the
// lamp it happens to serve.
func TestABridgeIsFoundAsABridge(t *testing.T) {
	t.Parallel()

	var home matter.Home

	mattertest.Light(&home, "lamp")

	served, _, _ := newTown(t).bridge(t, "Test bridge", &home)

	soon(t, "the bridge is found as a bridge", func() bool {
		for _, service := range served.Node().MDNSServices() {
			if service.ServiceType != "_matterc._udp" {
				continue
			}

			for _, said := range service.TXT {
				if said.Key == "DT" {
					return said.Value == "14"
				}
			}
		}

		return false
	})
}

// TestANodeIsHeld: paired, described, invited to another, and given up.
func TestANodeIsHeld(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()

	mattertest.Light(&home, "lamp")

	place := newTown(t)
	_, bridge, code := place.bridge(t, "Test bridge", &home)
	control := place.controller(t, fabric.TrustAnything, nil)

	if nodes := control.HeldNodes(); len(nodes) != 0 {
		t.Fatalf("a controller that paired nothing holds %d nodes", len(nodes))
	}

	for name, bad := range map[string]string{"no code": "", "letters": "not a code", "too short": "1234"} {
		if _, err := control.PairNode(ctx, bad); !errors.Is(err, fabric.ErrSetupCode) {
			t.Errorf("pairing with %s: %v", name, err)
		}
	}

	paired, err := control.PairNode(ctx, "  "+code+"\n")
	if err != nil {
		t.Fatal(err)
	}

	if paired.ID() == 0 {
		t.Fatal("a paired node has no id")
	}

	if nodes := control.HeldNodes(); len(nodes) != 1 || nodes[0].ID() != paired.ID() {
		t.Fatalf("the controller holds %d nodes", len(nodes))
	}

	if said := paired.Describe(); said.Name != "Test bridge" || said.Serial != "B-1" {
		t.Fatalf("the node says it is %+v", said)
	}

	if fabrics, _ := bridge.Fabrics(ctx); len(fabrics) != 1 {
		t.Fatalf("the bridge is held by %+v", fabrics)
	}

	devices, err := paired.Devices(ctx)
	if err != nil || len(devices) != 1 || devices[0].Name != "lamp" || devices[0].Unreachable {
		t.Fatalf("the node's devices: %+v, %v", devices, err)
	}

	all, err := control.Devices(ctx, nil)
	if err != nil || len(all) != 1 || all[0].ID != devices[0].ID {
		t.Fatalf("the controller's devices: %+v, %v", all, err)
	}

	// A context that has ended is the caller's own error, not the
	// device's.
	ended, end := context.WithCancel(ctx)
	end()

	if err = devices[0].OnOff.Set(ended, true); !errors.Is(err, context.Canceled) || errors.Is(err, matter.ErrFailed) {
		t.Fatalf("a command under a context that ended: %v", err)
	}

	for _, within := range []time.Duration{0, -time.Second} {
		if _, _, err = paired.Invite(ctx, within); !errors.Is(err, matter.ErrInvalid) {
			t.Fatalf("an invitation of %s: %v", within, err)
		}
	}

	manual, qr, err := paired.Invite(ctx, 5*time.Minute)
	if err != nil || len(manual) != 11 || manual == code || len(qr) < 4 || qr[:3] != "MT:" {
		t.Fatalf("an invitation through the controller: %q, %q, %v", manual, qr, err)
	}

	// Given up, by two at once: once.
	watching := make(chan error, 1)

	go func() { watching <- paired.Watch(ctx, func(matter.Event) {}) }()

	time.Sleep(100 * time.Millisecond)

	var (
		both    sync.WaitGroup
		answers [2]error
	)

	for index := range answers {
		both.Go(func() { answers[index] = paired.Unpair(ctx) })
	}

	both.Wait()

	if (answers[0] == nil) == (answers[1] == nil) ||
		(answers[0] != nil && !errors.Is(answers[0], matter.ErrClosed)) ||
		(answers[1] != nil && !errors.Is(answers[1], matter.ErrClosed)) {
		t.Fatalf("a node given up by two at once: %v, %v", answers[0], answers[1])
	}

	select {
	case err = <-watching:
		if !errors.Is(err, matter.ErrClosed) {
			t.Fatalf("the watch of a node given up: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the watch of a node given up went on")
	}

	// Its devices go and stay gone, whatever the node said as it went.
	time.Sleep(300 * time.Millisecond)

	if all, err = control.Devices(ctx, nil); err != nil || len(all) != 0 {
		t.Fatalf("after its node was given up the controller holds %+v, %v", all, err)
	}

	if nodes := control.HeldNodes(); len(nodes) != 0 {
		t.Fatalf("after its node was given up the controller holds %d nodes", len(nodes))
	}

	if fabrics, _ := bridge.Fabrics(ctx); len(fabrics) != 0 {
		t.Fatalf("a bridge given up is held by %+v", fabrics)
	}

	if err = devices[0].OnOff.Set(ctx, true); !errors.Is(err, matter.ErrNoDevice) {
		t.Fatalf("a device of a node given up: %v", err)
	}

	if err = control.Watch(ctx, paired.ID(), func(matter.Event) {}); !errors.Is(err, matter.ErrClosed) {
		t.Fatalf("a watch started after its node was given up: %v", err)
	}

	_, devicesErr := paired.Devices(ctx)
	_, _, inviteErr := paired.Invite(ctx, time.Minute)

	for name, err := range map[string]error{
		"Devices": devicesErr, "Invite": inviteErr, "Unpair": paired.Unpair(ctx),
		"Watch": paired.Watch(ctx, func(matter.Event) {}),
	} {
		if !errors.Is(err, matter.ErrClosed) {
			t.Errorf("%s of a node given up: %v", name, err)
		}
	}
}

// TestAControllerCloses: every watch ends, and everything answers that
// it is closed.
func TestAControllerCloses(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()

	mattertest.Light(&home, "lamp")

	place := newTown(t)
	_, _, code := place.bridge(t, "Test bridge", &home)
	control := place.controller(t, fabric.TrustAnything, nil)

	paired, err := control.PairNode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}

	devices, err := paired.Devices(ctx)
	if err != nil || len(devices) != 1 {
		t.Fatal(devices, err)
	}

	ended := make(chan error, 2)

	go func() { ended <- control.Watch(ctx, 0, func(matter.Event) {}) }()
	go func() { ended <- paired.Watch(ctx, func(matter.Event) {}) }()

	time.Sleep(100 * time.Millisecond)

	if err = control.Close(); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		select {
		case err = <-ended:
			if !errors.Is(err, matter.ErrClosed) {
				t.Fatalf("a watch of a controller closed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a watch of a controller closed went on")
		}
	}

	_, pairErr := control.PairNode(ctx, code)
	_, devicesErr := control.Devices(ctx, nil)
	_, nodeErr := paired.Devices(ctx)

	for name, err := range map[string]error{
		"Close": control.Close(), "Pair": pairErr, "Devices": devicesErr, "a node's Devices": nodeErr,
		"Watch": control.Watch(ctx, 0, func(matter.Event) {}), "a command": devices[0].OnOff.Set(ctx, true),
		"Unpair": paired.Unpair(context.WithoutCancel(ctx)),
	} {
		if !errors.Is(err, matter.ErrClosed) {
			t.Errorf("%s of a closed controller: %v", name, err)
		}
	}

	if nodes := control.HeldNodes(); len(nodes) != 0 {
		t.Fatalf("a closed controller holds %d nodes", len(nodes))
	}
}

// TestAControllerIsOpenedAgain: over the state of one that held a node,
// it holds that node, and its devices.
func TestAControllerIsOpenedAgain(t *testing.T) {
	t.Parallel()

	var (
		home  matter.Home
		state controller.Config
	)

	ctx := t.Context()

	mattertest.Light(&home, "lamp")

	place := newTown(t)
	_, _, code := place.bridge(t, "Test bridge", &home)
	first := place.controller(t, fabric.TrustAnything, func(cfg *controller.Config) { state = *cfg })

	paired, err := first.PairNode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}

	if err = first.Close(); err != nil {
		t.Fatal(err)
	}

	opened := make(chan *fabric.Controller, 1)

	go func() {
		opened <- place.controller(t, fabric.TrustAnything, func(cfg *controller.Config) { cfg.DB = state.DB })
	}()

	var again *fabric.Controller

	select {
	case again = <-opened:
	case <-time.After(20 * time.Second):
		t.Fatal("a controller over the state of one that held a node never opened")
	}

	nodes := again.HeldNodes()
	if len(nodes) != 1 || nodes[0].ID() != paired.ID() {
		t.Fatalf("opened again, the controller holds %d nodes", len(nodes))
	}

	soon(t, "the node's device, held again", func() bool {
		devices, err := nodes[0].Devices(ctx)

		return err == nil && len(devices) == 1 && devices[0].Name == "lamp"
	})
}

// TestTrust: Matter's development identity, which a home-made bridge
// presents, is refused by Certified and taken by the other two.
func TestTrust(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	for name, row := range map[string]struct {
		trust fabric.Trust
		taken bool
	}{
		"certified":   {fabric.TrustCertified, false},
		"development": {fabric.TrustDevelopment, true},
		"anything":    {fabric.TrustAnything, true},
	} {
		var home matter.Home

		place := newTown(t)
		_, _, code := place.bridge(t, "Test bridge", &home)

		_, err := place.controller(t, row.trust, nil).PairNode(ctx, code)

		// Refused, it is for its root, which Certified does not hold: not
		// for being a test device under roots that would take it.
		var finding *attestation.Error

		switch {
		case row.taken && err != nil:
			t.Errorf("%s refused a bridge of the development identity: %v", name, err)
		case !row.taken && (!errors.Is(err, fabric.ErrUntrusted) || !errors.As(err, &finding) ||
			finding.Check != attestation.PaaNotTrusted):
			t.Errorf("%s, of a bridge of the development identity: %v", name, err)
		}
	}
}

// TestANodeThatDoesNotAnswer: its devices are unreachable, the other
// node's are not, and it is given up all the same.
func TestANodeThatDoesNotAnswer(t *testing.T) {
	t.Parallel()

	var home, other matter.Home

	ctx := t.Context()

	mattertest.Light(&home, "lamp")
	mattertest.Light(&other, "elsewhere")

	place := newTown(t)
	served, _, code := place.bridge(t, "Test bridge", &home)
	_, _, second := place.bridge(t, "Other bridge", &other)
	control := place.controller(t, fabric.TrustAnything, nil)

	paired, err := control.PairNode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}

	elsewhere, err := control.PairNode(ctx, second)
	if err != nil {
		t.Fatal(err)
	}

	// Each node's devices and watch are its own.
	mine, err := paired.Devices(ctx)
	if err != nil || len(mine) != 1 || mine[0].Name != "lamp" {
		t.Fatalf("the first node's devices: %+v, %v", mine, err)
	}

	theirs, err := elsewhere.Devices(ctx)
	if err != nil || len(theirs) != 1 || theirs[0].Name != "elsewhere" {
		t.Fatalf("the second node's devices: %+v, %v", theirs, err)
	}

	if all, _ := control.Devices(ctx, nil); len(all) != 2 {
		t.Fatalf("the controller's devices: %+v", all)
	}

	var (
		mu    sync.Mutex
		heard []matter.ID
	)

	watching, stop := context.WithCancel(ctx)
	defer stop()

	go func() {
		_ = paired.Watch(watching, func(event matter.Event) {
			mu.Lock()
			defer mu.Unlock()

			heard = append(heard, event.Device)
		})
	}()

	if err = other.Report("elsewhere", matter.OnOffChanged{On: true}); err != nil {
		t.Fatal(err)
	}

	if err = home.Report("lamp", matter.OnOffChanged{On: true}); err != nil {
		t.Fatal(err)
	}

	soon(t, "the first node's lamp heard on", func() bool {
		mu.Lock()
		defer mu.Unlock()

		return len(heard) >= 2
	})

	mu.Lock()

	for _, device := range heard {
		if device != mine[0].ID {
			t.Errorf("a watch of one node heard of %s", device)
		}
	}

	mu.Unlock()

	// The first node stops answering: its device is unreachable, the
	// other node's is not.
	if err = served.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	soon(t, "the device of a node that does not answer, unreachable", func() bool {
		devices, failed := paired.Devices(ctx)

		return failed == nil && len(devices) == 1 && devices[0].Unreachable
	})

	if theirs, _ = elsewhere.Devices(ctx); len(theirs) != 1 || theirs[0].Unreachable {
		t.Fatalf("the other node's device: %+v", theirs)
	}

	// It cannot be told, within the time the caller gives, and is given
	// up all the same.
	patient, enough := context.WithTimeout(ctx, 3*time.Second)
	defer enough()

	if err = paired.Unpair(patient); err != nil {
		t.Fatalf("giving up a node that does not answer: %v", err)
	}

	if nodes := control.HeldNodes(); len(nodes) != 1 || nodes[0].ID() != elsewhere.ID() {
		t.Fatalf("after giving up the one that does not answer, the controller holds %d nodes", len(nodes))
	}
}

// TestCloseWhileANodeIsGivenUp: the giving up ends first, and nothing
// after it finds a controller that is half closed.
func TestCloseWhileANodeIsGivenUp(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()

	mattertest.Light(&home, "lamp")

	place := newTown(t)
	served, _, code := place.bridge(t, "Test bridge", &home)
	control := place.controller(t, fabric.TrustAnything, nil)

	paired, err := control.PairNode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}

	watches := make(chan error, 2)

	go func() { watches <- control.Watch(ctx, 0, func(matter.Event) {}) }()
	go func() { watches <- paired.Watch(ctx, func(matter.Event) {}) }()

	if err = served.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	given := make(chan error, 1)

	go func() {
		patient, enough := context.WithTimeout(ctx, 4*time.Second)
		defer enough()

		given <- paired.Unpair(patient)
	}()

	time.Sleep(500 * time.Millisecond)

	if err = control.Close(); err != nil {
		t.Fatalf("Close while a node is given up: %v", err)
	}

	// Whatever the giving up answered, it had answered.
	select {
	case err = <-given:
		t.Logf("the giving up answered %v", err)
	default:
		t.Fatal("Close returned while the giving up was still under way")
	}

	for range 2 {
		select {
		case err = <-watches:
			if !errors.Is(err, matter.ErrClosed) {
				t.Fatalf("a watch, once closed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a watch went on past Close")
		}
	}
}

// TestGivenUpAgainAndAgain: a node says its last words as it is given up,
// at a moment of its own. However they fall, nothing of it is left.
func TestGivenUpAgainAndAgain(t *testing.T) {
	t.Parallel()

	var home matter.Home

	ctx := t.Context()

	mattertest.Light(&home, "lamp")

	place := newTown(t)
	_, bridge, code := place.bridge(t, "Test bridge", &home)
	control := place.controller(t, fabric.TrustAnything, nil)

	for round := range 12 {
		paired, err := control.PairNode(ctx, code)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}

		if err = paired.Unpair(ctx); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}

		time.Sleep(100 * time.Millisecond)

		if left, _ := control.Devices(ctx, nil); len(left) != 0 {
			t.Fatalf("round %d: a node given up left %+v", round, left)
		}

		if code, _, err = bridge.Invite(ctx, 10*time.Minute); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
}
