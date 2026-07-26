//go:build !linux && !darwin

package atomberg

// soReusePort of 0 means "don't ask for it": listenShared skips the setsockopt
// call rather than guess a value for a platform Setu doesn't ship on.
const soReusePort = 0
