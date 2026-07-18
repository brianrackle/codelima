package codelima

// Version is replaced at build time with -ldflags -X. Development builds keep
// the explicit value so daemon handshakes are deterministic.
var Version = "0.0.0-dev"
