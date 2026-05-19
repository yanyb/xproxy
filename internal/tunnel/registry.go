package tunnel

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

type Registry struct {
	mu   sync.RWMutex
	sess map[string]*yamux.Session
}

func NewRegistry() *Registry {
	return &Registry{sess: make(map[string]*yamux.Session)}
}

func (r *Registry) Put(id string, s *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.sess[id]; ok && old != nil {
		_ = old.Close()
	}
	r.sess[id] = s
}

func (r *Registry) Remove(id string, s *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.sess[id]; ok && cur == s {
		delete(r.sess, id)
	}
}

func (r *Registry) CloseAll() {
	r.mu.RLock()
	list := make([]*yamux.Session, 0, len(r.sess))
	for _, s := range r.sess {
		list = append(list, s)
	}
	r.mu.RUnlock()
	for _, s := range list {
		_ = s.Close()
	}
}

func (r *Registry) ResolveDevice(socksUser string) (string, error) {
	id := strings.TrimSpace(socksUser)
	if id != "" {
		return id, nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
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

func (r *Registry) Wait(ctx context.Context, deviceID string) (*yamux.Session, error) {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return nil, fmt.Errorf("empty device id")
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		r.mu.RLock()
		s, ok := r.sess[deviceID]
		r.mu.RUnlock()
		if ok {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}
