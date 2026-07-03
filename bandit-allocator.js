/**
 * Multi-Armed Bandit Capital Allocator
 * ────────────────────────────────────
 * Instead of deploying every position with one fixed strategy, Meridian
 * runs a small set of "arms" (strategy presets) and uses Thompson
 * Sampling — a Bayesian bandit algorithm — to decide which arm gets the
 * next deploy. Arms that perform well win more capital over time; arms
 * that underperform get starved automatically, without needing a manual
 * `/evolve` threshold rewrite.
 *
 * Each arm keeps a Beta(alpha, beta) posterior over "is this a winning
 * deploy" (pnl_usd > 0). Before each screening deploy, pickArm() draws a
 * random sample from every arm's Beta distribution and returns the arm
 * with the highest sample — arms with more evidence of winning get
 * sampled high more often, but there's always some exploration built in
 * (this is what makes it better than "just use whatever has the best
 * win rate so far", which can get stuck on lucky-but-small samples).
 *
 * Drop-in path: bandit-allocator.js  (repo root, alongside lessons.js)
 */

import fs from "fs";
import { log } from "./logger.js";
import { repoPath } from "./repo-root.js";
import { config } from "./config.js";

const BANDIT_STATE_FILE = repoPath("bandit-state.json");

// ─── Default arms — override via user-config.json -> banditArms: [...] ───
const DEFAULT_ARMS = [
  {
    id: "conservative_spot",
    label: "Conservative (spot, narrow)",
    strategy: "spot",
    binsBelowOffset: -10,     // relative to config.strategy.defaultBinsBelow, clamped to [min,max]
    binsAbove: 0,
    positionSizeMultiplier: 0.8,
  },
  {
    id: "balanced_bidask",
    label: "Balanced (bid_ask, default width)",
    strategy: "bid_ask",
    binsBelowOffset: 0,
    binsAbove: 0,
    positionSizeMultiplier: 1.0,
  },
  {
    id: "aggressive_bidask_wide",
    label: "Aggressive (bid_ask, wide)",
    strategy: "bid_ask",
    binsBelowOffset: 15,
    binsAbove: 10,
    positionSizeMultiplier: 1.1,
  },
  {
    id: "curve_wide",
    label: "Curve (wide, smooth concentration)",
    strategy: "curve",
    binsBelowOffset: 10,
    binsAbove: 5,
    positionSizeMultiplier: 0.9,
  },
];

function getArms() {
  const custom = config.banditArms;
  return Array.isArray(custom) && custom.length ? custom : DEFAULT_ARMS;
}

function load() {
  if (!fs.existsSync(BANDIT_STATE_FILE)) return { arms: {} };
  try {
    return JSON.parse(fs.readFileSync(BANDIT_STATE_FILE, "utf8"));
  } catch {
    return { arms: {} };
  }
}

function save(data) {
  fs.writeFileSync(BANDIT_STATE_FILE, JSON.stringify(data, null, 2));
}

function ensureArmState(data, armId) {
  if (!data.arms[armId]) {
    // Beta(1,1) = uniform prior — no bias toward any arm before evidence exists
    data.arms[armId] = { alpha: 1, beta: 1, deploys: 0, wins: 0, total_pnl_usd: 0 };
  }
  return data.arms[armId];
}

// Sample from Beta(alpha, beta) using two Gamma draws (Marsaglia-Tsang).
function sampleGamma(shape) {
  if (shape < 1) {
    const u = Math.random();
    return sampleGamma(1 + shape) * Math.pow(u, 1 / shape);
  }
  const d = shape - 1 / 3;
  const c = 1 / Math.sqrt(9 * d);
  while (true) {
    let x, v;
    do {
      x = gaussian();
      v = 1 + c * x;
    } while (v <= 0);
    v = v * v * v;
    const u = Math.random();
    if (u < 1 - 0.0331 * x * x * x * x) return d * v;
    if (Math.log(u) < 0.5 * x * x + d * (1 - v + Math.log(v))) return d * v;
  }
}

