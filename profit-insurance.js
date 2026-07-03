/**
 * Profit Insurance Buffer
 * ───────────────────────
 * Every time claim_fees succeeds, a configurable % of the claimed amount
 * is set aside into a separate tracked "insurance fund" instead of
 * staying fully in the working trading balance. Over time this becomes
 * a self-funded bankroll buffer: when a position is about to get closed
 * on a stop-loss, the agent can pull from the buffer to cushion the hit
 * (or simply leave it accumulating as a withdrawal-ready safety net).
 *
 * Two modes, controlled by config.insurance.walletAddress:
 *   - TRACKING ONLY (default, walletAddress = null): nothing actually
 *     moves on-chain — this just keeps an accurate ledger so you know
 *     how much of your "trading" balance is really earmarked as reserve.
 *     Safe to enable immediately, zero execution risk.
 *   - REAL SEGREGATION (walletAddress set to a second wallet you own):
 *     each contribution actually swaps the base token to SOL (reusing
 *     tools/wallet.js#swapToken) and transfers SOL to that wallet, using
 *     real custody separation. This uses the existing swap infra only —
 *     no new external SDK risk — but transfers are a NEW code path, so
 *     it's gated behind DRY_RUN like every other write action in this repo.
 *
 * Drop-in path: profit-insurance.js (repo root, alongside lessons.js)
 */

import fs from "fs";
import { log } from "./logger.js";
import { repoPath } from "./repo-root.js";
import { config } from "./config.js";

const INSURANCE_FILE = repoPath("insurance-fund.json");

function load() {
  if (!fs.existsSync(INSURANCE_FILE)) {
    return { balance_sol: 0, contributions: [], withdrawals: [] };
  }
  try {
    return JSON.parse(fs.readFileSync(INSURANCE_FILE, "utf8"));
  } catch {
    return { balance_sol: 0, contributions: [], withdrawals: [] };
  }
}

function save(data) {
  fs.writeFileSync(INSURANCE_FILE, JSON.stringify(data, null, 2));
}

function insuranceConfig() {
  const u = config.insurance || {};
  return {
    enabled: u.enabled ?? true,
    reservePct: u.reservePct ?? 15,          // % of each fee claim set aside
    walletAddress: u.walletAddress ?? null,  // set to enable real on-chain segregation
    maxBufferSol: u.maxBufferSol ?? null,    // optional cap — extra goes back to trading balance
  };
}

/**
 * Call this right after claim_fees resolves successfully.
 * amountSol should be the SOL-equivalent value of what was claimed
 * (post any auto-swap-to-SOL, if that's enabled).
 */
export async function contributeFromClaim({ position_address, pool_name, amount_sol }) {
  const cfg = insuranceConfig();
  if (!cfg.enabled || !amount_sol || amount_sol <= 0) {
    return { skipped: true, reason: "Insurance disabled or no claimable amount." };
  }

  const reserveAmount = Math.round(amount_sol * (cfg.reservePct / 100) * 1e6) / 1e6;
  if (reserveAmount <= 0) return { skipped: true, reason: "Computed reserve amount is zero." };

  const data = load();

  if (cfg.maxBufferSol != null && data.balance_sol >= cfg.maxBufferSol) {
    return { skipped: true, reason: `Insurance buffer already at cap (${cfg.maxBufferSol} SOL).` };
  }

  let onChain = { moved: false };
  if (cfg.walletAddress) {
    try {
      onChain = await _transferToInsuranceWallet(reserveAmount, cfg.walletAddress);
    } catch (error) {
      log("insurance_error", `Failed to transfer reserve to insurance wallet: ${error.message}`);
      return { success: false, error: error.message };
    }
  }

  data.balance_sol = Math.round((data.balance_sol + reserveAmount) * 1e6) / 1e6;
  data.contributions.unshift({
    ts: new Date().toISOString(),
    position_address,
    pool_name,
    claimed_sol: amount_sol,
    reserved_sol: reserveAmount,
    on_chain: onChain.moved,
  });
  data.contributions = data.contributions.slice(0, 200);
  save(data);

  log("insurance", `Reserved ${reserveAmount} SOL (${cfg.reservePct}% of ${amount_sol}) from ${pool_name || position_address}. Buffer: ${data.balance_sol} SOL`);
  return { success: true, reserved_sol: reserveAmount, buffer_balance_sol: data.balance_sol, on_chain: onChain.moved };
}

/**
 * Optional real transfer path. Reuses the existing swap infra in
 * tools/wallet.js so no new dependency is introduced. If the reserve
 * amount is already SOL, this is a native transfer; if it's still a
 * base token at call time, swap first via swapToken() before calling this.
 */
async function _transferToInsuranceWallet(amountSol, destinationAddress) {
  if (process.env.DRY_RUN === "true") {
    return { moved: false, dry_run: true, would_send_sol: amountSol, to: destinationAddress };
  }
  const { Connection, PublicKey, SystemProgram, Transaction, sendAndConfirmTransaction } = await import("@solana/web3.js");
  const { getWallet, getConnection } = await import("./tools/wallet.js").then((m) => ({
    getWallet: m.getWallet,
    getConnection: m.getConnection,
  })).catch(() => ({ getWallet: null, getConnection: null }));

  if (!getWallet || !getConnection) {
    throw new Error("getWallet/getConnection are not exported from tools/wallet.js — export them or adapt this transfer call.");
  }

  const wallet = getWallet();
  const connection = getConnection();
  const tx = new Transaction().add(
    SystemProgram.transfer({
      fromPubkey: wallet.publicKey,
      toPubkey: new PublicKey(destinationAddress),
      lamports: Math.round(amountSol * 1e9),
    })
  );
  const sig = await sendAndConfirmTransaction(connection, tx, [wallet]);
  return { moved: true, tx: sig };
}

/**
 * Tool: insurance_withdraw
 * Manual (or agent-triggered) withdrawal from the buffer — e.g. to top
 * up gas reserve, or to absorb part of a stop-loss. Tracking-mode only
 * adjusts the ledger; real-segregation mode still requires you to move
 * funds back yourself (intentionally — no automatic outbound transfers
 * from the safety wallet without a human step, by design).
 */
export function insuranceWithdraw({ amount_sol, reason }) {
  const data = load();
  if (amount_sol > data.balance_sol) {
    return { success: false, error: `Requested ${amount_sol} SOL exceeds buffer balance (${data.balance_sol} SOL).` };
  }
  data.balance_sol = Math.round((data.balance_sol - amount_sol) * 1e6) / 1e6;
  data.withdrawals.unshift({ ts: new Date().toISOString(), amount_sol, reason: reason || null });
  data.withdrawals = data.withdrawals.slice(0, 200);
  save(data);

  log("insurance", `Withdrew ${amount_sol} SOL from buffer (${reason || "no reason given"}). Remaining: ${data.balance_sol} SOL`);
  return { success: true, withdrawn_sol: amount_sol, buffer_balance_sol: data.balance_sol };
}

/**
 * Tool: get_insurance_status
 */
export function getInsuranceStatus() {
  const cfg = insuranceConfig();
  const data = load();
  return {
    enabled: cfg.enabled,
    reserve_pct: cfg.reservePct,
    mode: cfg.walletAddress ? "real_segregation" : "tracking_only",
    buffer_balance_sol: data.balance_sol,
    total_contributions: data.contributions.length,
    total_withdrawals: data.withdrawals.length,
    recent_contributions: data.contributions.slice(0, 5),
    recent_withdrawals: data.withdrawals.slice(0, 5),
  };
}
