package gcsproxy

import (
	"encoding/json"
	"sync"
)

const MaxUint64 = ^uint64(0)

type Stats struct {
	Size       uint64
	Hit        uint64
	hitMutex   sync.Mutex
	Miss       uint64
	missMutex  sync.Mutex
	Error      uint64
	errorMutex sync.Mutex

	Skip      uint64
	skipMutex sync.Mutex

	Bypass      uint64
	bypassMutex sync.Mutex
}

func NewStats() *Stats {
	return &Stats{}
}

func (s *Stats) reset() {
	s.hitMutex.Lock()
	s.Hit = 0
	s.hitMutex.Unlock()
	s.missMutex.Lock()
	s.Miss = 0
	s.missMutex.Unlock()
	s.skipMutex.Lock()
	s.Skip = 0
	s.errorMutex.Unlock()
	s.bypassMutex.Lock()
	s.Bypass = 0
	s.bypassMutex.Unlock()
	s.errorMutex.Lock()
	s.Error = 0
	s.errorMutex.Unlock()
}
func (s *Stats) Inc(status string) {

	switch status {
	case "hit":
		if s.Hit == MaxUint64 {
			s.reset()
		}
		s.hitMutex.Lock()
		s.Hit += 1
		s.hitMutex.Unlock()
	case "miss":
		if s.Miss == MaxUint64 {
			s.reset()
		}
		s.missMutex.Lock()
		s.Miss += 1
		s.missMutex.Unlock()
	case "skip":
		if s.Skip == MaxUint64 {
			s.reset()
		}
		s.skipMutex.Lock()
		s.Skip += 1
		s.errorMutex.Unlock()
	case "bypass":
		if s.Bypass == MaxUint64 {
			s.reset()
		}
		s.bypassMutex.Lock()
		s.Bypass += 1
		s.bypassMutex.Unlock()
	case "error":
		if s.Error == MaxUint64 {
			s.reset()
		}
		s.errorMutex.Lock()
		s.Error += 1
		s.errorMutex.Unlock()
	}
}

func (s *Stats) String() string {
	b, err := json.Marshal(*s)
	if err != nil {
		return ""
	}
	return string(b)
}
