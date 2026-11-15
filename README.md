# go-matter

A Go API for Matter, the smart home protocol: serving a home's devices to Matter
controllers as a bridge, and commissioning and driving Matter devices as a controller.
This is the repository's baseline; the design follows, and the code after it.

The aim is one small vocabulary for what a home's devices are and do, spoken the same way
on both sides of the protocol, over an implementation of Matter that stays out of sight.
