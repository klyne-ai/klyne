package tasks_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klyne-ai/klyne/internal/ai"
	"github.com/klyne-ai/klyne/internal/ai/tasks"
	"github.com/klyne-ai/klyne/internal/api"
	"github.com/klyne-ai/klyne/internal/store"
)

// runnerTestDeps builds the dependencies for a Runner under test.
// fakeProvider and a real hub are shared; caller provides the DB.
func runnerTestDeps(t *testing.T, db *store.DB, fp *fakeProvider, window int) tasks.RunnerDeps {
	t.Helper()
	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })
	return tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: fp,
		Model:    "fake-model",
		Window:   window,
	}
}

// startRunner launches r.Start in a goroutine. It registers a t.Cleanup that
// calls r.Stop() and waits for the runner to shut down before the hub is
// closed. Returns a cancel function for tests that need to cancel early.
func startRunner(t *testing.T, r *tasks.Runner) (cancel context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = r.Start(ctx)
	}()
	// Register cleanup: stop the runner (which drains workers) BEFORE hub.Close.
	// t.Cleanup funcs run in LIFO order, so this must be registered AFTER hub
	// cleanup to run BEFORE it. But since the hub cleanup is registered in the
	// deps helper, we register runner stop here so it runs first (LIFO).
	t.Cleanup(func() {
		cancel()
		_ = r.Stop()
	})
	// Brief yield so the runner goroutine is scheduled and subscribes to the hub.
	time.Sleep(5 * time.Millisecond)
	return cancel
}

// publishMsgNew sends a MsgNew event for the given session.
func publishMsgNew(hub *api.Hub, sessionID string) {
	hub.Publish(api.MsgNewEvent(api.MsgNew{
		SessionID: sessionID,
		MessageID: "msg-" + sessionID,
		Ts:        time.Now().UnixMilli(),
	}))
}

// publishCompact sends a CompactDetected event for the given session.
func publishCompact(hub *api.Hub, sessionID string) {
	hub.Publish(api.CompactDetectedEvent(api.CompactDetected{
		SessionID: sessionID,
		Ts:        time.Now().UnixMilli(),
	}))
}

// waitForSummarize waits up to `timeout` for the fakeProvider to have received
// at least `n` Chat calls (a proxy for n summarize runs).
func waitForSummarize(t *testing.T, fp *fakeProvider, mu *sync.Mutex, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(fp.requests)
		mu.Unlock()
		if count >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	count := len(fp.requests)
	mu.Unlock()
	t.Errorf("timed out waiting for %d summarize calls; got %d", n, count)
}

// syncFakeProvider wraps fakeProvider with a mutex for concurrent tests.
type syncFakeProvider struct {
	mu sync.Mutex
	fp *fakeProvider
}

func newSyncFake(reply string, delay time.Duration) *syncFakeProvider {
	return &syncFakeProvider{fp: &fakeProvider{name: "fake", reply: reply, delay: delay}}
}

func (s *syncFakeProvider) Name() string { return s.fp.Name() }

func (s *syncFakeProvider) Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fp.Chat(ctx, req)
}

func (s *syncFakeProvider) Embed(ctx context.Context, req ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

func (s *syncFakeProvider) Models() []string { return s.fp.Models() }

func (s *syncFakeProvider) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.fp.requests)
}

// ── Tests ───────────────────────────────────────────────────────────────────

// TestRunner_BatchOf50_TriggersSummary verifies that exactly one summarize
// runs after exactly 50 MsgNew events for a single session.
func TestRunner_BatchOf50_TriggersSummary(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-runner-001"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 50)

	sfp := newSyncFake("summary text", 0)

	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })

	deps := tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: sfp,
		Model:    "fake-model",
		Window:   50,
	}

	r := tasks.NewRunner(deps)
	cancel := startRunner(t, r)
	defer cancel()

	// Publish 49 events — no summarize should fire.
	for i := 0; i < 49; i++ {
		publishMsgNew(hub, sessID)
	}
	time.Sleep(30 * time.Millisecond)

	if sfp.callCount() != 0 {
		t.Errorf("expected 0 summarize calls after 49 events, got %d", sfp.callCount())
	}

	// Publish the 50th — triggers exactly one summarize.
	publishMsgNew(hub, sessID)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sfp.callCount() < 1 {
		time.Sleep(10 * time.Millisecond)
	}

	if sfp.callCount() != 1 {
		t.Errorf("expected exactly 1 summarize call after 50 events, got %d", sfp.callCount())
	}
}

