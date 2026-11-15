package matter

import "errors"

// The errors a command returns, to test with errors.Is. They cross the
// protocol: an error a device's command returns behind a bridge is the
// error the controller's caller gets. Any other error crosses as
// ErrFailed.
var (
	// ErrUnreachable: the device did not answer.
	ErrUnreachable = errors.New("the device cannot be reached")
	// ErrInvalid: the device cannot do what was asked, as asked: a level
	// out of range, a transition it cannot keep.
	ErrInvalid = errors.New("the request is not one the device can honour")
	// ErrBusy: the device cannot do it now, and may later.
	ErrBusy = errors.New("the device is busy")
	// ErrFailed: the device tried and failed, or said no more than that.
	ErrFailed = errors.New("the device failed")
)

// The errors that do not cross the protocol: they are about what is
// held here.
var (
	// ErrDescription: a device described in a way that cannot be served,
	// or told a change it cannot have.
	ErrDescription = errors.New("the device's description cannot be served")
	// ErrNoDevice: no device by that ID is held.
	ErrNoDevice = errors.New("no such device")
	// ErrClosed: what was asked is over for good: a bridge or a
	// controller closed, a node unpaired.
	ErrClosed = errors.New("closed")
)
