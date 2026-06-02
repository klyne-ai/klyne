// Package handlers — SSE /events handler (W8).
//
// EventsMounter implements api.RouterMounter and registers:
//
//	GET /events — Server-Sent Events stream
//
// Each connected client receives a fan-out copy of every event published to
// the Hub. The handler:
//  1. Sets the canonical SSE response headers.
//  2. Replays missed events when the client supplies Last-Event-ID.
//  3. Writes each event as an SSE frame (id / event / data / blank line).
//  4. Sends `: ping` heartbeat comments to keep the connection alive.
//  5. Detects client disconnect via r.Context().Done() and calls cancel.
//
// Spec references:
//   - §5  (real-time: SSE not WebSockets)
//   - §7  (event names)
//   - §12 (<100ms p95 SSE latency)
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/klyne-ai/klyne/internal/api"
)

// EventsMounter registers GET /events on a chi.Router.
// It satisfies api.RouterMounter:
//
//	type RouterMounter interface { Mount(r chi.Router) }
//
// Wire it at daemon startup (W12):
//
//	hub := api.NewHub()
//	deps.Mounters = []api.RouterMounter{
//	    handlers.NewMounter(handlers.Deps{...}),
//	    &handlers.EventsMounter{Hub: hub},
//	}
type EventsMounter struct {
	// Hub is the SSE broadcast hub. Must not be nil.
	Hub *api.Hub
}

// Mount registers GET /events on r. Called by api.NewRouter after the
// middleware stack is attached.
func (m *EventsMounter) Mount(r chi.Router) {
	r.Get(api.RouteEvents, m.handle)
}

// handle is the SSE handler for GET /events.
func (m *EventsMounter) handle(w http.ResponseWriter, r *http.Request) {
	// SSE requires http.Flusher; verify before writing any headers.
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Canonical SSE response headers.
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-cache")
	hdr.Set("Connection", "keep-alive")
	hdr.Set("X-Accel-Buffering", "no") // prevent nginx from buffering the stream

	// Send headers immediately so the client's HTTP request returns and it
	// can start reading the stream. Without this, net/http does not write
	// headers until the first body write — and our body writes only happen
	// when an event is published, so a quiet hub would block client.Do()
	// forever (or until its context deadline).
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Choose subscribe variant based on Last-Event-ID replay header.
	var (
		ch     <-chan api.Event
		cancel func()
	)
	if lastIDStr := r.Header.Get("Last-Event-ID"); lastIDStr != "" {
		if lastID, err := strconv.ParseUint(lastIDStr, 10, 64); err == nil {
			ch, cancel = m.Hub.SubscribeWithReplay(lastID)
		} else {
			ch, cancel = m.Hub.Subscribe()
		}
	} else {
		ch, cancel = m.Hub.Subscribe()
	}
	defer cancel()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			// Client disconnected or request was cancelled.
			return

		case ev, ok := <-ch:
			if !ok {
				// Hub.Close() was called; terminate the stream.
				return
			}

			// Heartbeat: emit SSE comment to keep the TCP connection alive
			// through proxies without dispatching a real event to the client.
			// Heartbeats carry NO id: line so they never advance the client's
			// Last-Event-ID cursor past a real event.
			if ev == api.HeartbeatSentinel {
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
				continue
			}

			// Emit the hub's ring/sequence id as the SSE id: — the SAME value
			// a later Last-Event-ID replay keys the ring on. The hub delivers
			// every live and replayed event wrapped in api.SeqEvent carrying
			// that id. (A bare Event without a seq — should not happen — falls
			// back to skipping the id: line rather than emitting a wrong one.)
			seqEv, hasSeq := ev.(api.SeqEvent)
			var id uint64
			if hasSeq {
				id = seqEv.Seq
			}

			// Marshal payload to JSON for the data line.
			data, err := json.Marshal(ev.Payload())
			if err != nil {
				// Should not happen for well-formed events; log as SSE comment.
				_, _ = fmt.Fprintf(w, ": marshal error for %s: %v\n\n",
					ev.EventName(), err)
				flusher.Flush()
				continue
			}

			// Write the SSE frame:
			//   id: <seq>\n   (omitted when the event carries no seq)
			//   event: <name>\n
			//   data: <json>\n
			//   \n         ← blank line terminates the frame
			if hasSeq {
				_, _ = fmt.Fprintf(w, "id: %d\n", id)
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n",
				ev.EventName(), data)
			flusher.Flush()
		}
	}
}
