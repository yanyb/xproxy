module xproxy

go 1.25.0

require (
	github.com/hashicorp/yamux v0.1.2
	github.com/things-go/go-socks5 v0.1.1
	go.uber.org/zap v1.28.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/google/uuid v1.6.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/mobile v0.0.0-20260520154334-0e4426e1883d // indirect
	golang.org/x/mod v0.36.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/tools v0.45.0 // indirect
)

replace github.com/things-go/go-socks5 => ./third_party/go-socks5
