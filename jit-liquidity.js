/**
 * JIT / Flow-Following Rapid Liquidity
 * ────────────────────────────────────
 * READ THIS FIRST — scope honesty:
 *
 * "True" JIT liquidity (as done by professional MMs on Uniswap v3/v4) means:
 * seeing a large swap BEFORE it lands, deploying concentrated liquidity in
 * the same block right before it, capturing that swap's fee, then
 * withdrawing in the same or next block — all atomically.
 *
 * That requires seeing pending order flow before it's final. Solana does
 * NOT have a public mempool like Ethereum — transactions generally aren't
 * visible pre-confirmation except through specialized infrastructure
 * (e.g. Jito's block-engine / ShredStream feed, or running your own
 * validator/relayer). This repo has no such feed, and I'm not going to
 * fabricate calls against Jito's searcher API from memory — getting that
 * wrong risks real funds and failed/frontrun bundles.
 *
 * What THIS module actually implements, honestly, is the feasible version:
 * "Flow-Following Rapid LP" — it detects a large swap / volume burst that
 * has ALREADY landed (via fast polling of pool volume + swap_count, which
 * IS available with existing REST infra), and immediately deploys a very
 * tight, small position to catch the continuation flow (momentum, or
 * follow-on arbitrage rebalancing swaps that typically follow a large
 * trade), then force-closes shortly after. This captures a real, common
 * pattern (bursts cluster in time) without needing pre-trade visibility.
 *
 * The `_jito` adapter below is the clearly-marked upgrade path to real
 * atomic JIT if you later add Jito bundle infra — everything else in this
 * file (burst detection, sizing, lifecycle, safety caps) stays the same.
 *
 * Drop-in path: jit-liquidity.js (repo root)
 */

import fs from "fs";
import { log } from "./logger.js";
import { repoPath } from "./repo-root.js";
import { config } from "./config.js";
import { getPoolDetail } from "./tools/screening.js";
import { deployPosition, closePosition } from "./tools/dlmm.js";

const JIT_STATE_FILE = repoPath("jit-liquidity-state.json");

function load() {
  if (!fs.existsSync(JIT_STATE_FILE)) return { watching: {}, sessions: [] };
  try {
    return JSON.parse(fs.readFileSync(JIT_STATE_FILE, "utf8"));
  } catch {
    return { watching: {}, sessions: [] };
  }
}

function save(data) {
  fs.writeFileSync(JIT_STATE_FILE, JSON.stringify(data, null, 2));
}

function jitConfig() {
  const u = config.jit || {};
  return {
    enabled: u.enabled ?? false,             // OFF by default
    burstVolumeMultiplier: u.burstVolumeMultiplier ?? 3, // volume vs rolling baseline to count as a "burst"
    pollIntervalSec: u.pollIntervalSec ?? 15,
    holdSeconds: u.holdSeconds ?? 90,         // how long to keep the rapid position open before forcing close
    positionSizeSol: u.positionSizeSol ?? 0.1,
    maxConcurrentSessions: u.maxConcurrentSessions ?? 1,
    tightBinsBelow: u.tightBinsBelow ?? 6,    // much tighter than normal deploys — this is the whole point
    tightBinsAbove: u.tightBinsAbove ?? 6,
    minFeeToJustifyGasUsd: u.minFeeToJustifyGasUsd ?? 0.5,
  };
}

/**
 * INTEGRATION POINT for real atomic JIT. Left unimplemented on purpose —
 * see file header. Until this is filled in, watchPool() only ever runs
 * the flow-following (post-trade) mode, never true pre-trade JIT.
 */
const _jito = {
  async submitBundle(/* transactions */) {
    throw new Error("Jito bundle submission not wired — this repo has no pre-trade order-flow visibility. See file header for why this is intentionally stubbed.");
  },
};

/**
 * Tool: register_jit_watch
 * Start tracking a pool's volume baseline so burst detection has
 * something to compare against. Call this on pools you're already
 * screening/interested in — cheap, just stores a rolling window.
 */
export async function registerJitWatch({ pool_address, timeframe }) {
  const data = load();
  const pool = await getPoolDetail({ pool_address, timeframe: timeframe || "5m" });
  const sample = { ts: Date.now(), volume: Number(pool.volume_window ?? pool.volume ?? 0) };

  data.watching[pool_address] = data.watching[pool_address] || { samples: [] };
  data.watching[pool_address].samples.push(sample);
  data.watching[pool_address].samples = data.watching[pool_address].samples.slice(-20);
  save(data);

  return { pool: pool_address, samples_recorded: data.watching[pool_address].samples.length };
}

/**
 * Tool: check_jit_opportunity
 * Call on a poll timer (config.jit.pollIntervalSec) for each watched
 * pool. Compares latest volume sample to the rolling baseline; if it's
 * a burst AND JIT is enabled AND under the concurrent-session cap,
 * deploys a tight rapid position and schedules its own force-close.
 */
