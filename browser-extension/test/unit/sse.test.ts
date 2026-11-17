import { describe, expect, it } from 'vitest';
import { SSEParser } from '../../src/adapters/sse.js';

describe('SSEParser', () => {
  it('preserves CRLF, comments, multiline data and split delimiters', () => {
    const parser = new SSEParser();
    const events = [
      'event: custom\r',
      '\n: ignored\r\ndata: first\r',
      '\ndata: second\r\n\r',
      '\n',
    ].flatMap((chunk) => parser.push(chunk));

    expect(events).toEqual([{ event: 'custom', data: 'first\nsecond' }]);
    expect(parser.rejected).toBe(false);
  });

  it('flushes a legitimate trailing frame without a delimiter', () => {
    const parser = new SSEParser();
    expect(parser.push('data: trailing')).toEqual([]);
    expect(parser.flush()).toEqual([{ event: 'message', data: 'trailing' }]);
  });

  it('rejects a delimiter-free stream after bounded tiny fragments', () => {
    const parser = new SSEParser({ maxLineChars: 100, maxFrameChars: 100, maxFrameParts: 4 });
    for (let i = 0; i < 4; i++) expect(parser.push('x')).toEqual([]);
    expect(parser.rejected).toBe(false);

    expect(parser.push('x')).toEqual([]);
    expect(parser.rejected).toBe(true);
    expect(parser.flush()).toEqual([]);
  });

  it('rejects oversized lines and multi-line frames independently', () => {
    const line = new SSEParser({ maxLineChars: 8, maxFrameChars: 100, maxFrameParts: 10 });
    line.push('data: xxx');
    expect(line.rejected).toBe(true);

    const frame = new SSEParser({ maxLineChars: 10, maxFrameChars: 15, maxFrameParts: 10 });
    frame.push('data: a\ndata: b\n');
    expect(frame.rejected).toBe(true);
  });
});
