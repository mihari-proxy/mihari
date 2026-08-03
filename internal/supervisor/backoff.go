package supervisor

import "time"

type Backoff struct {
	minimum time.Duration
	maximum time.Duration
	current time.Duration
}

func NewBackoff(minimum, maximum time.Duration) *Backoff {
	if minimum <= 0 {
		minimum = time.Second
	}
	if maximum < minimum {
		maximum = minimum
	}
	return &Backoff{minimum: minimum, maximum: maximum}
}

func (b *Backoff) Next() time.Duration {
	if b.current == 0 {
		b.current = b.minimum
		return b.current
	}
	next := b.current * 2
	if next < b.current || next > b.maximum {
		next = b.maximum
	}
	b.current = next
	return b.current
}

func (b *Backoff) Reset() { b.current = 0 }
