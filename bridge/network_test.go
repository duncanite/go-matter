package bridge_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/bridge"
	"github.com/duncanite/go-matter/mattertest"
)

// The services a node is found as.
const (
	commissionable = "_matterc._udp"
	operational    = "_matter._tcp"
)

// said is one thing a bridge asked of the network it was lent.
type said struct {
	// withdrawn is a Withdraw; the service then holds its name and type
	// alone.
	withdrawn bool
	service   bridge.Service
}

// lender is a network of a test: it writes down what a bridge asks of
// it, and announces nothing.
type lender struct {
	mu    sync.Mutex
	asked []said
	// refuses is what Announce answers, when it is to refuse.
	refuses error
}

func (l *lender) Announce(_ context.Context, service bridge.Service) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.refuses != nil {
		return l.refuses
	}

	service.Subtypes, service.Text = slices.Clone(service.Subtypes), slices.Clone(service.Text)
	l.asked = append(l.asked, said{service: service})

	return nil
}

func (l *lender) Withdraw(_ context.Context, name, kind string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.asked = append(l.asked, said{withdrawn: true, service: bridge.Service{Name: name, Type: kind}})

	return nil
}

func (*lender) Resolve(context.Context, string, string) ([]netip.AddrPort, error) {
	return nil, nil
}

// all is everything asked so far, in order.
func (l *lender) all() []said {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.asked)
}

// standing is what stands announced: each service announced and not
// withdrawn since, as it was last announced.
func (l *lender) standing() []bridge.Service {
	var now []bridge.Service

	for _, one := range l.all() {
		now = slices.DeleteFunc(now, func(was bridge.Service) bool {
			return was.Name == one.service.Name && was.Type == one.service.Type
		})

		if !one.withdrawn {
			now = append(now, one.service)
		}
	}

	return now
}

// opened is a bridge over a lamp, lent a network, in a directory.
func opened(t *testing.T, network bridge.Network, dir string) *bridge.Bridge {
	t.Helper()

	var home matter.Home

	mattertest.Light(&home, "lamp")

	served, err := bridge.Open(t.Context(), bridge.Config{Name: "Lent bridge", Dir: dir, Network: network}, &home)
	if err != nil {
		t.Fatal(err)
	}

	return served
}

// TestABridgeLentANetwork: a bridge that is lent a network says through
// it what it is found as: nothing while it is closed to everyone, itself
// as Matter has a node that can be commissioned announced once it is
// invited, and nothing again once it is closed.
//
//nolint:paralleltest // it may take Matter's port, which a machine has one of
func TestABridgeLentANetwork(t *testing.T) {
	network := &lender{}
	served := opened(t, network, filepath.Join(t.TempDir(), "bridge"))

	if standing := network.standing(); len(standing) != 0 {
		t.Fatalf("a bridge no one was invited to stands announced as %+v", standing)
	}

	if _, err := served.Invite(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}

	standing := network.standing()
	if len(standing) != 1 {
		t.Fatalf("invited, the bridge stands announced as %+v", standing)
	}

	found := standing[0]

	// Matter's own shape: a name of sixteen hexadecimal digits, the port
	// it answers on, and what tells a controller which node this is
	// before it asks anything of it.
	if found.Type != commissionable || !hexadecimal(found.Name, 16) || found.Port == 0 {
		t.Errorf("the bridge is announced as %+v", found)
	}

	for _, begins := range []string{"_L", "_S", "_CM", "_T14", "_V"} {
		if !slices.ContainsFunc(found.Subtypes, func(one string) bool { return strings.HasPrefix(one, begins) }) {
			t.Errorf("the bridge is found under %v, and under nothing that begins with %s", found.Subtypes, begins)
		}
	}

	text := map[string]string{}
	for _, entry := range found.Text {
		text[entry.Key] = entry.Value
	}

	if text["DN"] != "Lent bridge" || text["DT"] != "14" || text["CM"] != "1" || text["D"] == "" || text["VP"] == "" {
		t.Errorf("the bridge's text is %v", found.Text)
	}

	if err := served.Close(); err != nil {
		t.Fatal(err)
	}

	if standing = network.standing(); len(standing) != 0 {
		t.Fatalf("closed, the bridge stands announced as %+v", standing)
	}
}

