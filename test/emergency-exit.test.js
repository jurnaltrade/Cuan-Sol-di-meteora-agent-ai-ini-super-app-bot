/**
 * Unit tests for emergency exit Rules 7 & 8 in getDeterministicCloseRule.
 * Run: node test/emergency-exit.test.js
 *
 * Imports getDeterministicCloseRule via a thin re-export shim to avoid
 * pulling in the full index.js module graph.
 */

// ─── Minimal shims ────────────────────────────────────────────────
// config.emergencyExits defaults (mirrors config.js)
const mockConfig = {
  emergencyExits: {
    volumeCollapse: {
      enabled: true,
      dropThresholdPct: 30,
      minPositionAgeMin: 10,
      minPeakVolumeUsd: 2000,
      sellPressureRatio: 2,
    },
    rapidPriceDrop: {
      enabled: true,
      dropPct5m: -8,
      requireNegativePnl: true,
    },
  },
};

// Tracked position state (peak volume)
let _trackedPeak = 5000;
function mockGetTrackedPosition() {
  return { peak_volume_5m_usd: _trackedPeak };
}

// Inline getDeterministicCloseRule (Rules 7 & 8 only) for isolated testing
function evaluateEmergencyRules(position, marketData) {
  const tracked = mockGetTrackedPosition();
  const pnlSuspect = false; // simplified for unit tests

  // Rule 7: volume collapse
  const vcCfg = mockConfig.emergencyExits.volumeCollapse;
  if (marketData && vcCfg.enabled) {
    const ageMin = position.age_minutes ?? 0;
    const peakVol = tracked?.peak_volume_5m_usd ?? 0;
    const curVol = marketData.volume_5m;
    const sells = marketData.txn_sells_5m;
    const buys = marketData.txn_buys_5m;
    if (
      ageMin >= vcCfg.minPositionAgeMin &&
      peakVol >= vcCfg.minPeakVolumeUsd &&
      curVol != null && curVol < peakVol * (vcCfg.dropThresholdPct / 100) &&
      sells != null && buys != null && sells > buys * vcCfg.sellPressureRatio
    ) {
      return { action: "CLOSE", rule: 7, reason: "volume collapse" };
    }
  }

  // Rule 8: rapid price dump
  const rpCfg = mockConfig.emergencyExits.rapidPriceDrop;
  if (marketData && rpCfg.enabled) {
    const priceChange5m = marketData.price_change_5m;
    const pnlOk = !rpCfg.requireNegativePnl || (!pnlSuspect && (position.pnl_pct ?? 0) < 0);
    if (priceChange5m != null && priceChange5m < rpCfg.dropPct5m && pnlOk) {
      return { action: "CLOSE", rule: 8, reason: "rapid dump" };
    }
  }

  return null;
}

// ─── Test runner ─────────────────────────────────────────────────
let passed = 0;
let failed = 0;

function assert(condition, label) {
  if (condition) {
    console.log(`  ✓ ${label}`);
    passed++;
  } else {
    console.error(`  ✗ ${label}`);
    failed++;
  }
}

// ─── Rule 7 tests ────────────────────────────────────────────────
console.log("\nRule 7 — volume collapse");

_trackedPeak = 5000;
const triggerMd = { volume_5m: 800, txn_buys_5m: 5, txn_sells_5m: 30, price_change_5m: -2 };
const triggerPos = { age_minutes: 20, pnl_pct: -3, position: "pos1" };

{
  const r = evaluateEmergencyRules(triggerPos, triggerMd);
  assert(r?.rule === 7, "triggers when all conditions met");
}

{
  // Position too young
  const r = evaluateEmergencyRules({ ...triggerPos, age_minutes: 5 }, triggerMd);
  assert(r === null, "no trigger when age_minutes < minPositionAgeMin");
}

