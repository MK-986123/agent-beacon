// Accumulates raw intercept events into completed ChatTurns, one parser per
// (tabId, reqId). No chrome.* — unit-testable. State is intentionally in-memory:
// a stream lasts seconds and the SW is kept awake while one is active; the
// DURABLE state (the delivery queue) lives in storage, not here.
//
// The state is bounded. A tab holds at most `maxPerTab` open streams and the
// worker at most `maxTotal`; aggregate in-flight content is bounded by
// `maxTotalBytes`. A stream with no event for `maxIdleMs`, one that exceeds
// `maxEntryBytes`, or one whose parser rejects an oversized SSE frame is closed.
// Closing a stream that never saw `done` finalizes it the same way an aborted
// stream is: whatever was parsed becomes a best-effort partial turn (no
// completedAt), or nothing if the parser captured nothing. So a stream that
// never ends cannot hold memory, or the keepalive, forever.

import type { ChatTurn } from '../shared/types.js';
import type { InterceptEvent } from '../shared/types.js';
import { adapterForHost, type SiteAdapter, type TurnParser } from '../adapters/adapter.js';
import { LIMITS } from '../shared/validate.js';

interface Entry {
  tabId: number;
  adapter: SiteAdapter;
  parser: TurnParser;
  /** Last event time (epoch ms). */
  seenAt: number;
  /** Request body plus chunk characters buffered so far. */
  bytes: number;
}

export interface AssemblerLimits {
  maxPerTab: number;
  maxTotal: number;
  maxIdleMs: number;
  maxEntryBytes: number;
  maxTotalBytes: number;
}

export const DEFAULT_LIMITS: Readonly<AssemblerLimits> = Object.freeze({
  maxPerTab: 8,
  maxTotal: 64,
  // Long reasoning turns can go quiet for a while between chunks.
  maxIdleMs: 10 * 60_000,
  maxEntryBytes: LIMITS.stream,
  maxTotalBytes: 16 * 1024 * 1024,
});

export class Assembler {
  // Map iteration order is insertion order, which makes the first entry for a
  // tab its oldest.
  private entries = new Map<string, Entry>();
  private totalBytes = 0;
  private readonly limits: AssemblerLimits;
  private readonly now: () => number;

  constructor(limits: Partial<AssemblerLimits> = {}, now: () => number = Date.now) {
    this.limits = { ...DEFAULT_LIMITS, ...limits };
    this.now = now;
  }

  private key(tabId: number, reqId: string): string {
    return `${tabId}:${reqId}`;
  }

  /**
   * Feed one intercept event. Returns the turns finalized by it: the stream's
   * own turn on 'done'/'error', plus any older stream closed to make room.
   */
  handle(senderOrigin: string, tabId: number | undefined, event: InterceptEvent): ChatTurn[] {
    const tab = tabId ?? -1;
    const k = this.key(tab, event.reqId);
    const out: ChatTurn[] = [];

    if (event.kind === 'request') {
      // A repeated request id replaces nothing: ids are random, so a repeat is
      // not the interceptor's.
      if (this.entries.has(k)) return out;
      const source = parseOrigin(senderOrigin);
      if (!source) return out;
      const adapter = adapterForHost(source.host);
      if (
        !adapter ||
        !requestMatchesOrigin(event.url, source) ||
        !adapter.matchesRequest(event.url, event.method)
      ) {
        return out;
      }
      const initialBytes = event.body?.length ?? 0;
      if (
        initialBytes > this.limits.maxEntryBytes ||
        initialBytes > this.limits.maxTotalBytes
      ) {
        return out;
      }
      this.makeRoom(tab, out);
      this.makeByteRoom(initialBytes, out);
      const parser = adapter.createParser(event.reqId);
      parser.onRequest(event.url, event.method, event.body);
      this.entries.set(k, {
        tabId: tab,
        adapter,
        parser,
        seenAt: this.now(),
        bytes: initialBytes,
      });
      this.totalBytes += initialBytes;
      return out;
    }

    const entry = this.entries.get(k);
    if (!entry) return out;
    entry.seenAt = this.now();

    switch (event.kind) {
      case 'chunk':
        if (
          entry.bytes + event.chunk.length > this.limits.maxEntryBytes ||
          this.totalBytes + event.chunk.length > this.limits.maxTotalBytes
        ) {
          this.close(k, out);
          return out;
        }
        entry.bytes += event.chunk.length;
        this.totalBytes += event.chunk.length;
        if (!entry.parser.onChunk(event.chunk)) this.close(k, out);
        return out;
      case 'done':
      case 'error':
        this.close(k, out);
        return out;
    }
    return out;
  }

  /** Close every stream idle for longer than maxIdleMs. */
  sweep(): ChatTurn[] {
    const out: ChatTurn[] = [];
    const cutoff = this.now() - this.limits.maxIdleMs;
    for (const [k, e] of [...this.entries]) {
      if (e.seenAt <= cutoff) this.close(k, out);
    }
    return out;
  }

  /** Close every stream of a tab (the tab was closed or navigated away). */
  closeTab(tabId: number): ChatTurn[] {
    const out: ChatTurn[] = [];
    for (const [k, e] of [...this.entries]) {
      if (e.tabId === tabId) this.close(k, out);
    }
    return out;
  }

  /** Number of in-flight streams (used to decide SW keepalive). */
  get active(): number {
    return this.entries.size;
  }

  /** Whether this request is still accepting chunks. */
  has(tabId: number | undefined, reqId: string): boolean {
    return this.entries.has(this.key(tabId ?? -1, reqId));
  }

  private makeRoom(tabId: number, out: ChatTurn[]): void {
    const forTab = [...this.entries].filter(([, e]) => e.tabId === tabId);
    for (let i = 0; i <= forTab.length - this.limits.maxPerTab; i++) this.close(forTab[i][0], out);
    while (this.entries.size >= this.limits.maxTotal) {
      const oldest = this.entries.keys().next().value as string;
      this.close(oldest, out);
    }
  }

  private makeByteRoom(bytes: number, out: ChatTurn[]): void {
    while (this.totalBytes + bytes > this.limits.maxTotalBytes && this.entries.size > 0) {
      const oldest = this.entries.keys().next().value as string;
      this.close(oldest, out);
    }
  }

  private close(k: string, out: ChatTurn[]): void {
    const entry = this.entries.get(k);
    if (!entry) return;
    this.entries.delete(k);
    this.totalBytes -= entry.bytes;
    entry.parser.onDone();
    const turn = entry.parser.getTurn();
    if (turn) out.push(turn);
  }
}

function parseOrigin(raw: string): URL | null {
  try {
    const url = new URL(raw);
    return url.protocol === 'https:' && url.origin === raw ? url : null;
  } catch {
    return null;
  }
}

/** Relative provider URLs resolve at the sender origin; absolute cross-origin URLs do not. */
function requestMatchesOrigin(raw: string, source: URL): boolean {
  try {
    return new URL(raw, `${source.origin}/`).origin === source.origin;
  } catch {
    return false;
  }
}
