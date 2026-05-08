/**
 * sse.ts — Typed EventSource client with automatic reconnect.
 *
 * Event names and payload types mirror internal/api/sse_events.go (W0-frozen).
 *
 * Usage:
 *   const unsub = subscribe({
 *     onMsgNew: (payload) => { ... },
 *     onCostTick: (payload) => { ... },
 *   });
 *   // later:
 *   unsub();
 */

import type { CompactDetected, CostTick, MsgNew, SessionUpdate, SummaryReady, ThreadRebuild } from './types.js';
import {
  SSE_EVENT_COMPACT_DETECTED,
  SSE_EVENT_COST_TICK,
  SSE_EVENT_MSG_NEW,
  SSE_EVENT_SESSION_UPDATE,
  SSE_EVENT_SUMMARY_READY,
  SSE_EVENT_THREAD_REBUILD
} from './types.js';

// ---------------------------------------------------------------------------
// Handler interface
// ---------------------------------------------------------------------------

export interface SSEHandlers {
  onMsgNew?: (payload: MsgNew) => void;
  onSummaryReady?: (payload: SummaryReady) => void;
  onSessionUpdate?: (payload: SessionUpdate) => void;
  onCostTick?: (payload: CostTick) => void;
  onThreadRebuild?: (payload: ThreadRebuild) => void;
  onCompactDetected?: (payload: CompactDetected) => void;
  /** Called when the connection opens or reconnects. */
  onOpen?: () => void;
  /** Called when a parse error occurs (bad JSON from server). */
  onParseError?: (event: MessageEvent, error: unknown) => void;
}

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const SSE_URL =
  typeof import.meta !== 'undefined' &&
  typeof (import.meta as { env?: { VITE_API_BASE?: string } }).env !== 'undefined'
    ? `${(import.meta as { env?: { VITE_API_BASE?: string } }).env?.VITE_API_BASE ?? ''}/events`
    : '/events';

/** Minimum reconnect delay in milliseconds. */
const RECONNECT_DELAY_MS = 1_500;
/** Maximum reconnect delay (exponential backoff cap). */
const RECONNECT_DELAY_MAX_MS = 30_000;

// ---------------------------------------------------------------------------
// Factory — exported for testing
// ---------------------------------------------------------------------------

/** EventSource factory; can be replaced in tests with a mock. */
export type EventSourceFactory = (url: string) => EventSource;

let _eventSourceFactory: EventSourceFactory = (url) => new EventSource(url);

/** Override the EventSource factory (used in unit tests). */
export function setEventSourceFactory(factory: EventSourceFactory): void {
  _eventSourceFactory = factory;
}

/** Reset to the default browser EventSource factory. */
export function resetEventSourceFactory(): void {
  _eventSourceFactory = (url) => new EventSource(url);
}

// ---------------------------------------------------------------------------
// Core subscriber
// ---------------------------------------------------------------------------

/**
 * Subscribe to the klyne SSE stream.
 *
 * @param handlers - Object whose properties are optional per-event callbacks.
 * @param url - Override the default /events URL (useful in tests).
 * @returns An unsubscribe function; call it to close the EventSource.
 */
export function subscribe(handlers: SSEHandlers, url: string = SSE_URL): () => void {
  let es: EventSource | null = null;
  let closed = false;
  let reconnectDelay = RECONNECT_DELAY_MS;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

  function connect(): void {
    if (closed) return;
    es = _eventSourceFactory(url);

    es.onopen = () => {
      reconnectDelay = RECONNECT_DELAY_MS; // reset backoff on successful connect
      handlers.onOpen?.();
    };

    // Register per-event listeners.
    addListener(es, SSE_EVENT_MSG_NEW, (raw) => {
      handlers.onMsgNew?.(raw as MsgNew);
    });
    addListener(es, SSE_EVENT_SUMMARY_READY, (raw) => {
      handlers.onSummaryReady?.(raw as SummaryReady);
    });
    addListener(es, SSE_EVENT_SESSION_UPDATE, (raw) => {
      handlers.onSessionUpdate?.(raw as SessionUpdate);
    });
    addListener(es, SSE_EVENT_COST_TICK, (raw) => {
      handlers.onCostTick?.(raw as CostTick);
    });
    addListener(es, SSE_EVENT_THREAD_REBUILD, (raw) => {
      handlers.onThreadRebuild?.(raw as ThreadRebuild);
    });
    addListener(es, SSE_EVENT_COMPACT_DETECTED, (raw) => {
      handlers.onCompactDetected?.(raw as CompactDetected);
    });

    es.onerror = () => {
      // EventSource readyState 2 = CLOSED; schedule reconnect.
      if (es) {
        es.close();
        es = null;
      }
      if (!closed) {
        reconnectTimer = setTimeout(() => {
          reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_DELAY_MAX_MS);
          connect();
        }, reconnectDelay);
      }
    };
  }

  function addListener(
    source: EventSource,
    eventName: string,
    handler: (payload: unknown) => void
  ): void {
    source.addEventListener(eventName, (e: Event) => {
      const msg = e as MessageEvent;
      try {
        const payload: unknown = JSON.parse(msg.data as string);
        handler(payload);
      } catch (err) {
        handlers.onParseError?.(msg, err);
      }
    });
  }

  connect();

  return function unsubscribe(): void {
    closed = true;
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (es) {
      es.close();
      es = null;
    }
  };
}
