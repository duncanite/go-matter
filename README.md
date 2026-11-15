# go-matter

A Go API for Matter, the smart home protocol: serving a home's devices to Matter
controllers as a bridge, and commissioning and driving Matter devices as a controller.
The vocabulary, the home that devices are written into, the bridge and the controller all
work, and are tested against each other in one process. [The design](docs/design.md) says
why the API is shaped as it is, and where its first implementation falls short of it.

The aim is one small vocabulary for what a home's devices are and do, spoken the same way
on both sides of the protocol, over an implementation of Matter that stays out of sight.
