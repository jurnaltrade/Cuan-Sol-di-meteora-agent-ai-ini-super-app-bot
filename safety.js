import fs from "fs";
import { log } from "./logger.js";
import { config } from "./config.js";

const SAFETY_FILE = "./safety-state.json";
const LOCK_FILE = "./runtime-lock.json";

function todayKey() {
  return new Date().toISOString().slice(0, 10);
}

function defaultSafetyState() {
  return {
    paused: false,
    pausedAt: null,
    pausedReason: null,
    daily: {},
    consecutiveLosses: 0,
    lastUpdated: null,
  };
}

function loadJson(path, fallback) {
  if (!fs.existsSync(path)) return fallback;
  try {
    return JSON.parse(fs.readFileSync(path, "utf8"));
  } catch (error) {
    log("safety_error", `Failed to read ${path}: ${error.message}`);
    return fallback;
  }
}

function saveJson(path, data) {
  data.lastUpdated = new Date().toISOString();
  fs.writeFileSync(path, JSON.stringify(data, null, 2));
}

function loadState() {
  const state = loadJson(SAFETY_FILE, defaultSafetyState());
  state.daily ||= {};
  state.consecutiveLosses ||= 0;
  return state;
}

function saveState(state) {
  saveJson(SAFETY_FILE, state);
}

function ensureToday(state) {
  const key = todayKey();
  state.daily[key] ||= {
    deployedSol: 0,
    realizedLossUsd: 0,
    deployCount: 0,
    closeCount: 0,
    lastDeployAt: null,
    lastCloseAt: null,
  };
  return state.daily[key];
}

function finiteOrNull(value) {
  const n = Number(value);
  return Number.isFinite(n) ? n : null;
}

export function getSafetyState() {
  const state = loadState();
  return {
    ...state,
    today: ensureToday(state),
    todayKey: todayKey(),
    activeLock: getActionLock(),
  };
}

export function isPaused() {
  return Boolean(loadState().paused);
}

export function setPaused(paused, reason = null) {
  const state = loadState();
  state.paused = Boolean(paused);
  state.pausedAt = paused ? new Date().toISOString() : null;
  state.pausedReason = paused ? reason : null;
  saveState(state);
  log("safety", `${paused ? "Paused" : "Resumed"}${reason ? ` — ${reason}` : ""}`);
  return getSafetyState();
}

export function checkDeployBudget(amountSol) {
  const state = loadState();
  const today = ensureToday(state);
  const amount = finiteOrNull(amountSol);
  if (state.paused) {
    return { pass: false, reason: `Agent is paused${state.pausedReason ? `: ${state.pausedReason}` : ""}. Use /resume to re-enable deploys.` };
  }
  if (amount == null || amount <= 0) {
    return { pass: false, reason: "Deploy amount is invalid for daily budget check." };
  }

  const maxDailyDeploySol = finiteOrNull(config.risk.maxDailyDeploySol);
  if (maxDailyDeploySol != null && maxDailyDeploySol > 0 && today.deployedSol + amount > maxDailyDeploySol) {
    return {
      pass: false,
      reason: `Daily deploy cap reached: ${(today.deployedSol + amount).toFixed(4)} SOL would exceed ${maxDailyDeploySol} SOL/day.`,
    };
  }

  const maxDailyLossUsd = finiteOrNull(config.risk.maxDailyLossUsd);
  if (maxDailyLossUsd != null && maxDailyLossUsd > 0 && today.realizedLossUsd >= maxDailyLossUsd) {
    return {
      pass: false,
      reason: `Daily realized loss cap reached: $${today.realizedLossUsd.toFixed(2)} >= $${maxDailyLossUsd}.`,
    };
  }

  const maxConsecutiveLosses = finiteOrNull(config.risk.maxConsecutiveLosses);
  if (maxConsecutiveLosses != null && maxConsecutiveLosses > 0 && state.consecutiveLosses >= maxConsecutiveLosses) {
    return {
      pass: false,
      reason: `Consecutive loss cap reached: ${state.consecutiveLosses}/${maxConsecutiveLosses}. Pause deploys until reviewed.`,
    };
  }

  return { pass: true };
}

export function recordDeploy(result = {}, args = {}) {
  const amount = finiteOrNull(args.amount_y ?? args.amount_sol ?? result.amount_sol);
  if (amount == null || amount <= 0) return;
  const state = loadState();
  const today = ensureToday(state);
  today.deployedSol = Number((today.deployedSol + amount).toFixed(9));
  today.deployCount += 1;
  today.lastDeployAt = new Date().toISOString();
  saveState(state);
}

export function recordClose(result = {}) {
  const pnlUsd = finiteOrNull(result.pnl_usd);
  if (pnlUsd == null) return;
  const state = loadState();
  const today = ensureToday(state);
  today.closeCount += 1;
  today.lastCloseAt = new Date().toISOString();
  if (pnlUsd < 0) {
    today.realizedLossUsd = Number((today.realizedLossUsd + Math.abs(pnlUsd)).toFixed(6));
    state.consecutiveLosses = (state.consecutiveLosses || 0) + 1;
  } else if (pnlUsd > 0) {
    state.consecutiveLosses = 0;
  }
  saveState(state);
}

export function getActionLock() {
  return loadJson(LOCK_FILE, null);
}

export function acquireActionLock(tool, args = {}) {
  const existing = getActionLock();
  const maxAgeMs = Math.max(1, Number(config.risk.maxActionLockAgeMin ?? 20)) * 60_000;
  const now = Date.now();
  if (existing?.startedAt) {
    const age = now - new Date(existing.startedAt).getTime();
    if (Number.isFinite(age) && age < maxAgeMs) {
      return {
        pass: false,
        reason: `Another write action is in progress (${existing.tool}, started ${existing.startedAt}). Refusing ${tool} to prevent double actions.`,
      };
    }
    log("safety_warn", `Clearing stale runtime lock for ${existing.tool} from ${existing.startedAt}`);
  }

  const lock = {
    tool,
    startedAt: new Date().toISOString(),
    pid: process.pid,
    pool: args.pool_address || null,
    position: args.position_address || null,
  };
  saveJson(LOCK_FILE, lock);
  return { pass: true, lock };
}

export function releaseActionLock(tool) {
  const existing = getActionLock();
  if (!existing) return;
  if (existing.tool && existing.tool !== tool) return;
  try {
    fs.unlinkSync(LOCK_FILE);
  } catch (error) {
    if (error.code !== "ENOENT") log("safety_error", `Failed to clear runtime lock: ${error.message}`);
  }
}

export function formatSafetyStatus() {
  const state = getSafetyState();
  const today = state.today;
  const deployCap = config.risk.maxDailyDeploySol ?? "off";
  const lossCap = config.risk.maxDailyLossUsd ?? "off";
  const lossStreakCap = config.risk.maxConsecutiveLosses ?? "off";
  return [
    `Safety: ${state.paused ? "PAUSED" : "active"}`,
    state.pausedReason ? `Reason: ${state.pausedReason}` : null,
    `Today: ${state.todayKey}`,
    `Deployed: ${today.deployedSol} / ${deployCap} SOL (${today.deployCount} deploys)`,
    `Realized loss: $${today.realizedLossUsd} / $${lossCap}`,
    `Consecutive losses: ${state.consecutiveLosses} / ${lossStreakCap}`,
    state.activeLock ? `Runtime lock: ${state.activeLock.tool} since ${state.activeLock.startedAt}` : "Runtime lock: none",
  ].filter(Boolean).join("\n");
}
