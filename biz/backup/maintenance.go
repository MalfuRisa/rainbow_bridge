package backup

import (
	"context"
	"sync"
)

// Maintenance coordinates business writes with backup copying. New writes are
// rejected as soon as maintenance becomes active; already registered writes
// are allowed to finish. It is deliberately process-local: the first version
// only supports a single writing process.
type Maintenance struct {
	mu      sync.Mutex
	cond    *sync.Cond
	active  bool
	writers int
}

func NewMaintenance() *Maintenance {
	gate := &Maintenance{}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

// BeginWrite registers an in-flight business write. The returned release
// function must be called exactly once.
func (m *Maintenance) BeginWrite() (func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active {
		return nil, MaintenanceError()
	}
	m.writers++
	released := false
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if released {
			return
		}
		released = true
		m.writers--
		if m.writers == 0 {
			m.cond.Broadcast()
		}
	}, nil
}

// Enter marks maintenance active and waits for registered writes to drain.
// On timeout it clears the flag again so the caller can fail without leaving
// every subsequent write blocked.
func (m *Maintenance) Enter(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active {
		return busyError("another maintenance task is already running")
	}
	m.active = true
	if m.writers == 0 {
		return nil
	}

	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			m.cond.Broadcast()
		case <-finished:
		}
	}()
	for m.writers > 0 && ctx.Err() == nil {
		m.cond.Wait()
	}
	close(finished)
	if ctx.Err() != nil {
		m.active = false
		m.cond.Broadcast()
		return &Error{HTTPStatus: 503, Type: ErrMaintenance,
			Message: "timed out waiting for in-flight business writes", RetryAfter: 5}
	}
	return nil
}

// Leave releases maintenance after a failure or a cancelled restore plan.
func (m *Maintenance) Leave() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active {
		return
	}
	m.active = false
	m.cond.Broadcast()
}

func (m *Maintenance) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// InFlight returns the number of registered business writes (diagnostics).
func (m *Maintenance) InFlight() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writers
}
