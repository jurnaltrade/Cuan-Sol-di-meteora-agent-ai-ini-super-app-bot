/**
 * Whale-Shadow Rebalancing
 * ────────────────────────
 * smart-wallets.js already tracks KOL/alpha wallets and can tell you
 * whether they're IN a given pool right now (checkSmartWalletsOnPool).
 * This module goes one step further: it snapshots each tracked wallet's
 * open DLMM positions on a timer, diffs consecutive snapshots, and
 * detects ACTIONS — a whale closing a position, opening a new one, or
 * materially re-ranging (position address changed for the same pool,
 * which on Meteora DLMM means close+reopen).
 *
 * When a tracked whale acts in a pool where Meridian ALSO has an open
 * position, that's a high-value signal: whales often re-range or exit
 * ahead of visible on-chain deterioration (dev sells, LP migrations,
 * incoming volatility) that pure pool metrics haven't caught up to yet.
 *
 * Drop-in path: whale-shadow.js  (repo root, alongside smart-wallets.js)
 */

import fs from "fs";
import { log } from "./logger.js";
import { repoPath } from "./repo-root.js";
import { listSmartWallets } from "./smart-wallets.js";
import { getMyPositions } from "./tools/dlmm.js";

const SNAPSHOT_FILE = repoPath("whale-shadow-state.json");
const MAX_EVENTS = 200;

function load() {
  if (!fs.existsSync(SNAPSHOT_FILE)) return { snapshots: {}, events: [] };
  try {
    return JSON.parse(fs.readFileSync(SNAPSHOT_FILE, "utf8"));
  } catch {
    return { snapshots: {}, events: [] };
  }
}

function save(data) {
  fs.writeFileSync(SNAPSHOT_FILE, JSON.stringify(data, null, 2));
}

function diffPositions(prevPositions = [], currPositions = []) {
  const prevByAddr = new Map(prevPositions.map((p) => [p.position, p]));
  const currByAddr = new Map(currPositions.map((p) => [p.position, p]));
  const events = [];

  // Closed: was present before, gone now
  for (const [addr, pos] of prevByAddr) {
    if (!currByAddr.has(addr)) {
      events.push({ type: "CLOSED", pool: pos.pool, position: addr });
    }
  }
  // Opened: present now, wasn't before
  for (const [addr, pos] of currByAddr) {
    if (!prevByAddr.has(addr)) {
      // Was this wallet already in this pool under a different position address?
      // (close + reopen in the same pool = re-range, not a fresh entry)
      const rerangedFrom = prevPositions.find((p) => p.pool === pos.pool && !currByAddr.has(p.position));
      events.push({
        type: rerangedFrom ? "RERANGED" : "OPENED",
        pool: pos.pool,
        position: addr,
        previous_position: rerangedFrom?.position || null,
      });
    }
  }
  return events;
}

/**
 * Tool: scan_whale_activity
 * Call once per management cycle. Snapshots every tracked LP-type smart
 * wallet, diffs against the last snapshot, records events, and — most
 * importantly — flags any event that happened in a pool Meridian
 * currently has an open position in.
 */
export async function scanWhaleActivity() {
  const { wallets } = listSmartWallets();
  const lpWallets = wallets.filter((w) => !w.type || w.type === "lp");
  if (lpWallets.length === 0) {
    return { tracked_wallets: 0, events: [], flagged_for_my_positions: [] };
  }

  const data = load();
  const { getWalletPositions } = await import("./tools/dlmm.js");
  const allEvents = [];

  for (const wallet of lpWallets) {
    let currPositions = [];
    try {
      const res = await getWalletPositions({ wallet_address: wallet.address });
      currPositions = res.positions || [];
    } catch (error) {
      log("whale_shadow_warn", `Failed to fetch positions for ${wallet.name}: ${error.message}`);
      continue;
    }

    const prevPositions = data.snapshots[wallet.address] || [];
    const events = diffPositions(prevPositions, currPositions).map((e) => ({
      ...e,
      wallet: wallet.name,
      wallet_address: wallet.address,
      wallet_category: wallet.category,
      ts: new Date().toISOString(),
    }));

    allEvents.push(...events);
    data.snapshots[wallet.address] = currPositions;
  }

  if (allEvents.length) {
    data.events.unshift(...allEvents);
    data.events = data.events.slice(0, MAX_EVENTS);
    for (const e of allEvents) {
      log("whale_shadow", `${e.wallet} (${e.wallet_category}) ${e.type} in pool ${e.pool?.slice(0, 8)}`);
    }
  }
  save(data);

  // Cross-reference against Meridian's own open positions
  let myPositions = [];
  try {
    const res = await getMyPositions({});
    myPositions = res.positions || [];
  } catch {
    // non-fatal — just skip cross-referencing this cycle
  }
  const myPools = new Set(myPositions.map((p) => p.pool));

  const flagged = allEvents.filter((e) => myPools.has(e.pool));

  return {
    tracked_wallets: lpWallets.length,
    events_this_cycle: allEvents,
    flagged_for_my_positions: flagged.map((e) => ({
      ...e,
      signal: e.type === "CLOSED"
        ? `${e.wallet} just EXITED a pool you're currently in — investigate before your next hold decision.`
        : e.type === "RERANGED"
        ? `${e.wallet} just RE-RANGED in a pool you're in — may indicate a price move you haven't reacted to yet.`
        : `${e.wallet} just entered a pool you're already in — mild positive confirmation signal.`,
    })),
  };
}

/**
 * Tool: get_whale_signal_for_pool
 * Lightweight lookup for the screener/manager to check recent whale
 * activity history on a specific pool without re-scanning.
 */
export function getWhaleSignalForPool({ pool_address, hours = 12 }) {
  const data = load();
  const cutoff = Date.now() - hours * 60 * 60 * 1000;
  const relevant = data.events.filter(
    (e) => e.pool === pool_address && new Date(e.ts).getTime() >= cutoff
  );
  const exits = relevant.filter((e) => e.type === "CLOSED").length;
  const entries = relevant.filter((e) => e.type === "OPENED").length;

  return {
    pool: pool_address,
    window_hours: hours,
    events: relevant,
    net_whale_flow: entries - exits,
    signal: exits > entries
      ? `Net whale EXODUS in this pool over the last ${hours}h (${exits} exits vs ${entries} entries) — caution.`
      : entries > exits
      ? `Net whale ACCUMULATION in this pool over the last ${hours}h (${entries} entries vs ${exits} exits) — mild confidence boost.`
      : "No net whale directional signal in this window.",
  };
}
