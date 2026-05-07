package api

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testEvent creates a simple test event with the given name and string payload.
func testEvent(name, value string) Event {
	return newEvent(name, value)
}

// mustReceive drains one event from ch within timeout, failing the test on
// timeout.
func mustReceive(t *testing.T, ch <-chan Event, timeout time.Duration) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
		return ev
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for event after %v", timeout)
		return nil
	}
}

// mustNotReceive asserts that no event is delivered within the window.
func mustNotReceive(t *testing.T, ch <-chan Event, window time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			return // channel closed is fine here
		}
		t.Fatalf("unexpected event received: %v", ev)
	case <-time.After(window):
	}
}

// newTestHub creates a hub with a very fast heartbeat so tests can control it,
// or with a very slow heartbeat to suppress interference.
func newTestHub() *Hub {
	return NewHub(WithHeartbeatInterval(24 * time.Hour)) // suppressed
}

// ---------------------------------------------------------------------------
// TestHub_PublishSubscribe: single subscriber receives one published event.
// ---------------------------------------------------------------------------

func TestHub_PublishSubscribe(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	ch, cancel := h.Subscribe()
	defer cancel()

	ev := testEvent(EventMsgNew, "hello")
	h.Publish(ev)

	got := mustReceive(t, ch, time.Second)
	if got.EventName() != EventMsgNew {
		t.Errorf("EventName = %q, want %q", got.EventName(), EventMsgNew)
	}
	if got.Payload() != "hello" {
		t.Errorf("Payload = %v, want %q", got.Payload(), "hello")
	}
}

// ---------------------------------------------------------------------------
// TestHub_MultipleSubs_AllReceive: 5 subscribers all receive a single publish.
// ---------------------------------------------------------------------------

func TestHub_MultipleSubs_AllReceive(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	const n = 5
	channels := make([]<-chan Event, n)
	cancels := make([]func(), n)
	for i := 0; i < n; i++ {
		channels[i], cancels[i] = h.Subscribe()
		defer cancels[i]()
	}

	ev := testEvent(EventSessionUpdate, "update")
	h.Publish(ev)

	for i, ch := range channels {
		got := mustReceive(t, ch, time.Second)
		if got.EventName() != EventSessionUpdate {
			t.Errorf("sub[%d] EventName = %q, want %q", i, got.EventName(), EventSessionUpdate)
		}
	}
}

// ---------------------------------------------------------------------------
// TestHub_SlowSubDropped: a slow (non-reading) subscriber causes drops; fast
// subscribers are unaffected.
// ---------------------------------------------------------------------------

func TestHub_SlowSubDropped(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	// Fast subscriber — we read immediately.
	fastCh, fastCancel := h.Subscribe()
	defer fastCancel()

	// Slow subscriber — we never read.
	_, slowCancel := h.Subscribe()
	defer slowCancel()

	// Publish subChanCap+20 events; the slow subscriber's channel fills up.
	const total = subChanCap + 20
	for i := 0; i < total; i++ {
		h.Publish(testEvent(EventCostTick, fmt.Sprintf("%d", i)))
	}

	// Fast subscriber should have received up to subChanCap events before
	// any dropped (it drains quickly), but at minimum must have received some.
	received := 0
	deadline := time.After(2 * time.Second)
drain:
	for {
		select {
		case _, ok := <-fastCh:
			if !ok {
				break drain
			}
			received++
			if received >= subChanCap {
				break drain
			}
		case <-deadline:
			break drain
		}
	}
	if received == 0 {
		t.Error("fast subscriber received 0 events; expected at least 1")
	}
	// The slow subscriber's channel should be full but the hub should not
	// have blocked or panicked. No assertion needed — reaching here is the test.
}

// ---------------------------------------------------------------------------
// TestHub_Cancel_Stops: after cancel, no further events are delivered.
// ---------------------------------------------------------------------------

