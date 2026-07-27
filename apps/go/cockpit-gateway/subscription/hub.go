package subscription

import (
	"errors"
	"sync"
)

var (
	ErrInvalidConfig = errors.New("subscription hub configuration is invalid")
	ErrCapacity      = errors.New("subscription capacity reached")
)

type Config struct {
	MaxSubscriptions uint32
	BufferSize       uint32
}

type Signal struct {
	TenantID string
	Name     string
	Labels   map[string]string
	Payload  []byte
}

type Filter struct {
	TenantID string
	Name     string
	Labels   map[string]string
}

type Subscription struct {
	ID      uint64
	Signals <-chan Signal
	close   func()
	once    sync.Once
}

func (s *Subscription) Close() {
	s.once.Do(s.close)
}

type subscriber struct {
	filter Filter
	output chan Signal
}

type Hub struct {
	config Config
	mu     sync.RWMutex
	nextID uint64
	items  map[uint64]subscriber
}

func New(config Config) (*Hub, error) {
	if config.MaxSubscriptions == 0 || config.BufferSize == 0 {
		return nil, ErrInvalidConfig
	}
	return &Hub{config: config, items: make(map[uint64]subscriber)}, nil
}

func (h *Hub) Subscribe(filter Filter) (*Subscription, error) {
	if filter.TenantID == "" || filter.Name == "" {
		return nil, ErrInvalidConfig
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if uint32(len(h.items)) >= h.config.MaxSubscriptions {
		return nil, ErrCapacity
	}
	h.nextID++
	id := h.nextID
	output := make(chan Signal, h.config.BufferSize)
	h.items[id] = subscriber{filter: cloneFilter(filter), output: output}
	return &Subscription{
		ID:      id,
		Signals: output,
		close:   func() { h.remove(id) },
	}, nil
}

// Publish fans out only to exact tenant/name subscribers whose configured
// label predicates are all present on the signal. Slow clients are bounded.
func (h *Hub) Publish(signal Signal) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	delivered := 0
	for _, item := range h.items {
		if !matches(item.filter, signal) {
			continue
		}
		select {
		case item.output <- cloneSignal(signal):
			delivered++
		default:
		}
	}
	return delivered
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.items)
}

func (h *Hub) remove(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	item, exists := h.items[id]
	if !exists {
		return
	}
	delete(h.items, id)
	close(item.output)
}

func matches(filter Filter, signal Signal) bool {
	if filter.TenantID != signal.TenantID || filter.Name != signal.Name {
		return false
	}
	for name, expected := range filter.Labels {
		if signal.Labels[name] != expected {
			return false
		}
	}
	return true
}

func cloneFilter(filter Filter) Filter {
	return Filter{TenantID: filter.TenantID, Name: filter.Name, Labels: cloneLabels(filter.Labels)}
}

func cloneSignal(signal Signal) Signal {
	return Signal{
		TenantID: signal.TenantID,
		Name:     signal.Name,
		Labels:   cloneLabels(signal.Labels),
		Payload:  append([]byte(nil), signal.Payload...),
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels))
	for name, value := range labels {
		result[name] = value
	}
	return result
}
