package bridge_test

import (
	"context"
	"fmt"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/bridge"
)

const invitation = 15 * time.Minute

// A bridge daemon, whole: home is what the makers' adapters write into.
func Example() {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var home matter.Home

	served, err := bridge.Open(ctx, bridge.Config{
		Name: "House bridge",
		Dir:  "/var/lib/house-bridge",
	}, &home)
	if err != nil {
		return
	}

	defer func() { _ = served.Close() }()

	// No one holds it yet: say how to.
	if held, _ := served.Fabrics(ctx); len(held) == 0 {
		code, err := served.Invite(ctx, invitation)
		if err != nil {
			return
		}

		fmt.Println("Commission with", code.Manual)
	}

	<-ctx.Done()
}
