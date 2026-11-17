// Incremental, bounded SSE frame parser. Handles chunks that split mid-line or
// mid-event, multi-line `data:` fields, CRLF and lone CR line endings. Each
// input character is scanned once; only the current frame is retained.

export interface SSEEvent {
  event: string; // the `event:` field, or 'message' if absent
  data: string; // concatenated `data:` lines (newline-joined)
}

export interface SSEParserLimits {
  maxLineChars: number;
  maxFrameChars: number;
  maxFrameParts: number;
}

export const DEFAULT_SSE_LIMITS: Readonly<SSEParserLimits> = Object.freeze({
  // Provider events are normally small deltas. These limits still leave room
  // for large tool results while bounding a delimiter-free response.
  maxLineChars: 1024 * 1024,
  maxFrameChars: 2 * 1024 * 1024,
  // Bounds retained string references when an attacker sends tiny chunks.
  maxFrameParts: 4096,
});

export class SSEParser {
  private readonly limits: SSEParserLimits;
  private lineParts: string[] = [];
  private lineChars = 0;
  private frameChars = 0;
  private frameParts = 0;
  private event = 'message';
  private dataLines: string[] = [];
  private pendingCR = false;
  private failed = false;

  constructor(limits: Partial<SSEParserLimits> = {}) {
    this.limits = { ...DEFAULT_SSE_LIMITS, ...limits };
  }

  /** Whether an explicit parser bound rejected the current stream. */
  get rejected(): boolean {
    return this.failed;
  }

  /** Push a raw chunk; returns any complete events it produced. */
  push(chunk: string): SSEEvent[] {
    if (this.failed) return [];
    const out: SSEEvent[] = [];
    let start = 0;

    // A CR at the end of the previous chunk already ended its line. Consume a
    // following LF without treating the CRLF pair as an empty line.
    if (this.pendingCR && chunk.length > 0) {
      this.pendingCR = false;
      if (chunk.charCodeAt(0) === 10) start = 1;
    }

    for (let i = start; i < chunk.length; i++) {
      const code = chunk.charCodeAt(i);
      if (code !== 10 && code !== 13) continue;

      if (!this.appendLinePart(chunk.slice(start, i)) || !this.finishLine(out)) return out;

      if (code === 13) {
        if (i + 1 < chunk.length && chunk.charCodeAt(i + 1) === 10) {
          i++;
        } else if (i + 1 === chunk.length) {
          this.pendingCR = true;
        }
      }
      start = i + 1;
    }

    this.appendLinePart(chunk.slice(start));
    return out;
  }

  /** Flush any trailing buffered frame (call on stream end). */
  flush(): SSEEvent[] {
    if (this.failed) return [];
    const out: SSEEvent[] = [];
    this.pendingCR = false;
    if (this.lineChars > 0 && !this.finishLine(out)) return out;
    this.emitFrame(out);
    return out;
  }

  private appendLinePart(part: string): boolean {
    if (part.length === 0) return true;
    if (
      this.lineChars + part.length > this.limits.maxLineChars ||
      this.frameChars + part.length > this.limits.maxFrameChars ||
      this.frameParts + 1 > this.limits.maxFrameParts
    ) {
      this.reject();
      return false;
    }
    this.lineParts.push(part);
    this.lineChars += part.length;
    this.frameChars += part.length;
    this.frameParts++;
    return true;
  }

  private finishLine(out: SSEEvent[]): boolean {
    const line =
      this.lineParts.length === 0
        ? ''
        : this.lineParts.length === 1
          ? this.lineParts[0]
          : this.lineParts.join('');
    this.lineParts = [];
    this.lineChars = 0;

    if (line === '') {
      this.emitFrame(out);
      return true;
    }
    if (this.frameChars + 1 > this.limits.maxFrameChars) {
      this.reject();
      return false;
    }
    this.frameChars++;

    if (line.startsWith(':')) return true;
    const idx = line.indexOf(':');
    const field = idx === -1 ? line : line.slice(0, idx);
    // Spec: strip one leading space after the colon.
    let value = idx === -1 ? '' : line.slice(idx + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'event') this.event = value;
    else if (field === 'data') this.dataLines.push(value);
    return true;
  }

  private emitFrame(out: SSEEvent[]): void {
    if (this.dataLines.length > 0 || this.event !== 'message') {
      out.push({ event: this.event, data: this.dataLines.join('\n') });
    }
    this.event = 'message';
    this.dataLines = [];
    this.frameChars = 0;
    this.frameParts = 0;
  }

  private reject(): void {
    this.failed = true;
    this.lineParts = [];
    this.lineChars = 0;
    this.event = 'message';
    this.dataLines = [];
    this.frameChars = 0;
    this.frameParts = 0;
    this.pendingCR = false;
  }
}
