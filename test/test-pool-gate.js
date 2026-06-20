/**
 * Unit checks for the pool-history hard gate (deploy_position).
 * Run: node test/test-pool-gate.js
 */
import assert from "node:assert/strict";
import { evaluatePoolHistoryGate } from "../pool-memory.js";

const TH = { minPoolDeploysForGate: 3, maxPoolExclusionRate: 0.6, minPoolAdjustedWinRate: 20 };
let passed = 0;
const check = (name, fn) => { fn(); passed++; console.log("  ✓", name); };

console.log("evaluatePoolHistoryGate:");

check("glippy case: 3 deploys, 100% left range → BLOCK (range-unstable)", () => {
  const mem = { known: true, total_deploys: 3, win_rate: 0.67, avg_pnl_pct: 2.26,
    adjusted_win_rate: 0, adjusted_win_rate_sample_count: 0, oor_exclusion_rate: 1.0 };
  const g = evaluatePoolHistoryGate(mem, TH);
  assert.equal(g.block, true);
  assert.match(g.reason, /range-unstable/);
});

check("in-range loser: clean closes but low adjusted WR → BLOCK", () => {
  const mem = { known: true, total_deploys: 5, oor_exclusion_rate: 0.2,
    adjusted_win_rate: 10, adjusted_win_rate_sample_count: 4 };
  const g = evaluatePoolHistoryGate(mem, TH);
  assert.equal(g.block, true);
  assert.match(g.reason, /in-range loser/);
});

check("healthy pool → PASS", () => {
  const mem = { known: true, total_deploys: 6, oor_exclusion_rate: 0.17,
    adjusted_win_rate: 70, adjusted_win_rate_sample_count: 5 };
  assert.equal(evaluatePoolHistoryGate(mem, TH).block, false);
});

check("thin history (< minDeploys) → PASS even if bad", () => {
  const mem = { known: true, total_deploys: 2, oor_exclusion_rate: 1.0,
    adjusted_win_rate: 0, adjusted_win_rate_sample_count: 0 };
  assert.equal(evaluatePoolHistoryGate(mem, TH).block, false);
});

check("unknown pool → PASS (first-time deploy)", () => {
  assert.equal(evaluatePoolHistoryGate({ known: false }, TH).block, false);
  assert.equal(evaluatePoolHistoryGate(null, TH).block, false);
});

check("config override loosens exclusion gate", () => {
  const mem = { known: true, total_deploys: 4, oor_exclusion_rate: 0.7,
    adjusted_win_rate: 80, adjusted_win_rate_sample_count: 3 };
  assert.equal(evaluatePoolHistoryGate(mem, TH).block, true);                          // default 0.6 → blocks
  assert.equal(evaluatePoolHistoryGate(mem, { ...TH, maxPoolExclusionRate: 0.9 }).block, false); // loosened → passes
});

console.log(`\n${passed} checks passed.`);
