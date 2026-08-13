package tundetect

import "context"

// FakeBackend is an in-memory Backend for tests. It returns a canned Detection
// (or Err) and records how many times Detect was called.
type FakeBackend struct {
	Detection Detection
	Err       error

	DetectCalls int
}

// Detect returns the fake detection or Err.
func (f *FakeBackend) Detect(ctx context.Context) (Detection, error) {
	f.DetectCalls++
	if f.Err != nil {
		return Detection{}, f.Err
	}
	return f.Detection, nil
}