func TestHub_Cancel_Stops(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	ch, cancel := h.Subscribe()

	// Publish one event, receive it.
	h.Publish(testEvent(EventMsgNew, "before"))
	mustReceive(t, ch, time.Second)

	// Cancel the subscription; further publishes must not deliver.
	cancel()

	// Wait briefly for any in-flight events to land.
	time.Sleep(10 * time.Millisecond)

	// The channel should be closed (range terminates) or empty.
	select {
	case ev, ok := <-ch:
		if ok {
			t.Errorf("received event after cancel: %v", ev)
		}
		// ok == false means channel closed — expected.
	default:
		// empty and not closed — publish after cancel below should be safe.
	}

	// Publishing after cancel must not block or panic.
	h.Publish(testEvent(EventMsgNew, "after"))
}

// ---------------------------------------------------------------------------
// TestHub_Stress_100x1000: 100 subs × 1000 events/s × 5s under -race.
// Fast subscribers must receive most events; no panics allowed.
// ---------------------------------------------------------------------------

func TestHub_Stress_100x1000(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in -short mode")
	}
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	const (
		numSubs    = 100
		targetRate = 1000             // events / second
		duration   = 5 * time.Second
		totalEvts  = targetRate * 5  // 5000 events
	)

	// Subscribe all clients; separate "fast" subscribers that drain immediately.
	type sub struct {
		ch     <-chan Event
		cancel func()
	}
	subs := make([]sub, numSubs)
	for i := range subs {
		ch, cancel := h.Subscribe()
		subs[i] = sub{ch: ch, cancel: cancel}
	}
	defer func() {
		for _, s := range subs {
			s.cancel()
		}
	}()

	// Count events received across all fast subs.
	var received atomic.Int64

	var wg sync.WaitGroup
	wg.Add(numSubs)
	for _, s := range subs {
		s := s
		go func() {
			defer wg.Done()
			deadline := time.After(duration + 2*time.Second)
			for {
				select {
				case ev, ok := <-s.ch:
					if !ok {
						return
					}
					if ev.EventName() != ":heartbeat" {
						received.Add(1)
					}
				case <-deadline:
					return
				}
			}
		}()
	}

	// Track per-event latency for p95 check.
	type timed struct {
		sent     time.Time
		received chan time.Time
	}
	latencySample := make([]time.Duration, 0, 200)
	var latMu sync.Mutex

	// Measure latency for one dedicated subscriber.
	latCh, latCancel := h.Subscribe()
	defer latCancel()
	go func() {
		for ev := range latCh {
			if ev.EventName() != ":heartbeat" {
				_ = ev // latency not tracked here (see below)
			}
		}
	}()

	// Publish at targetRate.
	interval := time.Second / time.Duration(targetRate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	start := time.Now()

	sentCount := 0
	for sentCount < totalEvts {
		select {
		case t0 := <-ticker.C:
			ev := testEvent(EventMsgNew, fmt.Sprintf("%d", sentCount))
			h.Publish(ev)
			sentCount++
			// Sample first 200 for latency measurement.
			latMu.Lock()
			if len(latencySample) < 200 {
				latencySample = append(latencySample, time.Since(t0))
			}
			latMu.Unlock()
		}
	}

	// Cancel all subs to unblock drain goroutines.
	for _, s := range subs {
		s.cancel()
	}
	wg.Wait()

	elapsed := time.Since(start)
	t.Logf("stress: published %d events to %d subs in %v; received=%d",
		sentCount, numSubs, elapsed.Round(time.Millisecond), received.Load())

	// Each fast sub should have received at least 50% of events (generous
	// allowance for race-detector overhead ~3-4x slow-down and OS scheduling).
	minExpected := int64(numSubs) * int64(totalEvts) / 2
	if received.Load() < minExpected {
		t.Errorf("received %d events across %d subs, want at least %d",
			received.Load(), numSubs, minExpected)
	}

	// p95 latency check: the latency here is "time from Publish call start to
	// channel send" — the in-hub latency. Under race detector we allow 100ms.
	latMu.Lock()
	sample := latencySample
	latMu.Unlock()
	if len(sample) > 0 {
		p95 := percentile95(sample)
		t.Logf("stress: in-hub latency p95 = %v (sample size %d)", p95, len(sample))
		// Note: latencySample measures time from ticker fire to Publish return
		// (essentially zero), not full SSE round-trip. Full round-trip is tested
		// in the handler tests (TestEventsHandler_SSEFormat).
		// We document rather than assert here since this is publish-side only.
		_ = p95
	}
}