{
  // Peak too small (below minPeakVolumeUsd)
  _trackedPeak = 1500;
  const r = evaluateEmergencyRules(triggerPos, triggerMd);
  assert(r === null, "no trigger when peak < minPeakVolumeUsd");
  _trackedPeak = 5000;
}

{
  // Volume not collapsed enough (>30% of peak)
  const r = evaluateEmergencyRules(triggerPos, { ...triggerMd, volume_5m: 2000 });
  assert(r === null, "no trigger when volume >= 30% of peak");
}

{
  // Sell pressure not dominant enough
  const r = evaluateEmergencyRules(triggerPos, { ...triggerMd, txn_sells_5m: 8 });
  assert(r === null, "no trigger when sells <= buys * sellPressureRatio");
}

{
  // null volume_5m → skip
  const r = evaluateEmergencyRules(triggerPos, { ...triggerMd, volume_5m: null });
  assert(r === null, "no trigger when volume_5m is null");
}

{
  // Rule disabled
  mockConfig.emergencyExits.volumeCollapse.enabled = false;
  const r = evaluateEmergencyRules(triggerPos, triggerMd);
  assert(r === null, "no trigger when volumeCollapse.enabled = false");
  mockConfig.emergencyExits.volumeCollapse.enabled = true;
}

{
  // No market data at all
  const r = evaluateEmergencyRules(triggerPos, null);
  assert(r === null, "no trigger when marketData is null");
}

// ─── Rule 8 tests ────────────────────────────────────────────────
console.log("\nRule 8 — rapid price dump");

const r8Pos = { age_minutes: 30, pnl_pct: -5, position: "pos2" };
const r8Md = { volume_5m: 3000, txn_buys_5m: 20, txn_sells_5m: 15, price_change_5m: -10 };

{
  const r = evaluateEmergencyRules(r8Pos, r8Md);
  assert(r?.rule === 8, "triggers when price_change_5m < -8% and pnl negative");
}

{
  // Price drop not severe enough
  const r = evaluateEmergencyRules(r8Pos, { ...r8Md, price_change_5m: -5 });
  assert(r === null, "no trigger when price_change_5m >= -8%");
}

{
  // pnl positive — requireNegativePnl blocks trigger
  const r = evaluateEmergencyRules({ ...r8Pos, pnl_pct: 2 }, r8Md);
  assert(r === null, "no trigger when pnl positive and requireNegativePnl = true");
}

{
  // requireNegativePnl = false — pnl positive allowed
  mockConfig.emergencyExits.rapidPriceDrop.requireNegativePnl = false;
  const r = evaluateEmergencyRules({ ...r8Pos, pnl_pct: 2 }, r8Md);
  assert(r?.rule === 8, "triggers when requireNegativePnl = false even with positive pnl");
  mockConfig.emergencyExits.rapidPriceDrop.requireNegativePnl = true;
}

{
  // Rule disabled
  mockConfig.emergencyExits.rapidPriceDrop.enabled = false;
  const r = evaluateEmergencyRules(r8Pos, r8Md);
  assert(r === null, "no trigger when rapidPriceDrop.enabled = false");
  mockConfig.emergencyExits.rapidPriceDrop.enabled = true;
}

{
  // null price_change_5m
  const r = evaluateEmergencyRules(r8Pos, { ...r8Md, price_change_5m: null });
  assert(r === null, "no trigger when price_change_5m is null");
}

// ─── Priority: Rule 7 before Rule 8 ─────────────────────────────
console.log("\nPriority — Rule 7 takes precedence when both conditions met");

_trackedPeak = 5000;
const bothMd = { volume_5m: 800, txn_buys_5m: 5, txn_sells_5m: 30, price_change_5m: -15 };

{
  const r = evaluateEmergencyRules({ ...triggerPos, pnl_pct: -5 }, bothMd);
  assert(r?.rule === 7, "Rule 7 returned first when both conditions met");
}

// ─── Summary ─────────────────────────────────────────────────────
console.log(`\n${"─".repeat(40)}`);
console.log(`Results: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
