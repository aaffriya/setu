package resolver

import "context"

// Candidate is one device a brand scanner saw on the local network. It carries
// exactly what adding a device needs — the (brand, driver) pair the factory
// registers and the MAC that is the device's identity — plus what the device
// said about itself, and the IP it answered from, which is shown for
// orientation only and is never written to config (principle 6).
//
// Driver is empty when the brand recognised the reply but this build has no
// driver for that hardware; callers surface such a candidate as "found, not
// supported" rather than guessing a driver that would command the wrong thing.
//
// Model is what the device calls its own hardware, verbatim: a Samsung TV's
// "UE43AU7700", an Atomberg fan's "R1", a WiZ bulb's module name. A scanner
// never derives it, never prettifies it, and leaves it empty when the protocol
// says nothing — it is a label, and the driver decision is Driver's job.
type Candidate struct {
	Brand  string `json:"brand"`
	Driver string `json:"driver"`
	Model  string `json:"model,omitempty"`
	Name   string `json:"name,omitempty"`
	MAC    string `json:"mac"` // canonical colon form (FormatMAC)
	IP     string `json:"ip,omitempty"`
}

// Scanner enumerates the devices of one brand on the local segment. It is the
// discovery counterpart of Resolver at the same seam: Resolver answers "where
// is this MAC now?" for a device already in config, Scanner answers "what is
// out there?" for a device that is not in config yet. Brands implement both on
// the same type — the transport is identical, only the filter differs.
//
// A Scanner must respect ctx (it is the caller's overall budget), must never
// invent devices it did not hear from, and returns an empty slice rather than
// an error when the network is simply quiet.
type Scanner interface {
	Scan(ctx context.Context) ([]Candidate, error)
}