// TestAnInvitationAndTheNetwork: an invitation made again while it
// stands asks nothing more of the network, since nothing of what is
// announced changed; one that runs out is withdrawn; a network that
// refuses the announcement is the invitation's failure, with why, and
// is asked to withdraw nothing; and the invitation after it is made.
//
//nolint:paralleltest // it may take Matter's port, which a machine has one of
func TestAnInvitationAndTheNetwork(t *testing.T) {
	network := &lender{}
	served := opened(t, network, filepath.Join(t.TempDir(), "bridge"))

	t.Cleanup(func() { _ = served.Close() })

	if _, err := served.Invite(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}

	asked := len(network.all())

	if _, err := served.Invite(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}

	if more := network.all()[asked:]; len(more) != 0 {
		t.Fatalf("invited again as it stood, the bridge asked %+v", more)
	}

	// The shortest an invitation lasts: it ends by itself.
	if _, err := served.Invite(t.Context(), time.Second); err != nil {
		t.Fatal(err)
	}

	for deadline := time.Now().Add(10 * time.Second); len(network.standing()) != 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("an invitation that ran out still stands announced as %+v", network.standing())
		}
	}

	network.mu.Lock()
	network.refuses = errRefused
	network.mu.Unlock()

	asked = len(network.all())

	if _, err := served.Invite(t.Context(), time.Minute); !errors.Is(err, errRefused) {
		t.Fatalf("invited on a network that refuses: %v", err)
	}

	// What was never announced is not withdrawn: the network is asked
	// nothing of a name it does not hold, which may be another's.
	if more := network.all()[asked:]; len(more) != 0 {
		t.Fatalf("refused, the bridge asked the network %+v", more)
	}

	// A refusal is not held against the invitations that follow.
	network.mu.Lock()
	network.refuses = nil
	network.mu.Unlock()

	if _, err := served.Invite(t.Context(), time.Minute); err != nil {
		t.Fatalf("invited once the network refuses no more: %v", err)
	}

	if standing := network.standing(); len(standing) != 1 {
		t.Fatalf("invited once the network refuses no more, the bridge stands announced as %+v", standing)
	}
}

// errRefused is what a network that refuses an announcement answers.
var errRefused = errors.New("the name is taken")

// hexadecimal says whether a name is that many hexadecimal digits, in
// capitals.
func hexadecimal(name string, digits int) bool {
	return len(name) == digits && strings.Trim(name, "0123456789ABCDEF") == ""
}

// TestHostName: the name Matter has a host take is its hardware address
// in capitals, twelve digits of a MAC address and sixteen of an extended
// one, and nothing else is one.
func TestHostName(t *testing.T) {
	t.Parallel()

	for written, want := range map[string]string{
		"02:00:0a:00:02:04":       "02000A000204",
		"AB:CD:EF:01:23:45":       "ABCDEF012345",
		"00:00:00:00:00:01":       "000000000001",
		"02:00:0a:ff:fe:00:02:04": "02000AFFFE000204",
	} {
		hardware, err := net.ParseMAC(written)
		if err != nil {
			t.Fatal(err)
		}

		name, err := bridge.HostName(hardware)
		if err != nil || name != want || !hexadecimal(name, len(want)) {
			t.Errorf("the host of %s is %q, %v", written, name, err)
		}
	}

	for what, hardware := range map[string]net.HardwareAddr{
		"no address":          nil,
		"five bytes":          {2, 0, 10, 0, 2},
		"seven bytes":         {2, 0, 10, 0, 2, 4, 6},
		"twenty bytes":        make(net.HardwareAddr, 20),
		"a MAC of zeros":      make(net.HardwareAddr, 6),
		"an extended of zero": make(net.HardwareAddr, 8),
	} {
		if name, err := bridge.HostName(hardware); !errors.Is(err, matter.ErrInvalid) || name != "" {
			t.Errorf("the host of %s is %q, %v", what, name, err)
		}
	}
}

// TestANetworkWithAnInterface: a bridge that is lent a network names no
// interface: where it is found is its lender's to say.
func TestANetworkWithAnInterface(t *testing.T) {
	t.Parallel()

	var home matter.Home

	_, err := bridge.Open(
		t.Context(),
		bridge.Config{Name: "Lent bridge", Dir: t.TempDir(), Interface: "lo0", Network: &lender{}},
		&home,
	)
	if !errors.Is(err, matter.ErrInvalid) || !strings.Contains(err.Error(), "lender") {
		t.Fatalf("a lent network with an interface: %v", err)
	}
}
