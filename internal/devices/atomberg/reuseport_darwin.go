package atomberg

// soReusePort is SO_REUSEPORT's numeric value on macOS (0x0200), used for local
// development builds. See reuseport_linux.go for why this isn't
// syscall.SO_REUSEPORT.
const soReusePort = 0x0200
