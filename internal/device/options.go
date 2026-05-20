package device

type Options struct {
	Lookup  HostLookup
	NetType func() string

	// OnNetworkChange returns a channel that, when closed by the caller, signals
	// the current session to terminate so RunWith reconnects immediately.
	// Each session asks for a fresh channel (the caller must produce a new one
	// after closing the previous). If nil, no external reset is wired.
	OnNetworkChange func() <-chan struct{}
}
