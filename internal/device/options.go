package device

type Options struct {
	Lookup  HostLookup
	NetType func() string
}