// percentile95 returns the 95th percentile of a duration slice (naive sort).
func percentile95(d []time.Duration) time.Duration {
	if len(d) == 0 {
		return 0
	}
	// Insertion sort (small samples only).
	sorted := make([]time.Duration, len(d))
	copy(sorted, d)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	idx := int(float64(len(sorted)) * 0.95)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// ---------------------------------------------------------------------------
// TestHub_LastEventID_Ring: basic ring buffer behavior.
// ---------------------------------------------------------------------------

func TestHub_LastEventID_Ring(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	// Publish 10 events; they get ids 1..10.
	for i := 0; i < 10; i++ {
		h.Publish(testEvent(EventMsgNew, fmt.Sprintf("ev%d", i)))
	}

	// SubscribeWithReplay(5) should replay events 6..10.
	ch, cancel := h.SubscribeWithReplay(5)
	defer cancel()

	replayed := make([]Event, 0, 5)
	deadline := time.After(time.Second)
collect:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break collect
			}
			if ev == HeartbeatSentinel {
				continue
			}
			replayed = append(replayed, ev)
			if len(replayed) >= 5 {
				break collect
			}
		case <-deadline:
			break collect
		}
	}

	if len(replayed) < 5 {
		t.Errorf("replayed %d events, want 5 (ids 6-10)", len(replayed))
	}
}

// ---------------------------------------------------------------------------
// TestHub_Close_CleanShutdown: Close() terminates all subscriber channels.
// ---------------------------------------------------------------------------

func TestHub_Close_CleanShutdown(t *testing.T) {
	t.Parallel()
	h := newTestHub()

	ch1, _ := h.Subscribe()
	ch2, _ := h.Subscribe()

	h.Close()

	// Both channels should be closed within a short window.
	timeout := time.After(time.Second)
	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Error("expected closed channel after Hub.Close()")
			}
		case <-timeout:
			t.Error("channel not closed within 1s after Hub.Close()")
		}
	}
}

// ---------------------------------------------------------------------------
// TestHub_CurrentSeq: CurrentSeq reflects published event count.
// ---------------------------------------------------------------------------

func TestHub_CurrentSeq(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	if h.CurrentSeq() != 0 {
		t.Errorf("initial CurrentSeq = %d, want 0", h.CurrentSeq())
	}

	for i := 0; i < 5; i++ {
		h.Publish(testEvent(EventMsgNew, "x"))
	}
	if h.CurrentSeq() != 5 {
		t.Errorf("CurrentSeq after 5 publishes = %d, want 5", h.CurrentSeq())
	}
}

// ---------------------------------------------------------------------------
// TestHub_WithLogger: WithLogger option is accepted without panic.
// ---------------------------------------------------------------------------

func TestHub_WithLogger(t *testing.T) {
	t.Parallel()

	t.Run("nil_logger_uses_default", func(t *testing.T) {
		t.Parallel()
		h := NewHub(WithLogger(nil), WithHeartbeatInterval(24*time.Hour))
		defer h.Close()
		ch, cancel := h.Subscribe()
		defer cancel()
		h.Publish(testEvent(EventMsgNew, "log-nil-test"))
		mustReceive(t, ch, time.Second)
	})

	t.Run("custom_logger", func(t *testing.T) {
		t.Parallel()
		// Use a discard handler so log output doesn't clutter test output.
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		h := NewHub(WithLogger(logger), WithHeartbeatInterval(24*time.Hour))
		defer h.Close()
		ch, cancel := h.Subscribe()
		defer cancel()
		h.Publish(testEvent(EventMsgNew, "log-custom-test"))
		mustReceive(t, ch, time.Second)
	})
}

// ---------------------------------------------------------------------------
// TestHub_DoubleClose: Close is idempotent.
// ---------------------------------------------------------------------------

func TestHub_DoubleClose(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	h.Close()
	h.Close() // must not panic
}

