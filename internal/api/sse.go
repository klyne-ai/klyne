// Package api — SSE broadcast hub.
//
// The Hub is the single fan-out point for all real-time events in the
// klyne daemon. Callers produce events via Publish; each active SSE
// connection calls Subscribe to receive them on a per-subscriber buffered
// channel.
//
// Design:
//   - Per-subscriber buffered channel (capacity subChanCap = 64).
//   - Non-blocking publish: if a subscriber's channel is full the event
//     is dropped and a warning is logged (no head-of-line blocking).
//   - Ring buffer of last ringSize = 256 events for Last-Event-ID replay.
//   - Hub.Close() drains all subscribers cleanly.
//
// Spec references:
//   - §5  (real-time choice: SSE)
//   - §7  (event names; see sse_events.go)
//   - §12 (<100ms p95 SSE latency)
package api

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// subChanCap is the per-subscriber channel capacity. 64 events provides
// enough headroom for bursts while keeping memory per connection bounded.
const subChanCap = 64

// ringSize is the number of events kept in the ring buffer for
// Last-Event-ID replay. Best-effort: events older than ringSize are gone.
const ringSize = 256

// defaultHeartbeatInterval is the interval at which the hub emits a
// sentinel heartbeat event. Handlers use this to send `: ping` SSE
// comments to keep the TCP connection alive through proxies.
const defaultHeartbeatInterval = 15 * time.Second

// Event is the interface every hub payload must implement. EventName()
// returns the SSE `event:` field value (one of the EventXxx constants in
// sse_events.go). Payload() returns the value to JSON-encode into the SSE
// `data:` line.
type Event interface {
	EventName() string
	// Payload returns the value to JSON-encode into the SSE data field.
	Payload() any
}

// ---------------------------------------------------------------------------
// Generic envelope
// ---------------------------------------------------------------------------

// envelope wraps any W0 payload type with its event name.
type envelope struct {
	name string
	data any
}

func (e envelope) EventName() string { return e.name }
func (e envelope) Payload() any      { return e.data }

// newEvent constructs an Event from a name constant and a typed payload.
func newEvent(name string, p any) Event {
	return envelope{name: name, data: p}
}

// heartbeatEvent is the sentinel emitted by the hub ticker. Handlers write
// a `: ping` SSE comment instead of a normal data frame.
type heartbeatEvent struct{}

func (heartbeatEvent) EventName() string { return ":heartbeat" }
func (heartbeatEvent) Payload() any      { return nil }

// HeartbeatSentinel is the singleton heartbeat Event. Handlers compare
// incoming events against this value to decide whether to emit a comment
// line rather than a data frame.
var HeartbeatSentinel Event = heartbeatEvent{}

// SeqEvent wraps an Event with the ring/sequence id the hub assigned to it
// in Publish. Subscribers receive SeqEvent values (both for live and
// replayed events) so the SSE handler can emit the SAME id it would replay
// against in Last-Event-ID — closing the gap where a per-frame counter
// diverged from the ring's h.seq key. Heartbeats are still delivered as the
// bare HeartbeatSentinel (no seq, no id: line).
//
// SeqEvent itself implements Event by delegating to the wrapped event, so
// existing consumers that call EventName()/Payload() keep working.
type SeqEvent struct {
	Seq   uint64
	Inner Event
}

func (e SeqEvent) EventName() string { return e.Inner.EventName() }
func (e SeqEvent) Payload() any      { return e.Inner.Payload() }

// ---------------------------------------------------------------------------
// Constructor helpers (one per W0 event type)
// ---------------------------------------------------------------------------

// MsgNewEvent wraps a MsgNew payload.
func MsgNewEvent(p MsgNew) Event { return newEvent(EventMsgNew, p) }

// SummaryReadyEvent wraps a SummaryReady payload.
func SummaryReadyEvent(p SummaryReady) Event { return newEvent(EventSummaryReady, p) }

// SessionUpdateEvent wraps a SessionUpdate payload.
func SessionUpdateEvent(p SessionUpdate) Event { return newEvent(EventSessionUpdate, p) }

