package matter

// SetupCode is what a person gives a controller to commission a node:
// the same secret, written two ways.
type SetupCode struct {
	// Manual is the eleven digits to type.
	Manual string
	// QR is the text of the QR code to scan, starting "MT:".
	QR string
}
