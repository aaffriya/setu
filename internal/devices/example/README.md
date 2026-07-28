# `internal/devices/example`

Compiling device-driver blueprint and no-hardware UI/API stand-in.

It demonstrates:

- an embedded brand `base`;
- cached MAC-to-IP resolution with ARP then brand discovery;
- invalidation after transport failure;
- immediate command events versus quiet poll updates;
- a driver type implementing only selected capabilities;
- compile-time interface assertions;
- `New`, `Register`, `Poll`, `Resolver`, and `Scanner` shapes.

The transport is a stub and discovery resolves to loopback, so the registered
`example/bulb` can exercise UI and API behavior without hardware. It is not a
real protocol implementation.

Copy the package, replace the stubs, remove unsupported capabilities, and follow
the checklist at the bottom of `example.go`.
