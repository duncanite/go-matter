package bridge_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/duncanite/go-matter"
	"github.com/duncanite/go-matter/bridge"
	"github.com/duncanite/go-matter/mattertest"
)

// TestOpenKeepsItsDirectory: a bridge opened in a directory is the same
// bridge at the next Open, with the same code, its secret its owner's
// alone.
//
//nolint:paralleltest // it takes Matter's port, which a machine has one of
func TestOpenKeepsItsDirectory(t *testing.T) {
	var home matter.Home

	mattertest.Light(&home, "lamp")

	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "bridge")

	// There already, and open to all: Open makes it its owner's alone.
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	for name, cfg := range map[string]bridge.Config{
		"no name":      {Dir: dir},
		"no directory": {Name: "Test bridge"},
	} {
		if _, err := bridge.Open(ctx, cfg, &home); !errors.Is(err, matter.ErrInvalid) {
			t.Fatalf("a bridge with %s: %v", name, err)
		}
	}

	if _, err := bridge.Open(
		ctx,
		bridge.Config{Name: "Test bridge", Dir: dir},
		nil,
	); !errors.Is(
		err,
		matter.ErrInvalid,
	) {
		t.Fatalf("a bridge with no source: %v", err)
	}

	var codes [2]matter.SetupCode

	for run := range codes {
		served, err := bridge.Open(ctx, bridge.Config{Name: "Test bridge", Dir: dir}, &home)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}

		held, err := served.Fabrics(ctx)
		if err != nil || len(held) != 0 {
			t.Fatalf("run %d: held by %+v, %v", run, held, err)
		}

		if codes[run], err = served.Invite(ctx, time.Minute); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}

		if err = served.Remove(ctx, 1); !errors.Is(err, bridge.ErrNoFabric) {
			t.Fatalf("run %d: removing a fabric that is not: %v", run, err)
		}

		if err = served.Close(); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}

		if err = served.Close(); !errors.Is(err, matter.ErrClosed) {
			t.Fatalf("run %d: closed twice: %v", run, err)
		}
	}

	if codes[0] != codes[1] || len(codes[0].Manual) != 11 || codes[0].QR[:3] != "MT:" {
		t.Fatalf("the bridge's code: %+v, then %+v", codes[0], codes[1])
	}

	if runtime.GOOS == "windows" {
		return
	}

	secret, err := os.Stat(filepath.Join(dir, "secret.json"))
	if err != nil || secret.Mode().Perm() != 0o600 {
		t.Fatalf("the secret: %v, %v", secret, err)
	}

	kept, err := os.Stat(dir)
	if err != nil || kept.Mode().Perm() != 0o700 {
		t.Fatalf("the directory: %v, %v", kept, err)
	}
}
