package transport

import "sync"

type dialScopeKey struct{}
type dialScope struct {
	mu      sync.Mutex
	active  int
	closing bool
	done    chan struct{}
}

func (s *dialScope) enter() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.active++
	return true
}
func (s *dialScope) leave() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	if s.closing && s.active == 0 {
		close(s.done)
	}
}
func (s *dialScope) closeAndWait() {
	s.mu.Lock()
	if !s.closing {
		s.closing = true
		if s.active == 0 {
			close(s.done)
		}
	}
	s.mu.Unlock()
	<-s.done
}
