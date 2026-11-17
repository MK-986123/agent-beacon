// The one source of the serializer every Beacon runtime plugin runs on its hook payloads.
//
// Each plugin installs as a single self-contained file that its runtime loads directly, so a plugin
// cannot import this module at run time. Instead the region between the two markers below is
// written into each plugin by `bun run sync` (sync-safe-clone.ts), verbatim into the TypeScript
// plugins and with its two type annotations removed into OpenClaw's JavaScript one. `bun run check`
// in every plugin, and `bun test` here, fail when any copy differs from this file.
//
// Edit the region here, never in a plugin, then run `bun run sync` in this directory.

// BEGIN shared/safe-clone
// Generated from plugins/shared/safe-clone.ts by `bun run sync`. Edit that file, not this block:
// `bun run check` fails when the two differ.

// How deep into a payload the serializer will walk before giving up.
const maxDepth = 8

// Runtime payloads are untrusted, model-influenced data. These limits keep cloning and the
// subsequent JSON.stringify bounded inside the co-resident plugin host. The byte accounting is
// deliberately conservative: any UTF-16 code unit may become a six-byte JSON escape, so an
// accepted clone is guaranteed to serialize below maxPayloadSerializedBytes without first
// serializing it to find out.
const maxPayloadNodes = 4096
const maxPayloadArrayItems = 1024
const maxPayloadObjectEntries = 1024
const maxPayloadStringCodeUnits = 64 * 1024
const maxPayloadSerializedBytes = 512 * 1024
const payloadLimitMessage = "Beacon payload exceeds the safe serialization limit"

function chargePayload(budget: { nodes: number; bytes: number }, bytes: number): void {
  budget.bytes += bytes
  if (budget.bytes > maxPayloadSerializedBytes) throw new RangeError(payloadLimitMessage)
}

// safeClone turns a live runtime payload into something JSON.stringify accepts.
//
// Runtimes hand plugins live objects, not plain data: AbortSignals, SDK class instances, parent
// references, provider message objects, and tool inputs that are mutable by design. JSON.stringify
// throws on a circular reference and on a bigint, and silently flattens an Error to {}, so each is
// handled here rather than losing the whole payload to one bad field.
//
// Cycles are tracked along the current path and released on the way out, so an object that legibly
// appears twice -- the same file path referenced from two places in one edit -- is kept both times.
// A WeakSet that never released would drop the second occurrence as if it were a cycle.
function safeClone(
  value: unknown,
  depth = 0,
  seen = new WeakSet<object>(),
  budget = { nodes: 0, bytes: 0 },
): unknown {
  budget.nodes++
  if (budget.nodes > maxPayloadNodes) throw new RangeError(payloadLimitMessage)

  if (value === null) {
    chargePayload(budget, 4)
    return null
  }
  const kind = typeof value
  if (kind === "function" || kind === "symbol" || kind === "undefined") return undefined
  if (kind === "bigint") {
    const text = String(value)
    if (text.length > maxPayloadStringCodeUnits) throw new RangeError(payloadLimitMessage)
    chargePayload(budget, 2 + 6 * text.length)
    return text
  }
  if (kind === "string") {
    if (value.length > maxPayloadStringCodeUnits) throw new RangeError(payloadLimitMessage)
    chargePayload(budget, 2 + 6 * value.length)
    return value
  }
  if (kind === "number") {
    // Every finite JavaScript number is shorter than 32 bytes in JSON; non-finite values are null.
    chargePayload(budget, 32)
    return value
  }
  if (kind === "boolean") {
    chargePayload(budget, 5)
    return value
  }
  if (typeof value !== "object") return value
  if (depth >= maxDepth) return undefined

  if (seen.has(value)) return undefined
  if (value instanceof Error) return safeClone({ name: value.name, message: value.message }, depth, seen, budget)
  // Match native JSON: an invalid Date is null, not a lost event.
  if (value instanceof Date) {
    return safeClone(Number.isFinite(value.getTime()) ? value.toISOString() : null, depth, seen, budget)
  }

  seen.add(value)
  try {
    if (Array.isArray(value)) {
      if (value.length > maxPayloadArrayItems) throw new RangeError(payloadLimitMessage)
      chargePayload(budget, 2 + Math.max(0, value.length - 1))
      const out = new Array(value.length)
      for (let index = 0; index < value.length; index++) {
        if (!(index in value)) {
          chargePayload(budget, 4)
          continue
        }
        const cloned = safeClone(value[index], depth + 1, seen, budget)
        if (cloned === undefined) chargePayload(budget, 4)
        else out[index] = cloned
      }
      return out
    }
    chargePayload(budget, 2)
    const record: Record<string, unknown> = value as Record<string, unknown>
    const out: Record<string, unknown> = {}
    let entries = 0
    for (const key in record) {
      if (!Object.prototype.hasOwnProperty.call(record, key)) continue
      entries++
      if (entries > maxPayloadObjectEntries || key.length > maxPayloadStringCodeUnits) {
        throw new RangeError(payloadLimitMessage)
      }
      // Quotes, colon and comma are included; charging a comma for the first key is conservative.
      chargePayload(budget, 4 + 6 * key.length)
      const cloned = safeClone(record[key], depth + 1, seen, budget)
      if (cloned !== undefined) out[key] = cloned
    }
    return out
  } finally {
    seen.delete(value)
  }
}
// END shared/safe-clone

export {
  maxDepth,
  maxPayloadArrayItems,
  maxPayloadNodes,
  maxPayloadObjectEntries,
  maxPayloadSerializedBytes,
  maxPayloadStringCodeUnits,
  payloadLimitMessage,
  safeClone,
}