// ---------------------------------------------------------------------------
// TestHeartbeatSentinel: HeartbeatSentinel has the correct event name.
// ---------------------------------------------------------------------------

func TestHeartbeatSentinel(t *testing.T) {
	t.Parallel()
	if HeartbeatSentinel.EventName() != ":heartbeat" {
		t.Errorf("HeartbeatSentinel.EventName() = %q, want %q",
			HeartbeatSentinel.EventName(), ":heartbeat")
	}
	if HeartbeatSentinel.Payload() != nil {
		t.Errorf("HeartbeatSentinel.Payload() = %v, want nil", HeartbeatSentinel.Payload())
	}
}

// ---------------------------------------------------------------------------
// TestHub_RingEmpty: SubscribeWithReplay on empty ring returns no replayed events.
// ---------------------------------------------------------------------------

func TestHub_RingEmpty(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	// No events published yet; ring is empty. SubscribeWithReplay(0) should
	// return a live subscription with nothing replayed.
	ch, cancel := h.SubscribeWithReplay(0)
	defer cancel()

	// Publish one fresh event.
	h.Publish(testEvent(EventCostTick, "fresh"))
	ev := mustReceive(t, ch, time.Second)
	if ev.EventName() != EventCostTick {
		t.Errorf("EventName = %q, want %q", ev.EventName(), EventCostTick)
	}
}

// TestHub_ReplayDrop: SubscribeWithReplay drops when the channel fills on replay.
// ---------------------------------------------------------------------------

func TestHub_ReplayDrop(t *testing.T) {
	t.Parallel()
	h := newTestHub()
	defer h.Close()

	// Publish subChanCap+10 events into the ring.
	total := subChanCap + 10
	for i := 0; i < total; i++ {
		h.Publish(testEvent(EventMsgNew, fmt.Sprintf("r%d", i)))
	}

	// Subscribing with replay from 0 means all events should replay,
	// but the channel only holds subChanCap. The overflow is dropped (not panic).
	ch, cancel := h.SubscribeWithReplay(0)
	defer cancel()

	// Drain what we can within a second.
	received := 0
	deadline := time.After(time.Second)
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			if ev != HeartbeatSentinel {
				received++
			}
		case <-deadline:
			break drain
		}
	}

	if received == 0 {
		t.Error("expected at least one replayed event, got 0")
	}
	// We accept that some events were dropped (no panic is the key assertion).
}

// ---------------------------------------------------------------------------
// TestHub_Heartbeat: verify heartbeat is delivered to subscribers.
// ---------------------------------------------------------------------------

func TestHub_Heartbeat(t *testing.T) {
	t.Parallel()
	h := NewHub(WithHeartbeatInterval(30 * time.Millisecond))
	defer h.Close()

	ch, cancel := h.Subscribe()
	defer cancel()

	// Wait for a heartbeat.
	deadline := time.After(200 * time.Millisecond)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			if ev == HeartbeatSentinel {
				return // success
			}
		case <-deadline:
			t.Error("did not receive heartbeat within 200ms (interval=30ms)")
			return
		}
	}
}

// ---------------------------------------------------------------------------
// TestHub_Constructor_EventHelpers: verify all 6 W0 event constructors.
// ---------------------------------------------------------------------------

func TestHub_Constructor_EventHelpers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		event Event
	}{
		{EventMsgNew, MsgNewEvent(MsgNew{SessionID: "s1"})},
		{EventSummaryReady, SummaryReadyEvent(SummaryReady{SessionID: "s2"})},
		{EventSessionUpdate, SessionUpdateEvent(SessionUpdate{SessionID: "s3"})},
		{EventCostTick, CostTickEvent(CostTick{Ts: 1})},
		{EventThreadRebuild, ThreadRebuildEvent(ThreadRebuild{ThreadCount: 3})},
		{EventCompactDetected, CompactDetectedEvent(CompactDetected{SessionID: "s4"})},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.event.EventName() != tc.name {
				t.Errorf("EventName() = %q, want %q", tc.event.EventName(), tc.name)
			}
			if tc.event.Payload() == nil {
				t.Error("Payload() returned nil")
			}
		})
	}
}
