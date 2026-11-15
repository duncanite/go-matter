package matter_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/duncanite/go-matter"
)

// hub stands for a maker's library: what an adapter is written over. It
// knows a zone's brightness, zero for off, and nothing of where a zone
// stood before it went off.
type hub struct{}

func (hub) Zones(context.Context) ([]string, error)                      { return nil, nil }
func (hub) Brightness(context.Context, string) (int, error)              { return 0, nil }
func (hub) Fade(context.Context, string, int, time.Duration) error       { return nil }
func (hub) Listen(context.Context, func(zone string, percent int)) error { return nil }

// errOffline stands for an error of the maker's own.
var errOffline = errors.New("hub: offline")

const full = 100

// feed is the whole of an adapter: it writes a maker's dimmers into a
// home and keeps them current until ctx ends. The home is the Source;
// the adapter implements nothing.
func feed(ctx context.Context, from hub, home *matter.Home) error {
	zones, err := from.Zones(ctx)
	if err != nil {
		return err
	}

	for _, zone := range zones {
		percent, err := from.Brightness(ctx, zone)
		if err != nil {
			return err
		}

		// A zone that is off comes back to full until it has been seen
		// lit: the hub does not say where it stood.
		level := matter.Level(1)
		if percent > 0 {
			level = matter.Level(percent) / full
		}

		err = home.Put(matter.Device{
			ID: matter.ID(zone), Name: zone, Kind: matter.Light,
			OnOff: &matter.OnOff{
				On: percent > 0,
				Set: func(ctx context.Context, on bool) error {
					if !on {
						return translate(from.Fade(ctx, zone, 0, 0))
					}

					// Back to the level it last had, which the home kept.
					last, held := home.Device(matter.ID(zone))
					if !held {
						return matter.ErrUnreachable
					}

					return translate(from.Fade(ctx, zone, percentOf(last.LevelControl.Level), 0))
				},
			},
			LevelControl: &matter.LevelControl{
				Level: level,
				Set: func(ctx context.Context, level matter.Level, over time.Duration) error {
					return translate(from.Fade(ctx, zone, percentOf(level), over))
				},
			},
		})
		if err != nil {
			return err
		}
	}

	// What the hub says is reported as it says it: the home tells its
	// watchers only what changed. A zone gone off keeps its level.
	return from.Listen(ctx, func(zone string, percent int) {
		if percent == 0 {
			_ = home.Report(matter.ID(zone), matter.OnOffChanged{On: false})

			return
		}

		_ = home.Report(matter.ID(zone),
			matter.OnOffChanged{On: true},
			matter.LevelChanged{Level: matter.Level(percent) / full},
		)
	})
}

// percentOf is a level in the hub's unit, never zero: zero is off, and a
// level is not.
func percentOf(level matter.Level) int {
	return max(1, int(math.Round(float64(level)*full)))
}

// translate puts the maker's errors in the vocabulary's, so that they
// cross the protocol as what they are.
func translate(err error) error {
	if errors.Is(err, errOffline) {
		return fmt.Errorf("%w: %w", matter.ErrUnreachable, err)
	}

	return err
}

// A maker's dimmers, written into a Home by an adapter and read back as
// any Source is.
func Example() {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	var home matter.Home

	go func() { _ = feed(ctx, hub{}, &home) }()

	_ = home.Watch(ctx, func(event matter.Event) {
		switch change := event.Change.(type) {
		case matter.DeviceAdded:
			fmt.Println(change.Device.Name, "is here, on:", change.Device.OnOff.On)
		case matter.OnOffChanged:
			fmt.Println(event.Device, "on:", change.On)
		case matter.LevelChanged:
			fmt.Println(event.Device, "level:", change.Level)
		case matter.ReachabilityChanged:
			fmt.Println(event.Device, "unreachable:", change.Unreachable)
		}
	})
}
