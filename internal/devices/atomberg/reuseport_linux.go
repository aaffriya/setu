package atomberg

// soReusePort is SO_REUSEPORT's numeric value on Linux — 15 (0xf) on every
// architecture Setu ships for (amd64, arm64, arm/v7, arm/v6, 386). The stdlib
// syscall package only exports the named constant on some of those (notably
// not 32-bit ARM), so the raw value is used here instead of syscall.SO_REUSEPORT
// to keep the cross-compiled matrix building.
const soReusePort = 0xf