export async function checkJitOpportunity({ pool_address, pool_name, timeframe }) {
  const cfg = jitConfig();
  if (!cfg.enabled) return { skipped: true, reason: "JIT disabled (config.jit.enabled = false)." };

  const data = load();
  const openSessions = data.sessions.filter((s) => s.status === "open");
  if (openSessions.length >= cfg.maxConcurrentSessions) {
    return { skipped: true, reason: `At max concurrent JIT sessions (${cfg.maxConcurrentSessions}).` };
  }

  const watch = data.watching[pool_address];
  if (!watch || watch.samples.length < 3) {
    await registerJitWatch({ pool_address, timeframe });
    return { skipped: true, reason: "Not enough baseline samples yet — registered this call as the first sample." };
  }

  const pool = await getPoolDetail({ pool_address, timeframe: timeframe || "5m" });
  const latestVolume = Number(pool.volume_window ?? pool.volume ?? 0);
  const baseline = watch.samples.slice(0, -1).reduce((s, x) => s + x.volume, 0) / Math.max(1, watch.samples.length - 1);

  watch.samples.push({ ts: Date.now(), volume: latestVolume });
  watch.samples = watch.samples.slice(-20);
  save(data);

  const isBurst = baseline > 0 && latestVolume >= baseline * cfg.burstVolumeMultiplier;
  if (!isBurst) {
    return { skipped: true, reason: "No volume burst detected.", latest_volume: latestVolume, baseline };
  }

  // ── Burst detected: deploy a tight, small rapid-response position ──
  log("jit", `Burst detected on ${pool_name || pool_address.slice(0, 8)}: volume=${latestVolume} vs baseline=${Math.round(baseline)} (${cfg.burstVolumeMultiplier}x trigger)`);

  const deployResult = await deployPosition({
    pool_address,
    amount_y: cfg.positionSizeSol,
    strategy: "spot",
    bins_below: cfg.tightBinsBelow,
    bins_above: cfg.tightBinsAbove,
    pool_name,
  });

  if (!deployResult?.success && !deployResult?.position) {
    return { success: false, error: deployResult?.error || "Deploy failed", latest_volume: latestVolume, baseline };
  }

  const session = {
    id: `jit_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`,
    pool_address,
    pool_name,
    position: deployResult.position,
    opened_at: new Date().toISOString(),
    hold_until: Date.now() + cfg.holdSeconds * 1000,
    trigger_volume: latestVolume,
    baseline_volume: Math.round(baseline),
    status: "open",
  };
  data.sessions.unshift(session);
  data.sessions = data.sessions.slice(0, 100);
  save(data);

  return { success: true, session, note: `Rapid position opened — call close_expired_jit_sessions() after ${cfg.holdSeconds}s to realize/close it.` };
}

/**
 * Tool: close_expired_jit_sessions
 * Call on the same poll timer. Force-closes any rapid session that has
 * passed its hold window, regardless of PnL — this strategy is about
 * fee capture during a short burst window, not directional conviction,
 * so positions should not be left open indefinitely.
 */
export async function closeExpiredJitSessions() {
  const cfg = jitConfig();
  const data = load();
  const now = Date.now();
  const toClose = data.sessions.filter((s) => s.status === "open" && now >= s.hold_until);

  const results = [];
  for (const session of toClose) {
    try {
      const closeResult = await closePosition({ position_address: session.position, reason: "jit_hold_window_expired" });
      session.status = "closed";
      session.closed_at = new Date().toISOString();
      session.pnl_usd = closeResult?.pnl_usd ?? null;
      session.fees_earned_usd = closeResult?.fees_earned_usd ?? null;
      results.push({ session_id: session.id, ...closeResult });
      log("jit", `Closed rapid session ${session.id}: pnl=${session.pnl_usd} fees=${session.fees_earned_usd}`);
    } catch (error) {
      log("jit_error", `Failed to close JIT session ${session.id}: ${error.message}`);
      results.push({ session_id: session.id, error: error.message });
    }
  }
  save(data);
  return { closed: results.length, results };
}

/**
 * Tool: get_jit_summary
 */
export function getJitSummary() {
  const data = load();
  const closed = data.sessions.filter((s) => s.status === "closed");
  const wins = closed.filter((s) => Number(s.pnl_usd) > 0).length;
  return {
    watched_pools: Object.keys(data.watching).length,
    open_sessions: data.sessions.filter((s) => s.status === "open").length,
    total_sessions: data.sessions.length,
    closed_sessions: closed.length,
    win_rate_pct: closed.length > 0 ? Math.round((wins / closed.length) * 100) : null,
    total_pnl_usd: Math.round(closed.reduce((s, x) => s + (Number(x.pnl_usd) || 0), 0) * 100) / 100,
    recent_sessions: data.sessions.slice(0, 10),
  };
}
