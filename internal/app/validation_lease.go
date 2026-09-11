package app

import "sync"

// ownedValidationLease makes cancellation and final cleanup share one close.
// The lifecycle owner returns the cached error after joining its pipe workers.
type ownedValidationLease struct {
	ValidationLease
	once sync.Once
	err  error
}

func (p *ownedValidationLease) Close() error {
	p.once.Do(func() { p.err = p.ValidationLease.Close() })
	return p.err
}
