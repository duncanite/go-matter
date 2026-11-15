package controller_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/controller"
)

// TestOpenKeepsItsDirectory: a controller opens in a directory it makes
// its owner's alone, holds nothing at first, refuses what is not a setup
// code, and is the same controller at the next Open.
func TestOpenKeepsItsDirectory(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "controller")

	for name, cfg := range map[string]controller.Config{
		"no name":       {Dir: dir},
		"no directory":  {Name: "Test"},
		"no such trust": {Name: "Test", Dir: dir, Trust: 9},
	} {
		if _, err := controller.Open(ctx, cfg); !errors.Is(err, matter.ErrInvalid) {
			t.Fatalf("a controller with %s: %v", name, err)
		}
	}

	// On Windows a controller says it does not run, and no more is asked
	// of it.
	if runtime.GOOS == "windows" {
		_, err := controller.Open(ctx, controller.Config{Name: "Test", Dir: dir})
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("a controller on Windows: %v", err)
		}

		return
	}

	for _, trust := range []controller.Trust{controller.Certified, controller.Development, controller.Anything} {
		control, err := controller.Open(ctx, controller.Config{Name: "Test", Dir: dir, Trust: trust})
		if err != nil {
			t.Fatalf("trust %d: %v", trust, err)
		}

		if nodes := control.Nodes(); len(nodes) != 0 {
			t.Fatalf("a controller that paired nothing holds %d nodes", len(nodes))
		}

		if _, held := control.Node(1); held {
			t.Fatal("a controller that paired nothing holds node 1")
		}

		devices, err := control.Devices(ctx)
		if err != nil || len(devices) != 0 {
			t.Fatalf("a controller that paired nothing has %+v, %v", devices, err)
		}

		if _, err = control.Pair(ctx, "not a code"); !errors.Is(err, controller.ErrSetupCode) {
			t.Fatalf("pairing with what is not a code: %v", err)
		}

		if err = control.Close(); err != nil {
			t.Fatal(err)
		}

		if err = control.Close(); !errors.Is(err, matter.ErrClosed) {
			t.Fatalf("closed twice: %v", err)
		}

		if _, err = control.Devices(ctx); !errors.Is(err, matter.ErrClosed) {
			t.Fatalf("the devices of a closed controller: %v", err)
		}
	}

	kept, err := os.Stat(dir)
	if err != nil || kept.Mode().Perm() != 0o700 {
		t.Fatalf("the directory: %v, %v", kept, err)
	}
}
