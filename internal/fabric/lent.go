package fabric

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"sync"

	fabricbridge "thingfabric.dev/go-fabric/bridge"
	"thingfabric.dev/go-fabric/mdns"
)

// The services a node is found as: open to be commissioned, and as a
// node of a fabric that holds it.
const (
	commissionable = "_matterc._udp"
	operational    = "_matter._tcp"
)

// Lent is the host's part in DNS-SD as a bridge is lent it: package
// bridge's Network, in this package's own words.
type Lent interface {
	// Announce announces a service, in place of the one of its name and
	// type.
	Announce(ctx context.Context, service Announced) error
	// Withdraw says the service of that name and type is gone.
	Withdraw(ctx context.Context, name, kind string) error
	// Resolve is where the instance of that name and type is.
	Resolve(ctx context.Context, name, kind string) ([]netip.AddrPort, error)
}

// Announced is one service a bridge announces: package bridge's Service.
type Announced struct {
	Name     string
	Type     string
	Subtypes []string
	Port     uint16
	// Text is the service's keys and values, in the order they are given.
	Text [][2]string
}

// announcer is what a bridge is lent, as the advertiser a node speaks
// DNS-SD through. It keeps what this bridge announced, and that only:
// what else the lender announces is not the bridge's to know or to
// withdraw.
type announcer struct {
	lent Lent

	mu sync.Mutex
	// held is what is announced now, by name and type, and said the
	// order it was first announced in.
	held map[[2]string]mdns.Service
	said [][2]string
	// refused is, for a service whose announcement was last refused, why:
	// a node writes such a failure down and goes on.
	refused map[[2]string]error
}

func newAnnouncer(lent Lent) *announcer {
	return &announcer{lent: lent, held: map[[2]string]mdns.Service{}, refused: map[[2]string]error{}}
}

// Publish is mdns.Advertiser's. The host name a node gives with a
// service is left out: a service is announced under its lender's own
// host name, the one name a host has. A service announced again as it
// stands is not said again: a node publishes anew whenever a controller
// goes, and DNS-SD has a record announced when it is new or has changed.
func (a *announcer) Publish(ctx context.Context, service mdns.Service) error {
	named := [2]string{service.InstanceName, service.ServiceType}

	a.mu.Lock()
	was, had := a.held[named]
	a.mu.Unlock()

	if had && reflect.DeepEqual(was, service) {
		return nil
	}

	text := make([][2]string, 0, len(service.TXT))
	for _, entry := range service.TXT {
		text = append(text, [2]string{entry.Key, entry.Value})
	}

	err := a.lent.Announce(ctx, Announced{
		Name: service.InstanceName, Type: service.ServiceType, Subtypes: append([]string(nil), service.Subtypes...),
		Port: service.Port, Text: text,
	})
	a.mu.Lock()
	defer a.mu.Unlock()

	if err != nil {
		a.refused[named] = fmt.Errorf("announcing %s.%s: %w", service.InstanceName, service.ServiceType, err)

		return a.refused[named]
	}

	delete(a.refused, named)

	if _, had = a.held[named]; !had {
		a.said = append(a.said, named)
	}

	a.held[named] = service

	return nil
}

// Withdraw is mdns.Advertiser's. What this bridge did not announce is
// not withdrawn.
func (a *announcer) Withdraw(ctx context.Context, instanceName, serviceType string) error {
	named := [2]string{instanceName, serviceType}

	a.mu.Lock()
	_, had := a.held[named]
	delete(a.held, named)
	delete(a.refused, named)

	for index, one := range a.said {
		if one == named {
			a.said = append(a.said[:index], a.said[index+1:]...)

			break
		}
	}
	a.mu.Unlock()

	if !had {
		return nil
	}

	if err := a.lent.Withdraw(ctx, instanceName, serviceType); err != nil {
		return fmt.Errorf("withdrawing %s.%s: %w", instanceName, serviceType, err)
	}

	return nil
}

// Active is mdns.Advertiser's.
func (a *announcer) Active() []mdns.Service {
	a.mu.Lock()
	defer a.mu.Unlock()

	active := make([]mdns.Service, 0, len(a.said))
	for _, named := range a.said {
		active = append(active, a.held[named])
	}

	return active
}

// Close is mdns.Advertiser's: a node closes its advertiser when it
// stops. What this bridge announced is withdrawn, and what it was lent
// is left as it is, since it is not the bridge's to close.
func (a *announcer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), stopping)
	defer cancel()

	for _, service := range a.Active() {
		if err := a.Withdraw(ctx, service.InstanceName, service.ServiceType); err != nil {
			return err
		}
	}

	return nil
}

// failed is why a service of a type is not announced, when the last
// announcement of one was refused.
func (a *announcer) failed(kind string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	for named, why := range a.refused {
		if named[1] == kind {
			return why
		}
	}

	return nil
}

// resolver is what a bridge is lent, as what a node finds a controller
// again with: the operational instance of a node on a fabric.
type resolver struct{ lent Lent }

var _ fabricbridge.OperationalResolver = resolver{}

// ResolveOperational is fabricbridge.OperationalResolver's.
func (r resolver) ResolveOperational(
	ctx context.Context, compressedFabricID [8]byte, nodeID uint64,
) ([]*net.UDPAddr, error) {
	name := fmt.Sprintf("%X-%016X", compressedFabricID[:], nodeID)

	found, err := r.lent.Resolve(ctx, name, operational)
	if err != nil {
		return nil, fmt.Errorf("finding %s.%s: %w", name, operational, err)
	}

	addresses := make([]*net.UDPAddr, 0, len(found))
	for _, at := range found {
		addresses = append(addresses, net.UDPAddrFromAddrPort(at))
	}

	return addresses, nil
}
