/**
 * Unit checks for EV measurement + EV-weighted threshold evolution.
 * Run: node test/test-ev.js   (no external runner / no server data needed)
 */
import assert from "node:assert/strict";
import { computeEdge, weightedMean, deriveThresholdChanges } from "../lessons.js";

let passed = 0;
function check(name, fn) { fn(); passed++; console.log("  ✓", name); }

console.log("computeEdge:");

check("steamroller: high win rate, negative EV, payoff<1", () => {
  const recs = [
    ...Array.from({ length: 9 }, () => ({ pnl_pct: 1, pnl_usd: 1 })),
    { pnl_pct: -50, pnl_usd: -50 },
  ];
  const e = computeEdge(recs);
  assert.equal(e.n, 10);
  assert.equal(e.win_rate_pct, 90);
  assert.equal(e.avg_win_pct, 1);
  assert.equal(e.avg_loss_pct, -50);
  assert.equal(e.ev_pct, -4.1);
  assert.equal(e.payoff_ratio, 0.02);
  assert.equal(e.worst_loss_pct, -50);
  assert.equal(e.steamroller_warning, true);
});

check("positive EV with low win rate, no steamroller", () => {
  const recs = [
    ...Array.from({ length: 4 }, () => ({ pnl_pct: 20, pnl_usd: 20 })),
    ...Array.from({ length: 6 }, () => ({ pnl_pct: -10, pnl_usd: -10 })),
  ];
  const e = computeEdge(recs);
  assert.equal(e.win_rate_pct, 40);
  assert.equal(e.ev_pct, 2);
  assert.equal(e.payoff_ratio, 2);
  assert.equal(e.steamroller_warning, false);
});

check("empty -> null", () => {
  assert.equal(computeEdge([]), null);
});

console.log("weightedMean:");

check("weights bias the mean", () => {
  assert.equal(weightedMean([2, 10], [1, 9]), 9.2);
});

check("all-zero weights -> plain mean", () => {
  assert.equal(weightedMean([4, 6], [0, 0]), 5);
});

console.log("deriveThresholdChanges (EV-weighted):");

check("penny-win does not protect low-fee zone; floor raised by magnitude", () => {
  const config = { screening: { minFeeActiveTvlRatio: 0.1, minOrganic: 70 } };
  const perf = [
    { fee_tvl_ratio: 2.0,  pnl_pct: 30 },   // big winner, high fee/TVL
    { fee_tvl_ratio: 0.12, pnl_pct: 1 },    // penny winner, low fee/TVL (must barely count)
    { fee_tvl_ratio: 0.11, pnl_pct: -20 },  // loser
    { fee_tvl_ratio: 0.13, pnl_pct: -25 },  // loser
    { fee_tvl_ratio: 0.10, pnl_pct: -2 },   // neutral, ignored
  ];
  const { changes } = deriveThresholdChanges(perf, config);
  assert.equal(changes.minFeeActiveTvlRatio, 0.12); // nudged up 0.10 -> 0.12 (20%/step cap)
  assert.equal(changes.minOrganic, undefined);      // no organic data -> no change
});

check("below MIN_EVOLVE_POSITIONS -> null", () => {
  assert.equal(deriveThresholdChanges([{ pnl_pct: 10 }], { screening: {} }), null);
});

console.log(`\n${passed} checks passed.`);
