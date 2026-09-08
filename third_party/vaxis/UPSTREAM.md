# Vaxis source provenance

This is the complete Go module `go.rockorager.dev/vaxis` at upstream tag
`v0.17.1`, copied from the verified Go module cache without modifying that cache.

- Upstream: https://github.com/rockorager/vaxis
- Tag revision: `1dea9475d7d75ec2c33a13777dcc96334b608b07`
- Module checksum: `h1:GE71riV9xp2rFDNqUDHU8JyoSOYHaC5G6zKte17oWbA=`
- go.mod checksum: `h1:5VpvRiT7INHD7fpN3G/z6c7A/tGwcIMaSyHdUwphKbI=`
- License: Apache-2.0; the original `LICENSE` and source notices are preserved.

The CodeLima delta is limited to an additive `EncodedKittyImage` API and its
tests, plus image-ID/lifecycle and z-order placement integration in `image.go`
and the in-flight upload owner/graphics-diff guard in `vaxis.go`. Existing image APIs retain their
behavior. The new API owns caller-provided PNG bytes, validates size/geometry,
uploads synchronously within a wire-byte budget using the existing terminal
write lock, serializes multipart transfers, and places exact pixels without
background encoding/resizing goroutines. Text rendering continues during a
multipart transfer while graphics diffs wait; explicit image deletion resets
any unfinished transfer to its first chunk. Continuations contain only m/q,
as required by the [Kitty protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/#remote-client).
It does not add native dependencies.

Run `make test-vaxis-fork` from the CodeLima root, including all retained
upstream module tests. When upgrading, copy the verified upstream module and
reapply this documented delta; do not edit cached modules. Remove the local
replacement once an upstream release provides these bounded public contracts.
