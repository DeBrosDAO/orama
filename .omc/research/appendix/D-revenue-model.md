# Appendix D — Revenue model assumptions

Draft, 3 October 2026. All amounts are in US dollars.

This appendix shows how the business makes money, the assumptions behind each revenue line, and three scenarios at month 18 and month 36 after funding. These are planning scenarios, not forecasts. Every input is stated so it can be challenged. Market benchmarks are sourced in the research file (pitch-deck-research.md); every number not taken from a source is marked as **our assumption**.

---

## 1. How the company earns

The company owns no data centres. Developers and operators pay for their own nodes, and the network pays operators for the capacity they provide. The company earns from the products built around the network:

| Line | Starts | What is charged |
|---|---|---|
| RootWallet subscriptions | Month 3 | Premium and Pro plans for the vault and developer features (passwords, two-factor codes, SSH keys, the desktop agent) |
| RootWallet swap fees | Month 3 | A fee on in-app swaps and bridges |
| RootWallet on-ramp and staking | Month 6 onward | Revenue share from card on-ramp partners; commission on staking through the wallet |
| Orama One hardware | Pre-orders from month 9, shipping month 12 onward | Sale of the plug-in node (two editions) |
| Compute marketplace | Month 18 onward | A share of what developers pay to rent capacity on other operators' nodes |
| Network participation | Mainnet | The company runs nodes and validators from genesis and earns rewards like any other operator. The base-fee destination is an open decision (Appendix B). Not included in the scenarios below. |

The first two lines are software revenue on products that already exist. The wallet and the network ship together, so every Orama developer, operator and end user is a wallet user too.

---

## 2. Assumptions by revenue line

### 2.1 RootWallet subscriptions

- **Plans.** The free plan is a full multi-chain wallet. Premium adds the vault: passwords, two-factor codes and SSH keys. Pro adds advanced features for developers and power users. Prices are not set yet.
- **Assumed prices (our assumption):** Premium $4.99 a month, Pro $9.99 a month. About 20% of subscribers choose Pro, which gives a blended **$6 a month**.
- **Benchmarks.**
  - 1Password Individual: $3.99 a month.
  - Bitwarden Premium: $19.80 a year.
  - Proton Pass: $1.99 a month.

  None of these include a crypto wallet or a signing agent for developers and AI agents. We price slightly above password-only managers because RootWallet replaces two products.
- **Paid conversion:** 2% / 3% / 5% of monthly active users (conservative / base / upside). Developer tools typically convert 2–7% of free users to paid (secondary benchmarks).
- **Gross margin: about 90%.** The product runs on the user's device. Costs are app-store fees where they apply, payment processing and support.

### 2.2 RootWallet swap fees

- **Fee:** 0.85% per swap, the same as Phantom. MetaMask charges 0.875%.
- **Swap volume per monthly user per year:** $300 / $600 / $1,000 (our assumption). We set this deliberately low. Exodus processed about $6.9B of swaps in 2025 across 1.5–2.3M monthly users, roughly $3,000 per user, but its users trade heavily.
- **Gross margin: about 85–90%.** Aggregator and RPC costs come out of the fee.

### 2.3 RootWallet on-ramp and staking

- **On-ramp:** a revenue share of about 0.5% of card purchases (our assumption). Annual purchases per monthly user: $50 / $100 / $150. Providers charge users 3.5–5.5%, and splits are negotiated.
- **Staking:** commission on ORAMA and other assets staked through the wallet. Not modelled until mainnet.

### 2.4 Orama One hardware

- **Two editions.**
  - **Orama One:** a sealed, encrypted, low-power node. About $899–1,199 retail, roughly 30–35% gross margin.
  - **Orama One Confidential:** AMD EPYC with SEV-SNP. About $2,400–2,900 retail, roughly 35% gross margin.

  These are estimates from component prices, not supplier quotes.
- **Blended price:** $999. **Gross margin:** 30%.
- **Units sold in year 3 (months 25–36):** 300 / 1,500 / 6,000 (our assumption). For comparison, Umbrel, a consumer node box with a small team, reports about $3.7M in revenue (weak source).
- The first batch is funded by refundable pre-orders. We treat hardware as the easiest way to join the network, not as the main source of margin.

### 2.5 Compute marketplace

- **Model.** Once Orama One nodes are live, developers rent capacity on other people's nodes and pay by usage in crypto. The operator gets most of the payment and the company takes a share.
- **Take rate: 10%** (our assumption). Akash takes 4% on payments in its own token and 20% on stablecoins. Render Network takes 5%.
- **Teams renting by month 36:** 100 / 1,000 / 5,000, each spending $50 / $80 / $100 a month.
- **Gross margin: about 95%.**
- We keep this line small on purpose. Decentralized compute marketplaces earn very little in their early years: Akash earned about $3M in protocol revenue in 2025. The main case does not depend on the marketplace.

### 2.6 Monthly active wallet users

- **Month 18:** 10,000 / 50,000 / 200,000. **Month 36:** 50,000 / 250,000 / 1,000,000 (our assumption).
- **For scale.**
  - MetaMask: more than 30M monthly active users.
  - Phantom: about 15–17M.
  - Exodus: 1.5M.
  - The base case at month 36 is about one sixth of Exodus today.
- **Sources of users.** Apps on Orama (AnChat signs every user in with RootWallet), node operators, developers, the mobile app on both app stores, and sales outreach funded by this round.

---

