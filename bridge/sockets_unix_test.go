//go:build unix

package bridge_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// mdnsPort is the port Multicast DNS is spoken on.
const mdnsPort = 5353

// onMDNS is how many sockets of this process are bound to the port of
// Multicast DNS.
func onMDNS() int {
	const descriptors = 4096

	count := 0

	for descriptor := range descriptors {
		name, err := syscall.Getsockname(descriptor)
		if err != nil {
			continue
		}

		switch at := name.(type) {
		case *syscall.SockaddrInet4:
			if at.Port == mdnsPort {
				count++
			}
		case *syscall.SockaddrInet6:
			if at.Port == mdnsPort {
				count++
			}
		}
	}

	return count
}

// most is the most sockets on Multicast DNS's port seen over a moment:
// a bridge opens its own when it first has something to announce.
func most() int {
	seen := 0

	for range 20 {
		seen = max(seen, onMDNS())

		time.Sleep(50 * time.Millisecond)
	}

	return seen
}

// TestALentNetworkIsTheOnlyOne: a bridge that is lent a network opens no
// socket for Multicast DNS, where one with its own does: a host has one
// part in it, and two on an interface do not hear each other's answers.
//
//nolint:paralleltest // it may take Matter's port, and counts the process's sockets
func TestALentNetworkIsTheOnlyOne(t *testing.T) {
	before := onMDNS()

	lent := opened(t, &lender{}, filepath.Join(t.TempDir(), "lent"))

	if _, err := lent.Invite(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}

	if with := most(); with != before {
		t.Errorf("lent a network, the process has %d sockets on Multicast DNS's port, where it had %d", with, before)
	}

	if err := lent.Close(); err != nil {
		t.Fatal(err)
	}

	// The count means something only where a bridge with a network of
	// its own is seen to open sockets there.
	own := opened(t, nil, filepath.Join(t.TempDir(), "own"))

	t.Cleanup(func() { _ = own.Close() })

	if _, err := own.Invite(t.Context(), time.Minute); err != nil {
		t.Fatal(err)
	}

	if with := most(); with <= before {
		t.Skipf(
			"a bridge with its own network shows %d sockets on Multicast DNS's port here, as before it: nothing to tell by",
			with,
		)
	}
}
