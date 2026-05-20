package tunnel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/hashicorp/yamux"
)

type Registry struct {
	mu      sync.Mutex
	sess    map[string]*yamux.Session
	waiters map[string][]chan struct{}
}

func NewRegistry() *Registry {
	return &Registry{
		sess:    make(map[string]*yamux.Session),
		waiters: make(map[string][]chan struct{}),
	}
}

func (r *Registry) Put(id string, s *yamux.Session) {
	r.mu.Lock()
	old, hadOld := r.sess[id]
	r.sess[id] = s
	waiters := r.waiters[id]
	delete(r.waiters, id)
	r.mu.Unlock()

	if hadOld && old != nil {
		_ = old.Close()
	}
	for _, ch := range waiters {
		close(ch)
	}
}

func (r *Registry) Remove(id string, s *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.sess[id]; ok && cur == s {
		delete(r.sess, id)
	}
}

func (r *Registry) CloseAll() {
	r.mu.Lock()
	list := make([]*yamux.Session, 0, len(r.sess))
	for _, s := range r.sess {
		list = append(list, s)
	}
	r.mu.Unlock()
	for _, s := range list {
		_ = s.Close()
	}
}

func (r *Registry) ResolveDevice(socksUser string) (string, error) {
	id := strings.TrimSpace(socksUser)
	if id != "" {
		return id, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sess) == 0 {
		return "", fmt.Errorf("no device online")
	}
	if len(r.sess) == 1 {
		for k := range r.sess {
			return k, nil
		}
	}
	ids := make([]string, 0, len(r.sess))
	for k := range r.sess {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	return "", fmt.Errorf("multiple devices online %v: set socks username to device_id", ids)
}

// Wait blocks until a session for deviceID becomes available or ctx is canceled.
// It uses per-id waiter channels (no polling), so newly-online devices are
// observed immediately and idle requests cost no CPU.
func (r *Registry) Wait(ctx context.Context, deviceID string) (*yamux.Session, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, fmt.Errorf("empty device id")
	}
	for {
		r.mu.Lock()
		if s, ok := r.sess[deviceID]; ok {
			r.mu.Unlock()
			return s, nil
		}
		ch := make(chan struct{})
		r.waiters[deviceID] = append(r.waiters[deviceID], ch)
		r.mu.Unlock()

		select {
		case <-ch:
			// Re-check: device arrived (or registry was rotated). Loop.
		case <-ctx.Done():
			r.removeWaiter(deviceID, ch)
			return nil, ctx.Err()
		}
	}
}

func (r *Registry) removeWaiter(id string, ch chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	list := r.waiters[id]
	for i, c := range list {
		if c == ch {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(r.waiters, id)
	} else {
		r.waiters[id] = list
	}
}