## 3. Scenarios

### 3.1 Month 18 (annualized run rate)

| | Conservative | Base | Upside |
|---|---|---|---|
| Monthly wallet users | 10,000 | 50,000 | 200,000 |
| Subscriptions | $14k | $108k | $720k |
| Swap fees | $26k | $255k | $1.70M |
| On-ramp | $3k | $25k | $150k |
| **Run rate** | **~$43k** | **~$390k** | **~$2.6M** |

Hardware and the marketplace are excluded at month 18. The first Orama One batch is shipping and the marketplace is just opening.

### 3.2 Month 36 (annualized run rate)

| | Conservative | Base | Upside |
|---|---|---|---|
| Monthly wallet users | 50,000 | 250,000 | 1,000,000 |
| Subscriptions | $72k | $540k | $3.6M |
| Swap fees | $128k | $1.28M | $8.5M |
| On-ramp | $13k | $125k | $750k |
| **Wallet revenue** | **$212k** | **$1.94M** | **$12.85M** |
| Orama One units (year 3) | 300 | 1,500 | 6,000 |
| Hardware revenue | $300k | $1.50M | $6.0M |
| Marketplace revenue | $6k | $96k | $600k |
| **Total revenue** | **~$0.52M** | **~$3.54M** | **~$19.5M** |
| **Gross profit** | **~$0.29M** | **~$2.29M** | **~$13.9M** |
| Blended gross margin | ~55% | ~65% | ~71% |

How the main lines are calculated, using the base case:

- **Subscriptions:** 250,000 users × 3% × $6 × 12 = $540k.
- **Swap fees:** 250,000 × $600 × 0.85% = $1.275M.
- **On-ramp:** 250,000 × $100 × 0.5% = $125k.
- **Hardware:** 1,500 × $999 ≈ $1.5M.
- **Marketplace:** 1,000 teams × $80 × 12 × 10% = $96k.

Without hardware, the blended gross margin is above 85%. Hardware brings revenue and users onto the network, but it pulls the blended margin down.

### 3.3 Revenue per wallet user

| | Conservative | Base | Upside |
|---|---|---|---|
| Wallet revenue per monthly user per year | $4.20 | $7.80 | $12.90 |

| Benchmark | Revenue per monthly user per year |
|---|---|
| MetaMask | about $1.40 |
| Phantom | about $6–20 (sources conflict) |
| Exodus | about $53–81 |

The base case sits in Phantom's range. It is well below Exodus, because we assume much less trading per user.

---

## 4. Cost base and break-even

**Operating costs at month 36 (our assumption).** The team of seven costs about $630k a year: three founders in Zug at about $135k each, and four engineers and staff in Greece. On top of that comes about $300k for servers, tools, legal, accounting, audits and travel. **Total: about $0.93M a year.**

**Break-even.** In the base case, gross profit at month 36 (about $2.29M) covers the cost base more than twice. The conservative case does not break even at month 36, and would rely on the next round or on a slower burn.

**Cost per user at scale.** We modelled one million wallet users, 5,000 node operators and 250,000 developers. That covers a team of about twelve, a few servers of our own, the third-party services the wallet uses, audits, legal and insurance. It comes to about $3.9–4.7M a year, or **about $4–5 per user**.

| Comparison | Cost or revenue per unit |
|---|---|
| Our model at 1M users | about $4–5 cost per user |
| Exodus | about $86 per monthly user on technology and administration (about 215 staff) |
| Wallet RPC services at published prices | $0.03–0.6 per user per year |
| Supabase | about $17 of ARR per developer, with infrastructure on its cost line |

Those RPC calls can move onto the Orama network itself, so operators carry that cost as well.

---

## 5. What is not in the model

These are upside or open items that the scenarios leave out:

- **Network participation.** Rewards the company earns as an operator and validator from genesis, and the base fee if the final design keeps it instead of burning it (Appendix B). Both pay out in ORAMA, and we do not assume a token price.
- **Staking commission** through the wallet.
- **Developer team plans, support contracts and SLAs** for companies running apps on Orama.
- **Grants.** The EU EIC Accelerator offers up to €2.5M and Innosuisse up to 70% of direct project costs. Neither is assumed.
- **Token demand from apps.** AnChat subscriptions paid in ORAMA create demand for the token. That benefits the network and every operator, including the company, but it is not company revenue.

---

## 6. Sensitivity

The model depends most on two inputs: **monthly wallet users** and **swap volume per user**. Every wallet line scales with users, and swaps alone are about two thirds of base-case wallet revenue.

- **Users 50% lower:** base-case wallet revenue falls to about $0.97M at month 36. Total revenue with hardware is about $2.6M.
- **Swap volume halved:** base-case total revenue falls by about $0.64M, to about $2.9M.
- **Paid conversion at 1% instead of 3%:** subscriptions fall from $540k to $180k.
- **No hardware at all:** base-case revenue is about $2.0M, at a gross margin above 85%.

The cost base is small in every case, so the business reaches break-even at a fraction of the revenue a centralized cloud or wallet company needs.

---

## 7. Proof points by month 18

These are the milestones that support the next round:

- RootWallet subscriptions and swaps live for 15 months, with a run rate of $390k or more (base case).
- RootWallet independently audited and live on the App Store and Google Play.
- ORAMA mainnet live, with the company running nodes from genesis.
- The first Orama One batch shipped from pre-orders.
- Apps beyond AnChat deployed on the network.
