/**
 * Portfolio Greeks Aggregator (Delta / Gamma exposure)
 * ─────────────────────────────────────────────────────
 * A Meteora DLMM position behaves like a short-gamma option position:
 * as price moves through your range, your holdings mechanically shift
 * from 100% base-token (below range) to 100% quote-token (above range).
 * That's exactly the payoff shape of a covered-call-like structure —
 * you're implicitly SHORT volatility on every position you hold.
 *
 * hedge-overlay.js (built earlier) hedges ONE position at a time. This
 * module goes one level up: it looks at ALL open positions together and
 * computes portfolio-level "Greeks":
 *
 *   - delta_usd  : net dollar directional exposure to the base tokens
 *                  you're currently holding, signed (+ = net long, i.e.
 *                  you'd lose money in aggregate if prices dropped).
 *   - gamma_proxy: how fast that delta will shift as prices move — driven
 *                  by how NARROW your ranges are. Tight ranges = high
 *                  gamma = your exposure flips fast and violently.
 *   - concentration: how much of your delta sits in tokens that likely
 *                  move together (rough proxy, see note below).
 *
 * This lets you hedge the PORTFOLIO's net exposure with one right-sized
 * position instead of hedging every LP individually (much more capital
 * efficient — individual hedges can literally cancel each other out).
 *
 * Drop-in path: portfolio-greeks.js (repo root)
 */

import { log } from "./logger.js";
import { getMyPositions } from "./tools/dlmm.js";
import { config } from "./config.js";

/**
 * Per-position delta proxy.
 * t = where the active bin sits within [lower_bin, upper_bin], 0..1
 *   t = 0   -> active bin at the very bottom of the range -> position is
 *              ~100% base token -> fully exposed to base token dropping
 *              in USD terms even though nothing has "moved" yet -> delta = +1
 *   t = 1   -> active bin at the very top -> ~100% quote token -> delta = -1
 * (Signed so that +delta = long-base = hurts on a base-token price drop.)
 */
function positionDelta(pos) {
  const { lower_bin, upper_bin, active_bin } = pos;
  if (lower_bin == null || upper_bin == null || active_bin == null || upper_bin === lower_bin) {
    return 0; // insufficient data — treat as neutral rather than guessing
  }
  const t = Math.min(1, Math.max(0, (active_bin - lower_bin) / (upper_bin - lower_bin)));
  return 1 - 2 * t; // +1 (all base) .. -1 (all quote)
}

/**
 * Per-position gamma proxy: inversely proportional to range width in
 * bins. A 5-bin-wide position has ~10x the "delta velocity" of a
 * 50-bin-wide position for the same price move. Not a real options
 * gamma (no time/vol scaling), but directionally correct and enough
 * to rank positions by concentration risk.
 */
function positionGammaProxy(pos) {
  const { lower_bin, upper_bin } = pos;
  const width = upper_bin != null && lower_bin != null ? Math.max(1, upper_bin - lower_bin) : null;
  if (!width) return 0;
  return 1 / width;
}

/**
 * Tool: get_portfolio_greeks
 * Call once per management cycle (cheap — reuses the getMyPositions
 * cache already used elsewhere in the agent).
 */
