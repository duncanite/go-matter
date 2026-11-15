package controller_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/controller"
)

const (
	dim  = 0.3
	fade = 2 * time.Second
)

// A controller that pairs a node by the code its owner typed, dims over
// two seconds every light that is on, and prints what happens after.
func Example() {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	control, err := controller.Open(ctx, controller.Config{
		Name:  "House",
		Dir:   "/var/lib/house",
		Trust: controller.Development,
	})
	if err != nil {
		return
	}

	defer func() { _ = control.Close() }()

	node, err := control.Pair(ctx, "34970112332")
	if err != nil {
		return
	}

	devices, err := node.Devices(ctx)
	if err != nil {
		return
	}

	for index := range devices {
		device := &devices[index]
		if device.LevelControl == nil || !device.OnOff.On {
			continue
		}

		// What the device refused behind its bridge is what is refused
		// here, as the same error.
		err = device.LevelControl.Set(ctx, dim, fade)
		if errors.Is(err, matter.ErrUnreachable) {
			fmt.Println(device.Name, "is not answering")
		}
	}

	_ = control.Watch(ctx, func(event matter.Event) {
		switch change := event.Change.(type) {
		case matter.LevelChanged:
			fmt.Println(event.Device, "level:", change.Level)
		case matter.ButtonPressed:
			fmt.Println(event.Device, "button", change.Button, "down")
		}
	})
}
