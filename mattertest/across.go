package mattertest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/clock"
	"github.com/SukramJ/go-fabric/fabrictest"
	"github.com/SukramJ/go-fabric/node"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/internal/fabric"
)

// crossed is the far side of Across: every device the controller holds.
type crossed struct{ far *fabric.Controller }

func (c crossed) Devices(ctx context.Context) ([]matter.Device, error) {
	return c.far.Devices(ctx, nil)
}

func (c crossed) Watch(ctx context.Context, handle func(matter.Event)) error {
	return c.far.Watch(ctx, 0, handle)
}

// Across serves near through a bridge, commissions that bridge with a
// controller, and returns the controller's view of it: the same devices,
// having crossed the protocol and back, in this process and on no
// network. What holds of near holds of what it returns, but for the
// devices' IDs, which every Source chooses for itself; a test of that is
// a test of this module. Both ends close with the test.
//
// It returns once the bridge is commissioned. A device of near is served
// a moment after it is put, and is the far side's a moment after that:
// a test waits for what it expects, as it would of any Source.
func Across(tb testing.TB, near matter.Source) matter.Source {
	tb.Helper()

	ctx := tb.Context()

	secret, err := node.NewCommissioningCredentials()
	if err != nil {
		tb.Fatal(err)
	}

	site := fabrictest.NewSite(tb, fabrictest.Options{Clock: clock.System{}})
	served := site.AddNode(fabric.NodeConfig(fabric.Identity{Name: "Bridge"}, secret, "", nil, ""))

	bridge := fabric.NewBridge(served.Node(), near)

	tb.Cleanup(func() { _ = bridge.Close() })

	control, err := fabric.NewController(
		ctx,
		site.ControllerConfig(fabrictest.ControllerOptions{}),
		fabric.TrustAnything,
	)
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = control.Close() })

	manual, _, err := bridge.Invite(ctx, time.Minute)
	if err != nil {
		tb.Fatal(err)
	}

	if _, err = control.Pair(ctx, manual); err != nil {
		tb.Fatal(fmt.Errorf("commissioning the bridge: %w", err))
	}

	return crossed{far: control}
}
