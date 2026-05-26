/**
 * Unit tests for tools/market-data.js
 * Run: node test/market-data.test.js
 *
 * Uses Node's built-in fetch mock via globalThis override — no external deps.
 */

import { fetchPoolMarketData, clearMarketDataCache } from "../tools/market-data.js";

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

function mockFetch(responseBody, { status = 200, throwError = null } = {}) {
  globalThis.fetch = async () => {
    if (throwError) throw throwError;
    return {
      ok: status >= 200 && status < 300,
      status,
      json: async () => responseBody,
    };
  };
}

const SAMPLE_PAIR = {
  pairAddress: "FAKE1111111111111111111111111111111111111111",
  volume: { m5: 1234.56, h1: 45678 },
  priceChange: { m5: -3.2, h1: 8.1 },
  txns: { m5: { buys: 12, sells: 34 } },
  liquidity: { usd: 98765 },
};

// ─── Test 1: happy path ───────────────────────────────────────────
console.log("\nTest 1: happy path — valid DexScreener response");
clearMarketDataCache();
mockFetch({ pairs: [SAMPLE_PAIR] });

{
  const data = await fetchPoolMarketData("FAKE1111111111111111111111111111111111111111");
  assert(data !== null, "returns non-null");
  assert(data.volume_5m === 1234.56, `volume_5m correct (${data?.volume_5m})`);
  assert(data.volume_1h === 45678, `volume_1h correct (${data?.volume_1h})`);
  assert(data.price_change_5m === -3.2, `price_change_5m correct (${data?.price_change_5m})`);
  assert(data.txn_buys_5m === 12, `txn_buys_5m correct (${data?.txn_buys_5m})`);
  assert(data.txn_sells_5m === 34, `txn_sells_5m correct (${data?.txn_sells_5m})`);
  assert(data.liquidity_usd === 98765, `liquidity_usd correct (${data?.liquidity_usd})`);
  assert(typeof data.fetched_at === "string", "fetched_at is string");
}

// ─── Test 2: cache hit (fetch not called again) ───────────────────
console.log("\nTest 2: cache — second call returns cached result");
let fetchCalled = false;
globalThis.fetch = async () => { fetchCalled = true; return { ok: true, json: async () => ({ pairs: [] }) }; };

{
  const data = await fetchPoolMarketData("FAKE1111111111111111111111111111111111111111");
  assert(!fetchCalled, "fetch was NOT called (cache hit)");
  assert(data?.volume_5m === 1234.56, "cached data returned");
}

// ─── Test 3: HTTP error returns null ─────────────────────────────
console.log("\nTest 3: HTTP 429 → null, no throw");
clearMarketDataCache();
mockFetch({}, { status: 429 });

{
  const data = await fetchPoolMarketData("FAKE2222222222222222222222222222222222222222");
  assert(data === null, "returns null on HTTP error");
}

// ─── Test 4: network error returns null ──────────────────────────
console.log("\nTest 4: network error → null, no throw");
clearMarketDataCache();
mockFetch(null, { throwError: new Error("ECONNREFUSED") });

{
  const data = await fetchPoolMarketData("FAKE3333333333333333333333333333333333333333");
  assert(data === null, "returns null on network error");
}

// ─── Test 5: empty pairs array → null ────────────────────────────
console.log("\nTest 5: empty pairs array → null");
clearMarketDataCache();
mockFetch({ pairs: [] });

{
  const data = await fetchPoolMarketData("FAKE4444444444444444444444444444444444444444");
  assert(data === null, "returns null when pairs array is empty");
}

// ─── Test 6: partial fields → null fields in output ──────────────
console.log("\nTest 6: partial DexScreener response → null fields");
clearMarketDataCache();
mockFetch({ pairs: [{ volume: { m5: 500 } }] }); // missing priceChange, txns, liquidity

{
  const data = await fetchPoolMarketData("FAKE5555555555555555555555555555555555555555");
  assert(data !== null, "still returns object");
  assert(data.volume_5m === 500, "volume_5m populated");
  assert(data.price_change_5m === null, "price_change_5m is null when missing");
  assert(data.txn_buys_5m === null, "txn_buys_5m is null when missing");
}

// ─── Test 7: null address → null ─────────────────────────────────
console.log("\nTest 7: null address → null");
clearMarketDataCache();

{
  const data = await fetchPoolMarketData(null);
  assert(data === null, "returns null for null address");
}

// ─── Summary ─────────────────────────────────────────────────────
console.log(`\n${"─".repeat(40)}`);
console.log(`Results: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
