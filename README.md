# go-matter

A Go API for Matter, the smart home protocol: serving a home's devices to Matter
controllers as a bridge, and commissioning and driving Matter devices as a controller.
Nothing is built yet. The API is declared, in the packages, with nothing behind it;
[the design](docs/design.md) says why it is shaped as it is and what is still open. The
implementation follows once it is agreed.

The aim is one small vocabulary for what a home's devices are and do, spoken the same way
on both sides of the protocol, over an implementation of Matter that stays out of sight.
