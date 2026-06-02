# Meridian — Findings & Issues (Draft)

_Paper validation run on VPS (`simulator-paper` branch, DRY_RUN). Compiled 2026-06-02._
_Goal: document issues found while paper-trading so they can be fixed/tested with data, not guesses._

---

## Current data snapshot (evidence)

```
Sample: 5 closed / 2 open  — Win/Loss: 0 / 5
Net PnL: -$1.17 · Fees: $0.08 · Avg L: -0.83% · Worst: -1.22%
Exit breakdown: OOR 3 (avg -0.89%) · STALE 2 (avg -0.72%)
Open: GACHA-SOL (in-range 0%, 90m) · GACHA-USDC (in-range 0%, 51m)
```

**Headline:** every position so far has **in-range ~0–9% and ~$0 fees** — including clean/mature pools (three-SOL $3M mcap/182 SOL fees, GACHA 14% bots/91 organic). Losses are small & controlled (exit rules work), but **zero fee capture**.

---

## Issues

| # | Severity | Issue | Status |
|---|---|---|---|
| 1 | 🔴 High | Single-sided bid_ask downside → in-range ~0% → $0 fees (root cause of all losses) | Open — needs decision |
| 2 | 🔴 High | Screening picks non-SOL-quote pools (e.g. GACHA-USDC); deploy assumes SOL | Must-fix before LIVE |
| 3 | 🟡 Med | Strategy locked to `bid_ask` (curve/spot blocked by prompt + executor) | Open |
| 4 | 🟡 Med | Bin step screened 80–125; meme guides recommend 250–400 → fast OOR | Open |
| 5 | 🟡 Med | Thin candidate funnel on `meteora` source (no GMGN key) | Open |
| 6 | 🟡 Med | Paper bridge dedup is pool-level only (held GACHA-SOL + GACHA-USDC = 2x same base) | Open |
| 7 | 🟡 Low | Race condition: tickPaperPositions (async) vs bridge openPaperPosition can drop a trade (~1%) | Open |
| 8 | 🟢 Cosmetic | Duplicate "Deployed" Telegram notifs + report≠paper when bridge skips | Open |
| 9 | 🟢 Cosmetic | `editMessageText 400: message not modified` (live progress edit) | Open |
| 10 | 🟢 Cosmetic | "undefined eligible from N screened" (simulator branch lacks main's fix) | Open |
| 11 | ✅ Resolved | Default model `openrouter/healer-alpha` retired (404) | Fixed → deepseek-v4-flash |

---

## Detail

### 🔴 #1 — Structural: single-sided bid_ask downside earns ~0 fees
- **What:** Deploy = single-sided SOL, bins below only (`bins_above=0`). Position sits at the TOP edge of its range (upper bound = active bin at deploy). Any upward price move → instantly OOR-up → $0 fees. Only earns when price FALLS into the bins.
- **Evidence:** 7 positions, all in-range 0–9%, ~$0 fees — even clean/mature pools. Not token-dependent → structural.
- **Impact:** No fee capture. Strategy behaves as "DCA buy-the-dip", NOT fee-farming. Edge depends on tokens dipping-then-recovering; current tokens pump up / chop.
- **Proposed (test later):** allow centered shapes (curve/spot, or `bins_above > 0` two-sided) → stay in-range → earn fees. Community proof: Kinji `curve` +8.43%.

### 🔴 #2 — Non-SOL-quote pools selected
- **What:** `screening.js` checks quote organic/warnings but has NO "quote == SOL" filter. So USDC-quote pools (GACHA-USDC) pass. Deploy uses `amount_y` = quote token; for a USDC pool that means USDC, not SOL — but logic/report assume SOL.
- **Impact:** Paper = harmless (bridge uses USD). **LIVE = real bug** (deploys wrong token / wallet lacks USDC).
- **Proposed:** add SOL-quote-only filter to screening before going live.

### 🟡 #3 — Strategy locked to bid_ask
- Prompt: "strategy = bid_ask, always use this, never change." Executor enforces single-side SOL + `bins_above=0`. Cannot use curve/spot without code/config changes.

### 🟡 #4 — Bin step too narrow for memes
- Config screens `minBinStep 80 / maxBinStep 125`. Practitioner guides: 250–400 for volatile memes (wider price coverage → stays in range). Narrow bins → fast OOR.

### 🟡 #5 — Thin funnel (meteora source)
- On `screeningSource: meteora` + Jupiter bot metric (`maxBotHoldersPct 30`). Many trending pools are botty (Magpie 42%) → few candidates. Community uses `screeningSource: gmgn` (different universe + bot metric) for more flow — requires GMGN_API_KEY (not set).

### 🟡 #6 — Bridge dedup is pool-level
- Bridge skips duplicate POOL, but not duplicate BASE TOKEN. Held GACHA-SOL + GACHA-USDC simultaneously (both base = GACHA). Live executor has base-mint dedup; paper bridge doesn't.

### 🟡 #7 — Race condition (low prob)
- `tickPaperPositions` holds stale `state` across `await fetchNewCandles`; if bridge `openPaperPosition` saves during that window, tick's save can overwrite it → trade dropped (~1%/deploy). `evaluatePaperExits` is sync (safe).

### 🟢 #8–10 — Cosmetic
- Dup deploy notifs; editMessageText 400; "undefined eligible" label. None affect behavior/data.

---

## Branch / config notes (confirmed)

- **GMGN screening** (`screeningSource: meteora|gmgn`, `gmgn-config.json`, `tools/gmgn.js`) exists on **experimental + simulator** branches, NOT on `main`. (Meridian Helper bot answers from `main` context → says "no GMGN" — correct for main only.)
- Experimental extras: `repeatDeployCooldown`, `emergencyPriceDropPct`, `gmgnRequireKol`.
- Agent names: "Hunter Alpha" (screener) / "Healer Alpha" (manager) = also the retired OpenRouter stealth-model aliases.

## Custom additions on `simulator-paper` (for record)
- Paper bridge: dry-run deploy → `openPaperPosition` (executor.js) — covers cron/auto/manual
- `evaluatePaperExits`: SL / TP / trailing / OOR / **STALE dead-money** cull
- `paper-stats.mjs`: Charonica-style validation gate + live open-position view
- Provider routing (pin DeepSeek), Telegram topic (thread 1398)

---

## Open questions / next steps
1. After ~15–20 closed trades: if in-range stays ~0% → confirm structural → decide on curve/spot or two-sided.
2. Get GMGN_API_KEY → A/B test `gmgn` vs `meteora` source.
3. Add SOL-quote filter **before any live trading**.
4. Decide: keep Meridian's "DCA buy-the-dip" thesis, or reshape toward fee-farming (centered ranges).
