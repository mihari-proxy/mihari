package sysproxy

// FakeBackend is an in-memory Backend for tests. It records call counts and last args.
type FakeBackend struct {
	State State

	GetErr     error
	EnableErr  error
	DisableErr error

	GetCalls       int
	EnableCalls    int
	DisableCalls   int
	LastEnableHost string
	LastEnablePort int
}

// Get returns the fake state or GetErr.
func (f *FakeBackend) Get() (State, error) {
	f.GetCalls++
	if f.GetErr != nil {
		return State{}, f.GetErr
	}
	return f.State, nil
}

// Enable records host/port, sets State to enabled, or returns EnableErr.
func (f *FakeBackend) Enable(host string, port int) error {
	f.EnableCalls++
	f.LastEnableHost = host
	f.LastEnablePort = port
	if f.EnableErr != nil {
		return f.EnableErr
	}
	f.State = State{
		Enabled: true,
		Server:  NormalizeServer(host, port),
	}
	return nil
}

// Disable records the call, clears Enabled, or returns DisableErr.
func (f *FakeBackend) Disable() error {
	f.DisableCalls++
	if f.DisableErr != nil {
		return f.DisableErr
	}
	f.State.Enabled = false
	return nil
}