// TestRunner_CompactTriggersImmediate verifies that a CompactDetected event
// triggers a summarize immediately, regardless of message count.
func TestRunner_CompactTriggersImmediate(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-runner-002"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 5)

	sfp := newSyncFake("compact summary", 0)

	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })

	deps := tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: sfp,
		Model:    "fake-model",
		Window:   50,
	}

	r := tasks.NewRunner(deps)
	cancel := startRunner(t, r)
	defer cancel()

	// Only 5 messages — well below window=50, so no summary yet.
	for i := 0; i < 5; i++ {
		publishMsgNew(hub, sessID)
	}
	time.Sleep(20 * time.Millisecond)
	if sfp.callCount() != 0 {
		t.Errorf("expected 0 summarize calls before compact, got %d", sfp.callCount())
	}

	// CompactDetected should trigger immediately.
	publishCompact(hub, sessID)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sfp.callCount() < 1 {
		time.Sleep(10 * time.Millisecond)
	}

	if sfp.callCount() < 1 {
		t.Errorf("expected >= 1 summarize call after CompactDetected, got %d", sfp.callCount())
	}
}

// TestRunner_CompactDebounce verifies that multiple CompactDetected events
// within 500ms produce only one summary.
func TestRunner_CompactDebounce(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-runner-debounce"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 3)

	sfp := newSyncFake("debounce summary", 0)

	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })

	deps := tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: sfp,
		Model:    "fake-model",
		Window:   50,
	}

	r := tasks.NewRunner(deps)
	cancel := startRunner(t, r)
	defer cancel()

	// Emit 3 compact events in quick succession (well within 500ms window).
	publishCompact(hub, sessID)
	time.Sleep(10 * time.Millisecond)
	publishCompact(hub, sessID)
	time.Sleep(10 * time.Millisecond)
	publishCompact(hub, sessID)

	// Allow time for the worker to process.
	time.Sleep(200 * time.Millisecond)

	// Should have triggered exactly one summarize.
	if sfp.callCount() != 1 {
		t.Errorf("expected exactly 1 summarize call (debounced), got %d", sfp.callCount())
	}
}

// TestRunner_PerSessionCounters verifies that message counters are independent
// per session: 25 events for A and 25 for B should not trigger any summarize
// (window=50), and then 25 more for A triggers exactly one for A only.
func TestRunner_PerSessionCounters(t *testing.T) {
	db, _ := openTestDB(t)
	sessA := "sess-runner-A"
	sessB := "sess-runner-B"
	seedSession(t, db, sessA)
	seedSession(t, db, sessB)
	// Seed enough messages so Summarize can fetch them.
	seedMessages(t, db, sessA, 60)
	seedMessages(t, db, sessB, 30)

	sfp := newSyncFake("per-session summary", 0)

	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })

	deps := tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: sfp,
		Model:    "fake-model",
		Window:   50,
	}

	r := tasks.NewRunner(deps)
	cancel := startRunner(t, r)
	defer cancel()

	// Interleave 25 events for A and 25 for B.
	for i := 0; i < 25; i++ {
		publishMsgNew(hub, sessA)
		publishMsgNew(hub, sessB)
	}
	time.Sleep(50 * time.Millisecond)

	// Neither session should have hit window=50.
	if sfp.callCount() != 0 {
		t.Errorf("expected 0 summarize calls after 25+25 interleaved events, got %d", sfp.callCount())
	}

	// 25 more for A → A hits 50.
	for i := 0; i < 25; i++ {
		publishMsgNew(hub, sessA)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sfp.callCount() < 1 {
		time.Sleep(10 * time.Millisecond)
	}

	if sfp.callCount() != 1 {
		t.Errorf("expected exactly 1 summarize call (for sessA), got %d", sfp.callCount())
	}
}