export async function getPortfolioGreeks() {
  const { positions } = await getMyPositions({});
  if (!positions || positions.length === 0) {
    return {
      total_positions: 0,
      net_delta_usd: 0,
      gamma_score: 0,
      stance: "FLAT",
      positions: [],
    };
  }

  let netDeltaUsd = 0;
  let weightedGamma = 0;
  let totalValueUsd = 0;
  const byBaseMint = new Map(); // for the correlation-concentration proxy

  const perPosition = positions.map((pos) => {
    const value = Number(pos.total_value_true_usd ?? pos.total_value_usd ?? 0);
    const delta = positionDelta(pos);
    const gamma = positionGammaProxy(pos);
    const deltaUsd = delta * value;

    netDeltaUsd += deltaUsd;
    weightedGamma += gamma * value;
    totalValueUsd += value;

    const mintKey = pos.base_mint || pos.pair || pos.pool;
    byBaseMint.set(mintKey, (byBaseMint.get(mintKey) || 0) + Math.abs(deltaUsd));

    return {
      position: pos.position,
      pair: pos.pair,
      value_usd: Math.round(value * 100) / 100,
      delta: Math.round(delta * 1000) / 1000,
      delta_usd: Math.round(deltaUsd * 100) / 100,
      gamma_proxy: Math.round(gamma * 1000) / 1000,
      in_range: pos.in_range,
    };
  });

  const gammaScore = totalValueUsd > 0 ? weightedGamma / totalValueUsd : 0;

  // Simple concentration proxy: what % of total |delta| comes from the
  // single largest base-token exposure. High = you're effectively making
  // one big directional bet spread across several "different" pools.
  let largestExposure = 0;
  for (const v of byBaseMint.values()) largestExposure = Math.max(largestExposure, v);
  const totalAbsDelta = perPosition.reduce((s, p) => s + Math.abs(p.delta_usd), 0);
  const concentrationPct = totalAbsDelta > 0 ? Math.round((largestExposure / totalAbsDelta) * 100) : 0;

  const netDeltaPctOfPortfolio = totalValueUsd > 0 ? (netDeltaUsd / totalValueUsd) * 100 : 0;

  let stance = "BALANCED";
  if (netDeltaPctOfPortfolio > 25) stance = "NET_LONG_BASE";
  else if (netDeltaPctOfPortfolio < -25) stance = "NET_LONG_QUOTE";

  const gammaRisk = gammaScore > 0.08 ? "HIGH" : gammaScore > 0.03 ? "MEDIUM" : "LOW";

  const result = {
    total_positions: positions.length,
    total_value_usd: Math.round(totalValueUsd * 100) / 100,
    net_delta_usd: Math.round(netDeltaUsd * 100) / 100,
    net_delta_pct_of_portfolio: Math.round(netDeltaPctOfPortfolio * 10) / 10,
    gamma_score: Math.round(gammaScore * 1000) / 1000,
    gamma_risk: gammaRisk,
    concentration_pct: concentrationPct,
    stance,
    positions: perPosition,
    recommendation: buildRecommendation({ netDeltaPctOfPortfolio, gammaRisk, concentrationPct }),
  };

  log("portfolio_greeks", `net_delta=${result.net_delta_pct_of_portfolio}% gamma=${gammaRisk} concentration=${concentrationPct}% stance=${stance}`);
  return result;
}

function buildRecommendation({ netDeltaPctOfPortfolio, gammaRisk, concentrationPct }) {
  const notes = [];
  if (Math.abs(netDeltaPctOfPortfolio) > 25) {
    notes.push(
      netDeltaPctOfPortfolio > 0
        ? "Portfolio is net-long base tokens — a broad market drop hits every position at once. Consider a single portfolio-level hedge sized to net_delta_usd instead of individual per-position hedges."
        : "Portfolio is net-long quote (mostly SOL/stables held) — upside in base tokens is being missed across positions; consider whether ranges are too wide/stale."
    );
  }
  if (gammaRisk === "HIGH") {
    notes.push("Aggregate gamma is high — several positions have narrow ranges, so total delta can flip fast on a sharp move. Consider widening ranges on the tightest positions or reducing count of concurrent tight positions.");
  }
  if (concentrationPct > 60) {
    notes.push(`${concentrationPct}% of total directional exposure sits in a single token/pool — this isn't real diversification even if position count looks high. Prefer deploying new capital into a less-correlated base token.`);
  }
  if (notes.length === 0) notes.push("Portfolio exposure looks balanced — no action needed.");
  return notes;
}

/**
 * Optional: size a single portfolio-level hedge using the existing
 * hedge-overlay.js infrastructure, targeting net_delta_usd instead of
 * one position's notional. Call this instead of per-position openHedge
 * if you want portfolio-level hedging only.
 */
export async function getPortfolioHedgeSizing() {
  const greeks = await getPortfolioGreeks();
  const cfg = config.hedge || {};
  const hedgeRatio = cfg.hedgeRatio ?? 0.5;
  const maxNotional = cfg.maxHedgeNotionalUsd ?? 200;

  const targetHedgeUsd = Math.min(Math.abs(greeks.net_delta_usd) * hedgeRatio, maxNotional);
  return {
    net_delta_usd: greeks.net_delta_usd,
    suggested_hedge_notional_usd: Math.round(targetHedgeUsd * 100) / 100,
    direction: greeks.net_delta_usd > 0 ? "SHORT base-token basket" : "LONG base-token basket (or reduce existing short)",
    note: "Feed suggested_hedge_notional_usd into hedge-overlay.js#openHedge with position_address = 'PORTFOLIO_AGGREGATE' to hedge at the portfolio level instead of per-position.",
  };
}
