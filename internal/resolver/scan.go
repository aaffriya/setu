package resolver

import "context"

// Candidate is one device a brand scanner saw on the local network. It carries
// exactly what adding a device needs — the (brand, model) pair the factory
// registers, the MAC that is the device's identity, and whatever label the
// device reported — plus the IP it answered from, which is shown for
// orientation only and is never written to config (principle 6).
//
// Model is empty when the brand recognised the reply but has no driver for that
// hardware; callers surface such a candidate as "found, not supported" rather
// than guessing a model that would command the wrong thing.
type Candidate struct {
	Brand  string `json:"brand"`
	Model  string `json:"model"`
	Series string `json:"series,omitempty"` // device-reported product/module name
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