// TestRunner_SummariesNotInline asserts that hub.Publish returns almost
// immediately even when the provider takes 100ms. This proves summarize runs
// in a separate goroutine (not inline with the SSE writer — mitigates risk R7).
func TestRunner_SummariesNotInline(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-runner-async"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 50)

	// Provider takes 100ms.
	sfp := newSyncFake("slow summary", 100*time.Millisecond)

	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })

	deps := tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: sfp,
		Model:    "fake-model",
		Window:   50,
	}

	r := tasks.NewRunner(deps)
	cancel := startRunner(t, r)
	defer cancel()

	// Publish 49 to prime the counter.
	for i := 0; i < 49; i++ {
		publishMsgNew(hub, sessID)
	}
	time.Sleep(20 * time.Millisecond)

	// Time the 50th publish — it must return in <10ms (way less than 100ms).
	start := time.Now()
	publishMsgNew(hub, sessID)
	elapsed := time.Since(start)

	if elapsed > 20*time.Millisecond {
		t.Errorf("hub.Publish blocked for %v; want <20ms (summarize must run async)", elapsed)
	}

	// The summarize should complete eventually.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && sfp.callCount() < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if sfp.callCount() < 1 {
		t.Error("summarize never ran (expected at least 1 call)")
	}
}

// TestRunner_StopWaitsForInflight verifies that Stop() waits for an in-flight
// slow summarize to complete before returning.
func TestRunner_StopWaitsForInflight(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-runner-stop"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 50)

	const providerDelay = 80 * time.Millisecond

	// Use an atomic counter to track calls outside the mutex.
	var callCount atomic.Int32
	var callDone atomic.Bool

	// Custom provider that signals when it starts and when it finishes.
	started := make(chan struct{})
	finishedCh := make(chan struct{})

	cp := &callbackProvider{
		name:  "fake",
		reply: "inflight summary",
		delay: providerDelay,
		onCall: func() {
			select {
			case started <- struct{}{}:
			default:
			}
		},
		onDone: func() {
			callCount.Add(1)
			callDone.Store(true)
			select {
			case finishedCh <- struct{}{}:
			default:
			}
		},
	}

	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })

	deps := tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: cp,
		Model:    "fake-model",
		Window:   50,
	}

	r := tasks.NewRunner(deps)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = r.Start(ctx)
	}()
	time.Sleep(5 * time.Millisecond)

	// Trigger summarize by hitting window.
	for i := 0; i < 50; i++ {
		publishMsgNew(hub, sessID)
	}

	// Wait for provider to start.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider never started")
	}

	// Now call Stop — it should block until summarize finishes.
	stopDone := make(chan struct{})
	go func() {
		_ = r.Stop()
		close(stopDone)
	}()

	// Stop must not return before the provider finishes.
	select {
	case <-stopDone:
		if !callDone.Load() {
			t.Error("Stop() returned before in-flight summarize completed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() timed out")
	}
}

// callbackProvider is a test provider that calls onCall/onDone hooks.
type callbackProvider struct {
	name   string
	reply  string
	delay  time.Duration
	onCall func()
	onDone func()
}

func (c *callbackProvider) Name() string { return c.name }

func (c *callbackProvider) Chat(_ context.Context, req ai.ChatRequest) (*ai.ChatResponse, error) {
	if c.onCall != nil {
		c.onCall()
	}
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	if c.onDone != nil {
		c.onDone()
	}
	return &ai.ChatResponse{Text: c.reply, Model: req.Model}, nil
}

func (c *callbackProvider) Embed(_ context.Context, _ ai.EmbedRequest) (*ai.EmbedResponse, error) {
	return nil, ai.ErrUnsupported
}

func (c *callbackProvider) Models() []string { return []string{"fake-model"} }

// TestRunner_SummaryReadyPublished verifies that a SummaryReady event is
// published to the hub after a successful summarize.
func TestRunner_SummaryReadyPublished(t *testing.T) {
	db, _ := openTestDB(t)
	sessID := "sess-runner-event"
	seedSession(t, db, sessID)
	seedMessages(t, db, sessID, 50)

	sfp := newSyncFake("event summary", 0)

	hub := api.NewHub(api.WithHeartbeatInterval(1 * time.Hour))
	t.Cleanup(func() { hub.Close() })

	// Subscribe to hub BEFORE starting the runner.
	evCh, unsub := hub.Subscribe()
	defer unsub()

	deps := tasks.RunnerDeps{
		DB:       db,
		Hub:      hub,
		Provider: sfp,
		Model:    "fake-model",
		Window:   50,
	}

	r := tasks.NewRunner(deps)
	cancel := startRunner(t, r)
	defer cancel()

	// Trigger summarize.
	for i := 0; i < 50; i++ {
		publishMsgNew(hub, sessID)
	}

	// Drain events until we see SummaryReady.
	timeout := time.After(3 * time.Second)
	for {
		select {
		case ev := <-evCh:
			if ev.EventName() == api.EventSummaryReady {
				// We got the event.
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for SummaryReady event")
		}
	}
}
