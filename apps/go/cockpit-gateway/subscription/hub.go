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
	ReplaySize       uint32
	MaxReplayStreams uint32
}

type Signal struct {
	Cursor   string
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
	ID          uint64
	Signals     <-chan Signal
	ResumeReset bool
	close       func()
	once        sync.Once
}

func (s *Subscription) Close() {
	s.once.Do(s.close)
}

type subscriber struct {
	filter Filter
	output chan Signal
}

type bucketKey struct {
	tenantID string
	name     string
}

type bucket struct {
	subscribers map[uint64]subscriber
	replay      []Signal
	replayStart int
}

type PublishStats struct {
	Delivered    int
	Disconnected int
}

type Hub struct {
	config Config
	mu     sync.RWMutex
	nextID uint64
	count  uint32
	items  map[bucketKey]*bucket
	byID   map[uint64]bucketKey
}

func New(config Config) (*Hub, error) {
	if config.MaxSubscriptions == 0 || config.BufferSize == 0 ||
		config.ReplaySize == 0 ||
		config.MaxReplayStreams < config.MaxSubscriptions {
		return nil, ErrInvalidConfig
	}
	return &Hub{
		config: config,
		items:  make(map[bucketKey]*bucket),
		byID:   make(map[uint64]bucketKey),
	}, nil
}

func (h *Hub) Subscribe(filter Filter) (*Subscription, error) {
	return h.SubscribeFrom(filter, "")
}

func (h *Hub) SubscribeFrom(filter Filter, afterCursor string) (*Subscription, error) {
	if filter.TenantID == "" || filter.Name == "" {
		return nil, ErrInvalidConfig
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.count >= h.config.MaxSubscriptions {
		return nil, ErrCapacity
	}
	key := bucketKey{tenantID: filter.TenantID, name: filter.Name}
	group := h.items[key]
	if group == nil {
		h.evictInactiveReplayStream()
		if uint32(len(h.items)) >= h.config.MaxReplayStreams {
			return nil, ErrCapacity
		}
		group = &bucket{subscribers: make(map[uint64]subscriber)}
		h.items[key] = group
	}
	replay, reset := replayAfter(group, filter, afterCursor)
	if len(replay) > int(h.config.BufferSize) {
		replay = nil
		reset = true
	}
	h.nextID++
	id := h.nextID
	output := make(chan Signal, h.config.BufferSize)
	for _, signal := range replay {
		output <- signal
	}
	group.subscribers[id] = subscriber{filter: cloneFilter(filter), output: output}
	h.byID[id] = key
	h.count++
	return &Subscription{
		ID: id, Signals: output, ResumeReset: reset,
		close: func() { h.remove(id) },
	}, nil
}

// PublishToIndexedSubscribers is O(subscribers for tenant/name), not O(all
// active connections). A full outbound queue disconnects that slow client.
func (h *Hub) PublishToIndexedSubscribers(signal Signal) PublishStats {
	if signal.TenantID == "" || signal.Name == "" {
		return PublishStats{}
	}
	key := bucketKey{tenantID: signal.TenantID, name: signal.Name}
	h.mu.Lock()
	defer h.mu.Unlock()
	group := h.items[key]
	if group == nil {
		h.evictInactiveReplayStream()
		if uint32(len(h.items)) >= h.config.MaxReplayStreams {
			return PublishStats{}
		}
		group = &bucket{subscribers: make(map[uint64]subscriber)}
		h.items[key] = group
	}
	h.appendReplay(group, signal)
	stats := PublishStats{}
	for id, item := range group.subscribers {
		if !labelsMatch(item.filter.Labels, signal.Labels) {
			continue
		}
		select {
		case item.output <- cloneSignal(signal):
			stats.Delivered++
		default:
			close(item.output)
			delete(group.subscribers, id)
			delete(h.byID, id)
			h.count--
			stats.Disconnected++
		}
	}
	return stats
}

func (h *Hub) evictInactiveReplayStream() {
	if uint32(len(h.items)) < h.config.MaxReplayStreams {
		return
	}
	for key, group := range h.items {
		if len(group.subscribers) == 0 {
			delete(h.items, key)
			return
		}
	}
}

func (h *Hub) Publish(signal Signal) int {
	return h.PublishToIndexedSubscribers(signal).Delivered
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return int(h.count)
}

func (h *Hub) remove(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key, exists := h.byID[id]
	if !exists {
		return
	}
	group := h.items[key]
	item, exists := group.subscribers[id]
	if !exists {
		return
	}
	delete(group.subscribers, id)
	delete(h.byID, id)
	h.count--
	close(item.output)
	if len(group.subscribers) == 0 && len(group.replay) == 0 {
		delete(h.items, key)
	}
}

func (h *Hub) appendReplay(group *bucket, signal Signal) {
	if signal.Cursor == "" {
		return
	}
	value := cloneSignal(signal)
	if len(group.replay) < int(h.config.ReplaySize) {
		group.replay = append(group.replay, value)
		return
	}
	group.replay[group.replayStart] = value
	group.replayStart = (group.replayStart + 1) % len(group.replay)
}

func replayAfter(group *bucket, filter Filter, cursor string) ([]Signal, bool) {
	if cursor == "" {
		return nil, false
	}
	ordered := orderedReplay(group)
	found := false
	result := make([]Signal, 0, len(ordered))
	for _, signal := range ordered {
		if found && labelsMatch(filter.Labels, signal.Labels) {
			result = append(result, cloneSignal(signal))
		}
		if signal.Cursor == cursor {
			found = true
		}
	}
	return result, !found
}

func orderedReplay(group *bucket) []Signal {
	if len(group.replay) == 0 || group.replayStart == 0 {
		return group.replay
	}
	result := make([]Signal, 0, len(group.replay))
	result = append(result, group.replay[group.replayStart:]...)
	result = append(result, group.replay[:group.replayStart]...)
	return result
}

func labelsMatch(expected, actual map[string]string) bool {
	for name, value := range expected {
		if actual[name] != value {
			return false
		}
	}
	return true
}

func cloneFilter(filter Filter) Filter {
	return Filter{
		TenantID: filter.TenantID, Name: filter.Name,
		Labels: cloneLabels(filter.Labels),
	}
}

func cloneSignal(signal Signal) Signal {
	return Signal{
		Cursor: signal.Cursor, TenantID: signal.TenantID, Name: signal.Name,
		Labels:  cloneLabels(signal.Labels),
		Payload: append([]byte(nil), signal.Payload...),
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels))
	for name, value := range labels {
		result[name] = value
	}
	return result
}