// CostTickEvent wraps a CostTick payload.
func CostTickEvent(p CostTick) Event { return newEvent(EventCostTick, p) }

// ThreadRebuildEvent wraps a ThreadRebuild payload.
func ThreadRebuildEvent(p ThreadRebuild) Event { return newEvent(EventThreadRebuild, p) }

// CompactDetectedEvent wraps a CompactDetected payload.
func CompactDetectedEvent(p CompactDetected) Event { return newEvent(EventCompactDetected, p) }

// ---------------------------------------------------------------------------
// Ring buffer
// ---------------------------------------------------------------------------

// ringEntry holds a sequenced event for Last-Event-ID replay.
type ringEntry struct {
	id    uint64
	event Event
}

// ring is a fixed-size circular buffer of ringEntries.
type ring struct {
	mu      sync.Mutex
	buf     [ringSize]ringEntry
	head    int    // index of the next write slot
	count   int    // number of valid entries (up to ringSize)
	lastID  uint64 // highest id stored
}

// push appends an event to the ring, overwriting the oldest entry when
// the buffer is full. Returns the assigned sequence id.
func (rb *ring) push(id uint64, ev Event) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.buf[rb.head] = ringEntry{id: id, event: ev}
	rb.head = (rb.head + 1) % ringSize
	if rb.count < ringSize {
		rb.count++
	}
	rb.lastID = id
}

// since returns all entries with id > afterID in ascending order.
// If afterID is older than what the ring holds, it returns whatever
// is available (best-effort).
func (rb *ring) since(afterID uint64) []ringEntry {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if rb.count == 0 {
		return nil
	}

	// Walk from oldest to newest.
	results := make([]ringEntry, 0, rb.count)
	start := (rb.head - rb.count + ringSize*2) % ringSize
	for i := 0; i < rb.count; i++ {
		entry := rb.buf[(start+i)%ringSize]
		if entry.id > afterID {
			results = append(results, entry)
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// Hub
// ---------------------------------------------------------------------------

// subscriber is a single SSE connection's state.
type subscriber struct {
	ch     chan Event
	closed chan struct{} // closed when the subscriber is cancelled
}

// HubOption is a functional option for NewHub.
type HubOption func(*hubConfig)

type hubConfig struct {
	heartbeatInterval time.Duration
	logger            *slog.Logger
}

// WithHeartbeatInterval overrides the heartbeat interval. Useful in tests
// (e.g. WithHeartbeatInterval(50 * time.Millisecond)).
func WithHeartbeatInterval(d time.Duration) HubOption {
	return func(c *hubConfig) { c.heartbeatInterval = d }
}

// WithLogger sets a custom structured logger on the hub.
// If l is nil, slog.Default() is used.
func WithLogger(l *slog.Logger) HubOption {
	return func(c *hubConfig) {
		if l != nil {
			c.logger = l
		}
	}
}

// Hub is the SSE broadcast hub. Create one with NewHub and pass it to
// EventsMounter. The hub is safe for concurrent use.
type Hub struct {
	cfg hubConfig

	mu          sync.Mutex
	subs        map[uint64]*subscriber
	nextSubID   uint64

	seq    atomic.Uint64 // monotonic event sequence number
	rb     ring

	done   chan struct{} // closed by Close()
	closed atomic.Bool
}

// NewHub creates and starts a Hub. Call Close() when the daemon shuts down.
func NewHub(opts ...HubOption) *Hub {
	cfg := hubConfig{
		heartbeatInterval: defaultHeartbeatInterval,
		logger:            slog.Default(),
	}
	for _, o := range opts {
		o(&cfg)
	}
	h := &Hub{
		cfg:  cfg,
		subs: make(map[uint64]*subscriber),
		done: make(chan struct{}),
	}
	go h.heartbeatLoop()
	return h
}

// heartbeatLoop ticks at cfg.heartbeatInterval and broadcasts a heartbeat
// sentinel so each handler can emit an SSE comment to keep the connection live.
func (h *Hub) heartbeatLoop() {
	ticker := time.NewTicker(h.cfg.heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			h.broadcast(HeartbeatSentinel, false /* skip ring */)
		case <-h.done:
			return
		}
	}
}

// Publish sends ev to every active subscriber. The call is non-blocking:
// if a subscriber's channel is full the event is dropped and a warning is
// logged. ev must not be the HeartbeatSentinel.
func (h *Hub) Publish(ev Event) {
	id := h.seq.Add(1)
	h.rb.push(id, ev)
	// Deliver the assigned seq alongside the event so the SSE handler emits
	// it as the `id:` field — the exact value a later Last-Event-ID replay
	// keys the ring on.
	h.broadcast(SeqEvent{Seq: id, Inner: ev}, false)
}

// broadcast sends ev to all subscribers. If addToRing is true the event is
// first appended to the ring buffer. heartbeat sentinels bypass the ring.
func (h *Hub) broadcast(ev Event, _ bool) {
	h.mu.Lock()
	subs := make([]*subscriber, 0, len(h.subs))
	for _, s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()

	for _, s := range subs {
		select {
		case s.ch <- ev:
		default:
			h.cfg.logger.Warn("sse hub: subscriber slow, dropping event",
				slog.String("event", ev.EventName()))
		}
	}
}

// subscribe is the internal registration helper. It returns the writable
// channel (so callers like SubscribeWithReplay can pre-fill it with missed
// events), along with a cancel function. The exported Subscribe and
// SubscribeWithReplay methods narrow the channel to receive-only.
func (h *Hub) subscribe() (chan Event, func()) {
	h.mu.Lock()
	id := h.nextSubID
	h.nextSubID++
	s := &subscriber{
		ch:     make(chan Event, subChanCap),
		closed: make(chan struct{}),
	}
	h.subs[id] = s
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		_, ok := h.subs[id]
		if ok {
			delete(h.subs, id)
		}
		h.mu.Unlock()
		if ok {
			close(s.ch)
			close(s.closed)
		}
	}
	return s.ch, cancel
}

// Subscribe registers a new subscriber and returns:
//   - a receive-only channel of Events (buffered, capacity subChanCap).
//   - a cancel function: calling it unregisters the subscriber and closes
//     the channel so the handler's range loop terminates.
//
// The caller MUST call cancel (e.g. via defer) to avoid a goroutine/memory
// leak. The returned channel is closed when cancel is called.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	ch, cancel := h.subscribe()
	return ch, cancel
}

