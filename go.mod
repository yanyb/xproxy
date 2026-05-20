module xproxy

go 1.25.0

require (
	github.com/hashicorp/yamux v0.1.2
	github.com/things-go/go-socks5 v0.1.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	golang.org/x/mobile v0.0.0-20260519152538-35b1249819c3 // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/tools v0.45.0 // indirect
)

replace github.com/things-go/go-socks5 => ./third_party/go-socks5
