package fabric_test

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/fabric"
	"github.com/duncanite/go-matter/mattertest"
)

// errTaken is what a network that refuses an announcement answers.
var errTaken = errors.New("the name is taken")

// refusing is a network of a test that refuses every announcement while
// it is told to, and takes them otherwise.
type refusing struct {
	mu      sync.Mutex
	refuses bool
}

func (r *refusing) Announce(context.Context, fabric.Announced) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.refuses {
		return errTaken
	}

	return nil
}

func (*refusing) Withdraw(context.Context, string, string) error { return nil }

func (*refusing) Resolve(context.Context, string, string) ([]netip.AddrPort, error) {
	return nil, nil
}

// TestAnInvitationNoOneCanFind: a bridge whose announcement the network
// refused is not left open. Its node opens the window whatever became
// of the announcement: left so, the bridge would take whoever has its
// code while its caller was told the invitation failed.
//
//nolint:paralleltest // it may take Matter's port, which a machine has one of
func TestAnInvitationNoOneCanFind(t *testing.T) {
	var home matter.Home

	mattertest.Light(&home, "lamp")

	network := &refusing{refuses: true}

	bridge, err := fabric.OpenBridge(
		t.Context(),
		fabric.Identity{Name: "Lent bridge"},
		t.TempDir(),
		nil,
		&home,
		network,
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = bridge.Close() })

	if _, _, err = bridge.Invite(t.Context(), time.Minute); !errors.Is(err, errTaken) {
		t.Fatalf("invited on a network that refuses: %v", err)
	}

	if bridge.Invited() {
		t.Fatal("a bridge whose announcement was refused is open all the same")
	}

	network.mu.Lock()
	network.refuses = false
	network.mu.Unlock()

	if _, _, err = bridge.Invite(t.Context(), time.Minute); err != nil || !bridge.Invited() {
		t.Fatalf("invited once the network refuses no more: open %t, %v", bridge.Invited(), err)
	}
}
