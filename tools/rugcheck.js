import { log } from "../logger.js";

const RUGCHECK_BASE = "https://api.rugcheck.xyz/v1";
const CACHE_TTL_MS = 15 * 60 * 1000; // 15 min — holder distribution moves slowly
const REQUEST_TIMEOUT_MS = 10_000;

// mint -> { at, signals }
const _cache = new Map();

function clampPct(n) {
  if (!Number.isFinite(n)) return null;
  return Math.max(0, Math.min(100, n));
}

function sumPct(holders, predicate) {
  if (!Array.isArray(holders)) return null;
  let total = 0;
  let matched = 0;
  for (const h of holders) {
    if (!predicate(h)) continue;
    const pct = Number(h?.pct ?? h?.percentage);
    if (Number.isFinite(pct)) {
      total += pct;
      matched += 1;
    }
  }
  return matched > 0 ? clampPct(total) : null;
}

/**
 * Derive the % of supply held by a class of insider network (e.g. snipers,
 * bundlers). rugcheck exposes `insiderNetworks[]` with a `type` and
 * `tokenAmount`; we convert to a % of total supply when both are available.
 * Returns null when the data isn't present so callers can fail open.
 */
function networkPct(data, typeRegex) {
  const networks = data?.insiderNetworks;
  if (!Array.isArray(networks) || networks.length === 0) return null;
  const supplyRaw = Number(data?.token?.supply);
  const decimals = Number(data?.token?.decimals);
  if (!Number.isFinite(supplyRaw) || supplyRaw <= 0) return null;
  const supply = Number.isFinite(decimals) ? supplyRaw / 10 ** decimals : supplyRaw;
  if (!Number.isFinite(supply) || supply <= 0) return null;

  let amount = 0;
  let matched = 0;
  for (const net of networks) {
    if (!typeRegex.test(String(net?.type || ""))) continue;
    const tokenAmount = Number(net?.tokenAmount);
    if (!Number.isFinite(tokenAmount)) continue;
    const scaled = Number.isFinite(decimals) ? tokenAmount / 10 ** decimals : tokenAmount;
    amount += scaled;
    matched += 1;
  }
  if (matched === 0) return null;
  return clampPct((amount / supply) * 100);
}

/**
 * Fetch rugcheck signals for a mint and derive the holder-quality metrics the
 * screener filters on. All derived fields are null when rugcheck doesn't
 * provide enough data, so the caller treats "unknown" as "don't filter".
 *
 * Returns:
 *   {
 *     rugged, rug_score,
 *     conviction_score,   // 0-100, higher = safer (inverse of normalized risk)
 *     suspicious_pct,     // % held by flagged insider holders
 *     sniper_pct,         // % held by sniper insider networks (often null)
 *     bundle_pct,         // % held by bundler insider networks (often null)
 *     top10_pct,
 *   }
 */
export async function getRugcheckSignals({ mint } = {}) {
  const empty = {
    rugged: null, rug_score: null, conviction_score: null,
    suspicious_pct: null, sniper_pct: null, bundle_pct: null, top10_pct: null,
  };
  if (!mint) return empty;

  const cached = _cache.get(mint);
  if (cached && Date.now() - cached.at < CACHE_TTL_MS) return cached.signals;

  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  try {
    const res = await fetch(`${RUGCHECK_BASE}/tokens/${mint}/report`, { signal: controller.signal });
    if (!res.ok) {
      // Unknown token / API hiccup — fail open, don't cache the miss
      return empty;
    }
    const data = await res.json();

    const rawScore = Number(data?.score);
    const normalised = Number(data?.score_normalised);
    let convictionScore = null;
    if (data?.rugged) {
      convictionScore = 0;
    } else if (Number.isFinite(normalised)) {
      convictionScore = clampPct(100 - normalised);
    }

    const signals = {
      rugged: data?.rugged ?? null,
      rug_score: Number.isFinite(rawScore) ? rawScore : null,
      conviction_score: convictionScore,
      suspicious_pct: sumPct(data?.topHolders, (h) => h?.insider === true),
      sniper_pct: networkPct(data, /snip/i),
      bundle_pct: networkPct(data, /bundl/i),
      top10_pct: sumPct(
        Array.isArray(data?.topHolders) ? data.topHolders.slice(0, 10) : [],
        () => true,
      ),
    };

    _cache.set(mint, { at: Date.now(), signals });
    return signals;
  } catch (e) {
    if (e?.name !== "AbortError") log("rugcheck", `getRugcheckSignals(${mint?.slice(0, 8)}) failed: ${e.message}`);
    return empty;
  } finally {
    clearTimeout(timer);
  }
}
