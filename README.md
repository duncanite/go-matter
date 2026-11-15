# go-matter

A Go API for Matter, the smart home protocol: serving a home's devices to Matter
controllers as a bridge, and commissioning and driving Matter devices as a controller.
The API is declared, in the packages, and being built behind it: the vocabulary and the
home that devices are written into work, and so does the bridge; the controller does not
yet.
[The design](docs/design.md) says why it is shaped as it is.

The aim is one small vocabulary for what a home's devices are and do, spoken the same way
on both sides of the protocol, over an implementation of Matter that stays out of sight.
