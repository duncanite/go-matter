package fabric

import (
	"github.com/SukramJ/go-fabric/node"
)

// Matter's identity for development: the vendor its test attestation is
// made for.
const (
	testVendor  uint16 = 0xFFF1
	testProduct uint16 = 0x8000
)

// aggregator is the device type a bridge is found as by what would pair
// with it. Left unsaid, the node says the type of whichever device it
// came to serve first: a bridge found as a lamp.
const aggregator uint16 = 0x000E

// Identity is who a bridge says it is.
type Identity struct {
	Name      string
	VendorID  uint16
	ProductID uint16
	Vendor    string
	Product   string
	Serial    string
}

// NodeConfig is the configuration of the node a bridge is: its identity,
// Matter's development one where none is given, the secret it is
// commissioned with, and where it keeps its state.
//
// listen is the address the node answers on; empty is Matter's own port,
// on every address.
func NodeConfig(
	identity Identity, secret node.Commissioning, store string, interfaces []string, listen string,
) node.Config {
	if identity.VendorID == 0 {
		identity.VendorID, identity.ProductID = testVendor, testProduct
	}

	if identity.Vendor == "" {
		identity.Vendor = identity.Name
	}

	if identity.Product == "" {
		identity.Product = identity.Name
	}

	return node.Config{
		Network:       node.Network{Listen: listen, MDNS: node.MDNS{Interfaces: interfaces}},
		Commissioning: secret,
		ProductDescription: node.ProductDescription{
			DeviceType: aggregator, VendorID: identity.VendorID, ProductID: identity.ProductID,
		},
		BasicInformation: node.BasicInformation{
			VendorName:   cut(identity.Vendor, nameBytes),
			ProductName:  cut(identity.Product, nameBytes),
			NodeLabel:    cut(identity.Name, nameBytes),
			SerialNumber: cut(identity.Serial, nameBytes),
		},
		Store: node.Store{Path: store},
	}
}