function gaussian() {
  // Box-Muller
  const u1 = Math.random() || 1e-9;
  const u2 = Math.random();
  return Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
}

function sampleBeta(alpha, beta) {
  const x = sampleGamma(alpha);
  const y = sampleGamma(beta);
  return x / (x + y);
}

/**
 * Tool: pick_bandit_arm
 * Call before deploy_position in the screening cycle. Returns the arm
 * to use plus concrete bins_below/bins_above/strategy/size_multiplier
 * the agent should pass into deploy_position.
 */
export function pickArm() {
  const arms = getArms();
  const data = load();

  const draws = arms.map((arm) => {
    const state = ensureArmState(data, arm.id);
    const sample = sampleBeta(state.alpha, state.beta);
    return { arm, state, sample };
  });

  save(data); // persist any newly-initialized arm states

  draws.sort((a, b) => b.sample - a.sample);
  const chosen = draws[0];

  const s = config.strategy;
  const binsBelow = Math.max(
    s.minBinsBelow,
    Math.min(s.maxBinsBelow, s.defaultBinsBelow + (chosen.arm.binsBelowOffset || 0))
  );

  return {
    arm_id: chosen.arm.id,
    label: chosen.arm.label,
    strategy: chosen.arm.strategy,
    bins_below: binsBelow,
    bins_above: chosen.arm.binsAbove || 0,
    position_size_multiplier: chosen.arm.positionSizeMultiplier ?? 1.0,
    thompson_sample: Math.round(chosen.sample * 1000) / 1000,
    all_arms_ranked: draws.map((d) => ({
      arm_id: d.arm.id,
      sample: Math.round(d.sample * 1000) / 1000,
      deploys: d.state.deploys,
      win_rate_pct: d.state.deploys > 0 ? Math.round((d.state.wins / d.state.deploys) * 100) : null,
      avg_pnl_usd: d.state.deploys > 0 ? Math.round((d.state.total_pnl_usd / d.state.deploys) * 100) / 100 : null,
    })),
  };
}

/**
 * Tool: record_bandit_outcome
 * Call when a position tagged with an arm_id closes (hook this into
 * lessons.js#recordPerformance, or call directly from executor.js right
 * after close_position resolves). Updates the arm's Beta posterior.
 */
export function recordArmOutcome({ arm_id, pnl_usd }) {
  if (!arm_id) return { skipped: true, reason: "No arm_id tagged on this position." };

  const data = load();
  const state = ensureArmState(data, arm_id);
  const win = Number(pnl_usd) > 0;

  state.deploys += 1;
  state.total_pnl_usd = Math.round((state.total_pnl_usd + Number(pnl_usd || 0)) * 100) / 100;
  if (win) {
    state.alpha += 1;
    state.wins += 1;
  } else {
    state.beta += 1;
  }

  save(data);
  log("bandit", `Arm ${arm_id}: ${win ? "WIN" : "LOSS"} (pnl=$${pnl_usd}) -> alpha=${state.alpha} beta=${state.beta}`);
  return { arm_id, updated: state };
}

/**
 * Tool: get_bandit_summary
 */
export function getBanditSummary() {
  const arms = getArms();
  const data = load();
  return {
    arms: arms.map((arm) => {
      const state = ensureArmState(data, arm.id);
      return {
        id: arm.id,
        label: arm.label,
        deploys: state.deploys,
        win_rate_pct: state.deploys > 0 ? Math.round((state.wins / state.deploys) * 100) : null,
        avg_pnl_usd: state.deploys > 0 ? Math.round((state.total_pnl_usd / state.deploys) * 100) / 100 : null,
        posterior: { alpha: state.alpha, beta: state.beta },
        expected_win_rate_pct: Math.round((state.alpha / (state.alpha + state.beta)) * 100),
      };
    }),
  };
}
