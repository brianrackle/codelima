module github.com/brianrackle/codelima

go 1.24.1

// Exact upstream v0.17.1 plus the bounded static Kitty presentation API.
replace go.rockorager.dev/vaxis => ./third_party/vaxis

require (
	github.com/creack/pty v1.1.18
	github.com/google/uuid v1.6.0
	go.rockorager.dev/vaxis v0.17.1
	golang.org/x/crypto v0.48.0
	golang.org/x/sys v0.41.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/rockorager/go-uucode v1.2.0 // indirect
	golang.org/x/term v0.40.0 // indirect
)