// SubscribeWithReplay is like Subscribe but also replays any buffered
// events whose sequence id is > afterID. The replay events are enqueued
// before live delivery begins. If the ring has rolled past afterID, only
// the available suffix is replayed (best-effort).
func (h *Hub) SubscribeWithReplay(afterID uint64) (<-chan Event, func()) {
	ch, cancel := h.subscribe()

	// Replay buffered events. Write to the writable side of the channel
	// synchronously; drop if the buffer would overflow.
	missed := h.rb.since(afterID)
	for _, entry := range missed {
		select {
		case ch <- SeqEvent{Seq: entry.id, Inner: entry.event}:
		default:
			h.cfg.logger.Warn("sse hub: replay dropped event",
				slog.Uint64("id", entry.id))
		}
	}
	return ch, cancel
}

// CurrentSeq returns the most recently assigned sequence number.
// Handlers use this to write `id:` fields on SSE frames.
func (h *Hub) CurrentSeq() uint64 {
	return h.seq.Load()
}


// Close shuts down the hub: stops the heartbeat goroutine and closes all
// active subscriber channels so handlers can drain and exit.
func (h *Hub) Close() {
	if !h.closed.CompareAndSwap(false, true) {
		return
	}
	close(h.done)

	h.mu.Lock()
	subs := h.subs
	h.subs = make(map[uint64]*subscriber)
	h.mu.Unlock()

	for _, s := range subs {
		close(s.ch)
		close(s.closed)
	}
}
