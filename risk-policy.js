import { config } from "./config.js";

function numeric(value, fallback = 0) {
  if (value == null || value === "") return fallback;
  const n = Number(value);
  return Number.isFinite(n) ? n : fallback;
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

function scoreLinear(value, min, max, points) {
  const n = numeric(value, null);
  if (n == null) return 0;
  if (n <= min) return 0;
  if (n >= max) return points;
  return ((n - min) / (max - min)) * points;
}

export function getRiskMode() {
  const mode = String(config.risk.riskMode || "stable").toLowerCase();
  return ["stable", "balanced", "aggressive"].includes(mode) ? mode : "stable";
}

export function getModeDefaults(mode = getRiskMode()) {
  const modes = {
    stable: {
      minEntryScore: 78,
      watchScore: 68,
      normalSizeScore: 88,
      maxSizeScore: 95,
      minSizeMultiplier: 0.35,
      normalSizeMultiplier: 0.75,
      maxSizeMultiplier: 1.0,
      lossSizeMultiplier: 0.5,
    },
    balanced: {
      minEntryScore: 70,
      watchScore: 60,
      normalSizeScore: 82,
      maxSizeScore: 92,
      minSizeMultiplier: 0.5,
      normalSizeMultiplier: 1.0,
      maxSizeMultiplier: 1.1,
      lossSizeMultiplier: 0.7,
    },
    aggressive: {
      minEntryScore: 62,
      watchScore: 52,
      normalSizeScore: 76,
      maxSizeScore: 90,
      minSizeMultiplier: 0.75,
      normalSizeMultiplier: 1.0,
      maxSizeMultiplier: 1.25,
      lossSizeMultiplier: 0.85,
    },
  };
  return modes[mode] || modes.stable;
}

export function getEntryPolicy() {
  const defaults = getModeDefaults();
  return {
    minEntryScore: numeric(config.risk.minEntryScore, defaults.minEntryScore),
    watchScore: numeric(config.risk.watchScore, defaults.watchScore),
    normalSizeScore: numeric(config.risk.normalSizeScore, defaults.normalSizeScore),
    maxSizeScore: numeric(config.risk.maxSizeScore, defaults.maxSizeScore),
    minSizeMultiplier: numeric(config.risk.minSizeMultiplier, defaults.minSizeMultiplier),
    normalSizeMultiplier: numeric(config.risk.normalSizeMultiplier, defaults.normalSizeMultiplier),
    maxSizeMultiplier: numeric(config.risk.maxSizeMultiplier, defaults.maxSizeMultiplier),
    lossSizeMultiplier: numeric(config.risk.lossSizeMultiplier, defaults.lossSizeMultiplier),
  };
}

export function scoreEntryCandidate(pool = {}) {
  const reasons = [];
  const penalties = [];
  let score = 0;

  const feeActiveTvl = numeric(pool.fee_active_tvl_ratio, 0);
  const organic = numeric(pool.organic_score ?? pool.base?.organic, 0);
  const quoteOrganic = numeric(pool.quote?.organic ?? pool.quote_organic_score, organic);
  const volume = numeric(pool.volume_window ?? pool.volume_24h ?? pool.volume, 0);
  const activeTvl = numeric(pool.active_tvl ?? pool.tvl, 0);
  const holders = numeric(pool.holders ?? pool.base_token_holders, 0);
  const activePct = numeric(pool.active_pct ?? pool.active_positions_pct, 0);
  const tokenFees = numeric(pool.global_fees_sol ?? pool.token?.global_fees_sol ?? pool.gmgn_total_fee_sol, 0);
  const top10Pct = numeric(pool.top10_pct ?? pool.token?.audit?.top10_pct ?? pool.token?.audit?.top_holders_pct ?? pool.gmgn_top10_holder_pct, null);
  const botPct = numeric(pool.bot_holders_pct ?? pool.token?.audit?.bots_pct ?? pool.token?.audit?.bot_holders_pct ?? pool.gmgn_bot_degen_pct, null);
  const bundlePct = numeric(pool.bundle_pct, null);
  const volatility = numeric(pool.volatility, 0);
  const ageHours = numeric(pool.token_age_hours, null);

  score += scoreLinear(feeActiveTvl, config.screening.minFeeActiveTvlRatio, Math.max(0.3, config.screening.minFeeActiveTvlRatio * 6), 22);
  score += scoreLinear(organic, config.screening.minOrganic, 95, 18);
  score += scoreLinear(quoteOrganic, config.screening.minQuoteOrganic, 95, 8);
  score += scoreLinear(volume, config.screening.minVolume, Math.max(config.screening.minVolume * 8, 5_000), 12);
  score += scoreLinear(activeTvl, config.screening.minTvl, Math.max(config.screening.minTvl * 5, 50_000), 10);
  score += scoreLinear(holders, config.screening.minHolders, Math.max(config.screening.minHolders * 4, 2_500), 8);
  score += scoreLinear(activePct, 20, 70, 7);
  score += scoreLinear(tokenFees, config.screening.minTokenFeesSol, Math.max(config.screening.minTokenFeesSol * 4, 120), 8);

  if (pool.discord_signal) { score += 3; reasons.push("discord signal"); }
  if (pool.kol_in_clusters) { score += 2; reasons.push("KOL cluster"); }
  if (pool.smart_wallets?.length || numeric(pool.gmgn_smart_wallets, 0) > 0) { score += 4; reasons.push("smart wallets"); }

  if (pool.is_wash) penalties.push({ points: 100, reason: "wash trading flagged" });
  if (pool.is_rugpull) penalties.push({ points: 35, reason: "rugpull risk flagged" });
  if (pool.is_pvp) penalties.push({ points: 18, reason: "PVP symbol conflict" });
  if (pool.dev_sold_all) penalties.push({ points: 15, reason: "dev sold all" });
  if (top10Pct != null && top10Pct > config.screening.maxTop10Pct) penalties.push({ points: 22, reason: `top10 ${top10Pct}% > ${config.screening.maxTop10Pct}%` });
  if (botPct != null && botPct > config.screening.maxBotHoldersPct) penalties.push({ points: 22, reason: `bot holders ${botPct}% > ${config.screening.maxBotHoldersPct}%` });
  if (bundlePct != null && bundlePct > config.screening.maxBundlePct) penalties.push({ points: 14, reason: `bundle ${bundlePct}% > ${config.screening.maxBundlePct}%` });
  if (ageHours != null && ageHours < 1 && getRiskMode() === "stable") penalties.push({ points: 8, reason: "token younger than 1h in stable mode" });
  if (volatility <= 0) penalties.push({ points: 100, reason: "invalid volatility" });
  if (volatility > 8 && getRiskMode() === "stable") penalties.push({ points: 10, reason: "extreme volatility in stable mode" });

  for (const p of penalties) score -= p.points;

  const finalScore = Math.round(clamp(score, 0, 100));
  const policy = getEntryPolicy();
  const band = finalScore >= policy.maxSizeScore
    ? "max"
    : finalScore >= policy.normalSizeScore
      ? "normal"
      : finalScore >= policy.minEntryScore
        ? "small"
        : finalScore >= policy.watchScore
          ? "watch"
          : "reject";

  const rejectReason = band === "reject" || band === "watch"
    ? `entry score ${finalScore} below deploy threshold ${policy.minEntryScore}`
    : null;

  return {
    score: finalScore,
    band,
    pass: finalScore >= policy.minEntryScore,
    rejectReason,
    reasons,
    penalties: penalties.map((p) => p.reason),
  };
}

export function computePositionSize(baseAmountSol, entryScore, consecutiveLosses = 0) {
  const policy = getEntryPolicy();
  const score = numeric(entryScore, 0);
  let multiplier;
  if (score >= policy.maxSizeScore) multiplier = policy.maxSizeMultiplier;
  else if (score >= policy.normalSizeScore) multiplier = policy.normalSizeMultiplier;
  else multiplier = policy.minSizeMultiplier;

  if (consecutiveLosses > 0) {
    multiplier *= Math.pow(policy.lossSizeMultiplier, consecutiveLosses);
  }

  const amount = numeric(baseAmountSol, 0) * clamp(multiplier, 0.05, 2);
  return Number(Math.max(0, amount).toFixed(3));
}
