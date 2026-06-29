import { describe, it, expect, vi, beforeEach } from "vitest";
import { log } from "../../logger.js";

vi.mock("../../logger.js", () => ({
  log: vi.fn(),
}));

// ─── fetch mock ─────────────────────────────────────────────────────────────
let fetchCalls = [];
const mockFetch = vi.fn((url, opts) => {
  fetchCalls.push({ url, opts });
  return Promise.resolve({
    ok: true,
    json: () => Promise.resolve({ ok: true }),
  });
});
vi.stubGlobal("fetch", mockFetch);

// ─── env setup ──────────────────────────────────────────────────────────────
const ORIG_ENV = { ...process.env };
beforeEach(() => {
  vi.clearAllMocks();
  fetchCalls = [];
  process.env.TELEGRAM_BOT_TOKEN = "test:bot123";
  process.env.TELEGRAM_CHAT_ID = "98765";
  process.env.TELEGRAM_ALLOWED_USER_IDS = "111,222";
  vi.resetModules();
});

afterEach(() => {
  Object.assign(process.env, ORIG_ENV);
});

// ─── helpers ────────────────────────────────────────────────────────────────
function extractSentMessage() {
  if (fetchCalls.length === 0) return null;
  const { opts } = fetchCalls[fetchCalls.length - 1];
  const body = JSON.parse(opts.body);
  return body.text;
}

describe("notifyClose", () => {
  it("includes reason below PnL when provided", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "SOL-USDC", pnlUsd: 1.23, pnlPct: 3.45, reason: "take profit: good return" });

    const msg = extractSentMessage();
    expect(msg).toContain("🔒 <b>Closed</b> SOL-USDC");
    expect(msg).toContain("PnL: +$1.23 (+3.45%)");
    expect(msg).toContain("Reason: take profit: good return");
    // reason appears after PnL
    const pnlIdx = msg.indexOf("PnL:");
    const reasonIdx = msg.indexOf("Reason:");
    expect(reasonIdx).toBeGreaterThan(pnlIdx);
  });

  it("omits reason line when reason is not provided (backward compat)", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "SOL-USDC", pnlUsd: 1.23, pnlPct: 3.45 });

    const msg = extractSentMessage();
    expect(msg).toContain("🔒 <b>Closed</b> SOL-USDC");
    expect(msg).toContain("PnL: +$1.23 (+3.45%)");
    expect(msg).not.toContain("Reason:");
  });

  it("omits reason line when reason is empty string", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "SOL-USDC", pnlUsd: 0.5, pnlPct: 1.2, reason: "" });

    const msg = extractSentMessage();
    expect(msg).not.toContain("Reason:");
  });

  it("shows + sign for positive PnL", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "BONK-SOL", pnlUsd: 5.0, pnlPct: 12.5, reason: "take profit" });

    const msg = extractSentMessage();
    expect(msg).toContain("+$5.00 (+12.50%)");
  });

  it("shows - sign for negative PnL", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "RKC-SOL", pnlUsd: -3.78, pnlPct: -9.71, reason: "stop loss: PnL -3.22% <= -3%" });

    const msg = extractSentMessage();
    // sign is empty for negatives; toFixed produces "-3.78" → "$-3.78 (-9.71%)"
    expect(msg).toContain("$-3.78 (-9.71%)");
  });

  it("handles zero PnL", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "TEST-SOL", pnlUsd: 0, pnlPct: 0, reason: "breakeven" });

    const msg = extractSentMessage();
    expect(msg).toContain("+$0.00 (+0.00%)");
  });

  it("handles null/undefined PnL gracefully", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "TEST-SOL", pnlUsd: null, pnlPct: undefined, reason: "test" });

    const msg = extractSentMessage();
    expect(msg).toContain("+$0.00 (+0.00%)");
  });
});

describe("notifyClose reason — real-world close_reason values from lessons.json", () => {
  it("displays stop loss reason from dlmm.js format", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "RKC-SOL", pnlUsd: -3.78, pnlPct: -9.71, reason: "stop loss: PnL -3.22% <= -3%" });

    const msg = extractSentMessage();
    expect(msg).toContain("Reason: stop loss: PnL -3.22% <= -3%");
  });

  it("displays user-requested close reason", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "ASTEROID-SOL", pnlUsd: 0.57, pnlPct: 1.46, reason: "User requested to close all positions and stop the bot" });

    const msg = extractSentMessage();
    expect(msg).toContain("Reason: User requested to close all positions and stop the bot");
  });

  it("displays low yield reason", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "SOL-USDC", pnlUsd: 0.12, pnlPct: 0.3, reason: "low yield" });

    const msg = extractSentMessage();
    expect(msg).toContain("Reason: low yield");
  });

  it("displays out-of-range close reason", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "SOL-USDC", pnlUsd: 0.33, pnlPct: 0.86, reason: "Out of range" });

    const msg = extractSentMessage();
    expect(msg).toContain("Reason: Out of range");
  });

  it("displays agent decision default reason", async () => {
    const { notifyClose } = await import("../../telegram.js");
    await notifyClose({ pair: "JUP-SOL", pnlUsd: -1.2, pnlPct: -3.1, reason: "agent decision" });

    const msg = extractSentMessage();
    expect(msg).toContain("Reason: agent decision");
  });
});
