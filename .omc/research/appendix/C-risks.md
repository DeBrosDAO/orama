# Appendix C: Risks and how we answer them

Prepared 3 October 2026 for Verdict Capital and its advisers. Orama Network, ORAMA L1 and RootWallet. Seed round: $1.75M of equity over 18 months.

## How to read this appendix

This appendix lists sixteen risks, ordered by our own view of severity. Each one has the same four parts: the risk, why it matters, our answer, and what would change our mind or what remains. We have tried to state each risk the way a skeptical adviser would, not the way a pitch would.

Two ground rules. First, "what exists" means running code, checked against the repository. Where a design document and the code differ, we use the code. Second, external facts carry a bracketed number that points to the source list at the end, with the date we read it. Legal and regulatory material is moving quickly, and some of it changed in the last month. It is a summary for discussion, not legal advice, and part of this round funds counsel to replace our reading with theirs.

Four facts frame everything below. The company does not exist yet; a Swiss AG in Zug is formed at close. The team is three engineers, moving from Greece to Zug, growing to seven. One live application, AnChat, runs on the platform (about 1,000 downloads, about 150 active users, 87 days without downtime, paid subscriptions in crypto). The ORAMA chain and RootWallet run on development, test and staging networks only; mainnet is about twelve months after funding.

---

## 1. Regulation of private, shielded transfers (High)

**Risk.** In the current design every ORAMA transfer between users is shielded; there is no public user-to-user path. The shielded pool reuses Zcash's Orchard circuits. A regulator, bank or exchange may treat a chain with mandatory privacy as a money-laundering risk by design.

**Why it matters.** The legal climate is mixed, and the mix is the problem. In the United States, sanctions on Tornado Cash were lifted in March 2025 after the Fifth Circuit held that immutable smart contracts are not sanctionable property [1]. The Justice Department told prosecutors in April 2025 to stop pursuing regulatory violations by mixers and wallets for their users' acts, but only absent willful knowledge of a licensing requirement, and the memo does not define protection for developers [2]. A jury still convicted a Tornado Cash developer in August 2025 of conspiring to run an unlicensed money transmitter [3]; the retrial on the other counts is now set for April 2027 [4]. The Samourai Wallet founders were sentenced to five and four years in November 2025 [5]. Those were services that touched user funds and, prosecutors said, knew the proceeds were criminal. In Europe, the anti-money-laundering regulation bars regulated crypto-asset service providers from handling anonymity-enhancing coins from July 2027, though self-custody and peer-to-peer use are not prohibited [6]. Kraken delisted Monero for European users in 2024 [7]. Against that, shielded Zcash has grown to roughly 29 to 31 percent of supply in 2026 [8], and Grayscale has been advancing a spot Zcash ETF with the SEC [9].

**Our answer.** The code does not custody user funds: RootWallet is self-custody and the chain has no admin key, so no one at the company can freeze, mint or move a user's balance. That is the main line between us and the prosecuted cases. We have to be candid about a gap, however. Our engineering plan set legal constraints outside design scope, and the legal workstream was removed. That was an engineering sequencing decision, not a legal conclusion, and we do not ask you to rely on it. This round funds Swiss and US counsel (about $110k in the budget with company formation) to give a written opinion before genesis. It matters that privacy-by-default is an ossified rule that can change only by a hard fork, so the opinion must come before genesis, not after. The levers we can still pull are selective-disclosure viewing keys for exchanges and auditors, region-specific wallet behavior, and, as a last resort, changing the default before launch.

**What would change our mind.** A counsel opinion that mandatory shielding makes the token unlistable everywhere that matters would move us to optional shielding with strong defaults. Residual risk: even with a clean opinion, ORAMA may never list on regulated EU venues after July 2027. Our market path (per-user deposit addresses with viewing keys, plus on-chain DEX contracts) does not depend on them, but liquidity will be thinner.

---

## 2. Token classification and securities risk (High)

**Risk.** A regulator could treat ORAMA as a security, or the equity raise and token could be read as one scheme. The US test turns on whether purchasers rely on a team's managerial efforts for profit.

**Why it matters.** Classification decides whether the company can operate as a node operator, who may hold the token, and whether a fund holding our equity inherits exposure. The US picture is better than in 2024 and still unsettled. The SEC chair described in March 2026 four non-security categories (digital commodities, collectibles, tools, GENIUS-Act stablecoins) and a safe harbor for when an investment contract ends [10]. A proposed Regulation Crypto Assets, with fundraising exemptions of $20M and $75M, followed in August [11]. But the CLARITY Act failed a Senate cloture vote 49 to 50 on 15 September 2026 [11], so these positions rest on agency discretion, not statute, and a later commission can reverse them. In Switzerland, FINMA's guidelines sort tokens into payment, utility and asset tokens; payment tokens are not treated as securities [12]. A new Swiss licence for crypto institutions is in consultation [13].

**Our answer.** The facts help. There is zero premine, no allocation to founders, team or investors, no sale, no airdrop and no treasury allocation. The emission schedule is ossified. Equity investors receive no tokens or token rights in this round; we recommend keeping it that way, because token warrants would change the analysis. The company earns ORAMA the same way any outside operator does. No one has bought ORAMA on our promise of effort. The counsel opinion in risk 1 covers this too.

**What would change our mind.** Any fundraising in tokens, or public statements promising price or future company effort, would weaken the position. Residual risk: the 5 percent development share in the emission split (recommended in the plan as a two-house-approved spend, owner decision still open) is the item a regulator will examine hardest. The company does not control it.

---

## 3. How the company captures value with a zero-premine token (High)

**Risk.** With no allocation, equity value depends on revenue the company earns, not on tokens it holds. If the token is illiquid or low-priced, token income is small. Meanwhile, if open-source software and an open chain are free to use, nothing guarantees that the company, rather than a competitor or fork, collects the margin.

**Why it matters.** This is the core question for an equity investor. The comparable shows the other side: ZODL raised $25M of equity in March 2026 around a fair-launch chain [14], so the model has precedent, but only one data point.

**Our answer.** The company's income has five separate sources, and we underwrite none of the token ones. (1) RootWallet subscriptions and swap fees, from month 3; peers charge about 0.85 percent per swap [15]. (2) Operator income from running nodes on the chain from genesis. Emission is 14,848 ORAMA per day in years one and two, of which 60 percent goes to validators and delegators, 25 percent to storage and 10 percent to relays; operators earn only what they actually do. (3) Usage billing for Orama hosting; nothing is metered or charged today. (4) Orama One hardware at an estimated 25 to 35 percent gross margin, from component prices, not supplier quotes. (5) Later, a compute marketplace; Akash's take rate is 4 to 20 percent and its quarterly lease revenue was $253k in Q1 2026 [16], so we do not underwrite it early.

The destination of the base fee is an open decision. The plan currently burns 100 percent. A share could instead go to operators or a development fund. We will decide before genesis, and nothing in the revenue plan assumes the company receives any of it.

**What would change our mind.** If token income stays under 10 percent of revenue by month 12 and the products carry the business, the model is a normal software company with a chain attached, which is fine. If the products do not carry the business, the token will not save it. Residual risk: a fork can copy the chain, and cannot copy the operators, the wallet users or the brand.

---

## 4. Adoption: developers, operators and users need each other (High)

**Risk.** A network needs developers to build, operators to supply nodes and users to pay. Each waits for the others. Decentralized infrastructure has mostly failed here.

**Why it matters.** The numbers are sobering. The whole DePIN sector earned about $72M of on-chain revenue in 2025 [17]. Akash's quarterly lease revenue fell 45 percent to $253k with 33.7 percent GPU use [16]. Crypto-native developers are shrinking, from about 31k monthly active in 2022 to roughly 18 to 21k in 2025 [18]. Our reading is that decentralized compute stalled on developer experience, not demand: those networks sell raw machines, not a full stack. That reading is a hypothesis, not a proven fact.

**Our answer.** We pitch mainstream developers a full stack (hosting, SQL, storage, functions, voice and video relay, wallet sign-in) with decentralization as a property, not the headline. Operators come second, from a vetted list, because node operation today requires trust: the operator of an Ubuntu node has root access. Today the networks have three nodes each and one real tenant. Public sign-up does not exist, and there is no dashboard. The first 18 months aim at that gap: sales hire, UX hire, developer onboarding. RootWallet brings its own users to the chain, and AnChat is the proof that an end-user product runs on it.

**What would change our mind.** If, six months after public sign-up, fewer than a few dozen outside developers deploy and pay, the thesis is wrong and we would say so. Residual risk is high and cannot be bought down with money alone.

---

## 5. Security incidents and audit coverage (High)

**Risk.** A wallet drain, a chain consensus bug or a zero-knowledge soundness bug would be severe, and on a chain with no admin key some of these cannot be undone except by a coordinated hard fork.

**Why it matters.** Wallets are the target: Chainalysis counts $713M stolen from personal wallets in 2025 across 158,000 incidents [19]. Chain and cryptography bugs mint money invisibly. Our own plan notes that a bug in the Orchard circuits survived four years of human audits before AI-assisted review found it.

**Our answer, and the honest coverage.** RootWallet gets an independent external audit funded by this round (about $90k). The Orama platform and our zero-knowledge integration are reviewed in house by the cryptography and security engineers we hire; the Orchard circuits themselves are upstream and previously audited, so only our integration is new. That is thinner than the plan requires: our own plan calls for external audits of every chain module and a separate external ZK audit before mainnet, and no audit of the chain has been done. The round's base budget does not pay for those. Our recommendation, to be agreed with you, is that mainnet does not carry value until an external audit of the chain and the ZK integration is complete, funded from contingency or a later raise. Existing practice: our own audits, 90 findings in one earlier review and two more in September 2026 (35 stability items, 29 authentication items), almost all fixed and awaiting review. A security policy and disclosure process exist; bounty amounts do not, and are not promised. A halt-height hard-fork playbook replaces any kill switch.

**What would change our mind.** A critical finding in the external wallet audit that requires redesign. Residual risk: audits reduce, not remove, bug risk, and mainnet will be the first time real value sits behind this code.

---

## 6. Revenue timing: the month-3 claim (High)

**Risk.** We expect revenue from month 3, from RootWallet subscriptions and swaps. Nothing is billed today in Orama, and RootWallet has not been tested on paying users at scale.

**Why it matters.** The claim could simply be too early. Free-to-paid conversion in developer tools runs about 2 to 7 percent, according to blog benchmarks of weak quality [20]. Take a $36 yearly subscription: $10k a month needs about 3,300 paying subscribers, which at 5 percent conversion means about 67,000 users. At 0.85 percent per swap, $10k a month needs about $1.2M of monthly swap volume. For scale, Exodus earned $121.6M in 2025, 76 percent from swaps, while its monthly users fell from 2.3M to 1.5M [15]. Swap revenue follows market volume, not product quality.

**Our answer.** Month 3 means first revenue, not meaningful revenue. Our cost model, built for a team of twelve at 1M monthly users, puts total annual cost (team, infrastructure, RPC, audits, legal, insurance) at roughly $3.9M to $4.7M, which shows how far early revenue is from covering payroll. Revenue at the start funds learning, not payroll. The 18-month runway covers payroll without it. The swap line carries a legal condition (see risk 14).

**What would change our mind.** Fewer than a few hundred paying users by month 6 would lead us to cut the hardware line and concentrate on whichever product is converting.

---

## 7. Untrusted operators reading tenant data (Medium)

**Risk.** Operators run other people's workloads. An operator can read memory, disks and traffic on a machine they control.

**Why it matters.** This limits what we may promise a tenant, and it can cause reputational harm if overpromised. Our whitepaper says plainly that memory is not protected from a host that can snapshot it, and that Ubuntu operators have root.

**Our answer.** Today operators are invited and vetted. Each tenant has its own small cluster, so a single node holds a share of one tenant's data, not everyone's. Orama's privacy comes mostly from not collecting data: no email, password or phone number, so a demand for identity returns only a public key. OramaOS (no shell, encrypted data partition whose key is split across peers) is built but has never been booted on a live network, and has known gaps. Confidential hardware has hard limits. Real confidential virtual machines need server CPUs (AMD EPYC 7003, 8004 and later; Intel Xeon with TDX), and the TEE.fail attack showed that a sub-$1,000 interposer on a DDR5 board extracts keys from both TDX and SEV-SNP [21]. Intel and AMD treat physical attacks as out of scope. So we will not claim that an owner cannot read tenant data. Sensitive tenants go on attested server-class nodes under vetted operators; consumer devices are described as sealed, encrypted and attested, nothing more.

**What would change our mind.** A working remote attack on SEV-SNP or TDX would remove the confidential tier. Residual risk: until OramaOS ships, public operators are not safe to admit.

---

## 8. Small team and key-person dependence (Medium)

**Risk.** Three engineers hold nearly all knowledge across a chain, a platform, a wallet and zero-knowledge integration. Losing one would stall the company.

**Why it matters.** Seed investors expect four-year vesting with a one-year cliff as a minimum [22], and expect a plan for the case where a founder leaves.

**Our answer.** Founders accept standard vesting at close. The documentation is unusually deep for the size (a written whitepaper checked against code, security notes, runbooks, an e2e test suite that fails on unmapped features), so a new engineer has a way in. The round adds a cryptography engineer and a security engineer, who cut concentration in the highest-risk areas. We would also take key-person insurance if you ask for it; at this size it is cheap and partial protection.

**What would change our mind.** A founder departure in the first year would trigger an immediate hire plan and, if two leave, a conversation about returning capital. Residual risk is real: seven people is the limit of what the plan can cover.

---

## 9. Competition from better-funded companies (Medium)

**Risk.** Supabase raised $500M at $10.5B in June 2026 [23]; Vercel raised $300M at $9.3B in September 2025 [24]; Railway raised $100M in January 2026 [25]. In wallets, MetaMask, Coinbase, Phantom and 1Password all launched agent features in 2026 [26].

**Why it matters.** Any of them can add a feature we have. They have distribution; we do not.

**Our answer.** We do not compete on their ground. The empty quadrant is a broad developer stack that no vendor can switch off, plus a wallet that is also a vault, local and self-custody. Incidents keep proving the demand: Google Cloud suspended Railway's account in May 2026 and caused an eight-hour outage [27]. None of the funded companies can offer it without giving up the business model they sell. Honest limit: our maturity and service-level record is weak next to theirs, and a customer who needs an SLA should not choose us yet.

**What would change our mind.** If a large player ships user-controlled hosting, we would shift weight to the chain and wallet.

---

## 10. Company formation, jurisdiction and relocation (Medium)

**Risk.** The company is formed at close, in Switzerland, by founders still in Greece. US funds often prefer a Delaware parent.

**Why it matters.** The structure affects your documents, our tax position and the timeline. US venture instruments, including the SAFE, assume a Delaware corporation, and re-papering mid-round is a known mistake [28].

**Our options, stated neutrally.** (a) A Swiss AG at close, with an investment agreement under Swiss law. This fits the Zug location, the FINMA framework and Swiss banking, and needs CHF 100k of capital, half paid in. It is less familiar to US counsel. (b) A Delaware parent over the Swiss AG (a "flip"), which costs roughly $5k to $25k and some weeks [28]. This is the usual US fund choice, and adds a second jurisdiction. (c) A SAFE or convertible into the AG, with the flip agreed in advance. We have no preference worth defending; we ask you to tell us which your documents need. For the move, Greek citizens may live and work in Switzerland under the EU free-movement agreement [29]. Counsel will confirm the residency rule for a Swiss AG's signatories and the permit timing. Relocation is budgeted at about $45k.

**What would change our mind.** If your counsel requires Delaware, we form it; we would not lose the deal over this.

---

## 11. Hardware execution (Medium)

**Risk.** Orama One is a sealed node at about $899 to $1,199, and a confidential edition at about $2.4k to $2.9k. We have never shipped hardware.

**Why it matters.** Hardware has long lead times, certification risk and inventory cost, and small teams stall on it. No hardware exists yet; the current design is a revision A concept.

**Our answer.** The round funds a prototype and a first small batch (about $165k), not a production run. Certification is a real cost: for a non-radio device, FCC declaration runs about $1.5k to $5k and EMC testing about EUR 1.5k to 10k, with repeat testing costing more [30]. Pricing is from component costs, not quotes, at 25 to 35 percent margin. We would open pre-orders only after prototype tests pass. The product is deliberately modest: sealed, encrypted and attested, never "the owner cannot read it".

**What would change our mind.** Quotes that push the bill of materials over the range, or a failed EMC test cycle, would defer the batch; the software plan does not depend on hardware.

---

## 12. Scale limits (Medium)

**Risk.** The platform fits small and mid-sized applications. It has been exercised on three-node networks with a few tenants. The design allows 20 namespaces per node, and growth is by adding nodes.

**Why it matters.** Investors will ask whether it is a toy. Enforced storage quotas, tenant metrics and alerting do not exist yet. Each tenant gets its own cluster, which is strong isolation and costly density.

**Our answer.** We state the limit. Within it, AnChat uses nearly every service and has run 87 days without downtime. Month-by-month work targets what blocks growth: roll the hardened release to both networks, add metrics, enforce quotas. We are not courting large-scale workloads in the first 18 months.

**What would change our mind.** Load tests with several dozen tenants that show per-tenant cost above what we can price.

---

## 13. Focus across three products (Medium)

**Risk.** A platform, a chain and a wallet is a lot for seven people.

**Why it matters.** Founders who build everything finish nothing.

**Our answer.** The three share one codebase style, one identity system and one security model: the wallet signs in to the platform, and the chain pays operators. The plan fixes an order: stabilize the network, finish OramaOS, then hardware. The wallet comes first on revenue, the platform second, the chain third. We would drop the hardware and marketplace lines first.

**What would change our mind.** Missing two consecutive quarterly milestones means cutting a product, not hiring around it.

---

## 14. App-store distribution of a privacy wallet (Medium)

**Risk.** A wallet reaches most consumers through Apple and Google. They could refuse or remove it.

**Why it matters.** Apple allows wallet apps only from developers enrolled as organizations [31], which is one more reason the AG must exist early. Google Play now requires licensing for custodial wallet providers in the US and EU, and excludes non-custodial wallets [32].

**Our answer.** RootWallet is non-custodial. The condition to watch is swaps: Swiss practice treats a wallet that lets users exchange two payment tokens as a possible financial intermediary under the anti-money-laundering act [33]. Counsel will decide whether swaps run through licensed third-party aggregators. Distribution does not depend only on stores: desktop, command-line, web and direct Android installs exist.

**What would change our mind.** A store removal in one market is an inconvenience; removal on both stores would hurt consumer growth, and we would lean on developers.

---

## 15. Third-party dependencies still in place (Low)

**Risk.** We claim independence yet depend on outside services: RPC providers for wallet balances, and commercial hosts (Hetzner, OVH) for operator nodes.

**Why it matters.** An RPC provider sees which addresses a wallet queries, and a host could suspend an account.

**Our answer.** RootWallet supports a direct mode and a proxy mode in which the provider key lives only in an Orama secret, so no key ships in the client. We list this as a limit, not a fix. Spreading operators across providers and, over time, onto owned hardware is the remedy. At 1M monthly users, RPC cost is roughly $30k to $63k a year at current pricing, which is small [34].

**What would change our mind.** A provider policy change affecting privacy-wallet traffic would move us to self-hosted nodes.

---

## 16. Open-source forking and the AGPL (Low)

**Risk.** Core and chain are AGPL-3.0, SDKs MIT. A competitor could run our code as a service, or fork it.

**Why it matters.** Open-core companies have been through license turmoil. MongoDB moved to the non-open-source SSPL in 2018; Elastic followed in 2021; Grafana moved to AGPL in 2021 [35]; Redis added AGPL again in May 2025 after a fork gained ground [36]. AGPL keeps the code open while requiring that anyone offering modified code as a service shares their changes, and some large enterprises avoid it.

**Our answer.** AGPL is the conservative choice among those precedents. Our moat is operators, brand, wallet users and speed, not the code. One gap to close now: the contributing guide does not yet ask contributors to sign a contributor license agreement or a developer certificate. Counsel should settle that while the contributor base is three people, because relicensing later requires every contributor's consent. A chain fork is also normal in this ecosystem and carries no legal risk.

**What would change our mind.** A cloud provider reselling a modified Orama without contributing back, which AGPL addresses.

---

## Summary table

| # | Risk | Severity | Mitigation status |
|---|------|----------|-------------------|
| 1 | Regulation of shielded transfers | High | Non-custodial, no admin keys; counsel opinion funded, due before genesis; not yet obtained |
| 2 | Token classification | High | Zero premine, no investor tokens; counsel opinion funded; US rules rest on agency discretion |
| 3 | Value capture without a premine | High | Five revenue lines; base-fee destination open; token income not underwritten |
| 4 | Adoption, chicken and egg | High | Full-stack positioning; vetted operators; sales and UX hires; unproven |
| 5 | Security incidents, audit coverage | High | RootWallet external audit funded; chain and ZK audit external gap, not in base budget |
| 6 | Revenue timing (month 3) | High | Runway does not depend on it; no billing in Orama today; unproven |
| 7 | Untrusted operators | Medium | Vetted operators; OramaOS unbooted; no owner-cannot-read claim |
| 8 | Small team, key person | Medium | Vesting, documentation, two security hires; insurance on request |
| 9 | Well-funded competitors | Medium | Empty-quadrant positioning; weak on SLA and maturity |
| 10 | Formation and relocation | Medium | Swiss AG, Delaware flip or SAFE open to your preference; counsel funded |
| 11 | Hardware execution | Medium | Prototype and first batch only; certification budgeted; software independent |
| 12 | Scale limits | Medium | Limits stated; hardening and metrics in progress; three-node networks today |
| 13 | Three-product focus | Medium | Fixed order; hardware and marketplace dropped first |
| 14 | App-store distribution | Medium | Non-custodial; AG enables Apple enrollment; swaps need counsel |
| 15 | Third-party dependence | Low | RPC proxy mode; operator diversification; disclosed |
| 16 | AGPL and forking | Low | AGPL precedent; contributor terms to settle |

---

## Sources (read 3 October 2026)

1. U.S. Treasury, Tornado Cash delisting, 21 March 2025: https://home.treasury.gov/news/press-releases/sb0057 ; summary of the Fifth Circuit ruling (Nov 2024): https://www.mlex.com/mlex/trade/articles/2314096/tornado-cash-removed-from-us-sanctions-list
2. Steptoe on the DAG memo "Ending Regulation by Prosecution" (7 April 2025): https://www.steptoe.com/en/news-publications/blockchain-blog/deputy-attorney-general-memorandum-ending-regulation-by-prosecution.html ; WilmerHale, 10 Sept 2025: https://www.wilmerhale.com/en/insights/client-alerts/20250910-doj-signals-approach-to-digital-assets-what-it-means-for-developers-and-platforms
3. Mayer Brown, Tornado Cash verdict (6 Aug 2025): https://www.mayerbrown.com/en/insights/publications/2025/08/the-tornado-cash-trials-mixed-verdict-implications-for-developer-liability
4. Retrial moved to 26 April 2027 (order of 25 Aug 2026): https://cryptobriefing.com/roman-storm-retrial-postponed-april-2027/
5. IRS Criminal Investigation, Samourai sentencing (Nov 2025): https://www.irs.gov/compliance/criminal-investigation/founders-of-samourai-wallet-cryptocurrency-mixing-service-sentenced-to-five-and-four-years-in-prison
6. EU AMLR Article 79 (Regulation 2024/1624), summary: https://blog.thirdweb.com/eu-privacy-coin-ban-2027-what-the-amlr-means-for-web3-builders/
7. Kraken delists Monero in the EEA (Apr 2024): https://www.benzinga.com/markets/cryptocurrency/24/04/38208325/crypto-exchange-kraken-joins-binance-in-delisting-privacy-coin-monero
8. Zcash shielded share, 2026: https://crypto.news/zcash-price-tests-300-shielded-supply-30-percent-2026/ ; https://coinstats.app/ai/a/fundamental-analysis-zcash (Sept 2026)
9. Grayscale amended Zcash ETF filing (21 Aug 2026): https://www.theblock.co/news/regulation/2026-08-21-grayscale-moves-closer-launching-first-zcash-etf-in-us-sec-amended-filing-412517
10. SEC Chair remarks, 17 March 2026: https://www.sec.gov/newsroom/speeches-statements/atkins-remarks-regulation-crypto-assets-031726
11. Troutman Pepper Locke on the CLARITY Act cloture failure (15 Sept 2026) and agency actions: https://www.troutman.com/insights/in-the-wake-of-clarity-acts-failure-agencies-move-forward-without-congressional-action-or-certainty/ ; CNBC: https://www.cnbc.com/2026/09/15/senate-cloture-vote-on-clarity-act-fails-dealing-regulatory-setback-to-crypto-industry.html
12. FINMA ICO guidelines (16 Feb 2018): https://www.finma.ch/en/news/2018/02/20180216-mm-ico-wegleitung/
13. Swiss Federal Council consultation on crypto institutions (22 Oct 2025, to 6 Feb 2026): https://www.borel-barbey.ch/en/the-swiss-federal-council-launches-a-consultation-aiming-at-advancing-swiss-crypto-and-stablecoin-regulation/
14. ZODL seed, CoinDesk (9 Mar 2026): https://www.coindesk.com/business/2026/03/09/josh-swihart-s-zcash-open-development-lab-raises-usd25-million-in-seed-funding
15. Exodus FY2025 and swap fees, The Block: https://theblock.co/post/393356/exodus-2025-net-loss (via internal research file, `.omc/research/pitch-deck-research.md`)
16. Messari, State of Akash Q1 2026: https://messari.io/report/state-of-akash-q1-2026-final
17. DePIN 2025 revenue, BlockEden (21 Mar 2026): https://blockeden.xyz/blog/2026/03/21/depin-march-2026-reality-check-650-projects-19b-market-cap-revenue/
18. CoinDesk, crypto developer activity (12 Mar 2026): https://www.coindesk.com/tech/2026/03/12/crypto-developer-activity-sinks-to-multi-year-low-as-ai-absorbs-github-s-talent-boom
19. Chainalysis, 2025 theft data: https://www.chainalysis.com/blog/crypto-hacking-stolen-funds-2026/
20. Free-to-paid benchmarks (low quality): https://www.acceleroi.com/blog/benchmarks/saas-plg-free-to-paid-conversion-rate
21. TEE.fail: https://tee.fail/ ; The Hacker News (Oct 2025): https://thehackernews.com/2025/10/new-teefail-side-channel-attack.html
22. Founder vesting norms: https://capbase.com/founder-vesting-schedules-best-practices/
23. CNBC on Supabase (4 June 2026): https://www.cnbc.com/2026/06/04/database-startup-supabase-raises-500-million-10point5-billion-valuation.html
24. GIC on Vercel Series F (Sept 2025): https://www.gic.com.sg/newsroom/all/vercel-closes-series-f-at-9-3b-valuation-to-scale-the-ai-cloud/
25. Railway Series B (Jan 2026): https://blog.railway.com/p/series-b
26. CoinDesk on MetaMask agent wallet (8 June 2026): https://www.coindesk.com/tech/2026/06/08/metamask-launches-ai-agent-wallet-with-built-in-security-for-crypto-trades ; Coinbase agentic wallets (Feb 2026): https://www.coinbase.com/developer-platform/discover/launches/agentic-wallets
27. Railway incident report (19 May 2026): https://blog.railway.com/p/incident-report-may-19-2026-gcp-account-outage
28. Delaware flip guidance, Haynes Boone: https://www.haynesboone.com/news/publications/the-flip-transaction-bringing-your-foreign-startup-into-the-us-investment-market ; cost range: https://capbase.com/delaware-flip-turn-your-startup-into-delaware-c-corp/
29. Swiss State Secretariat for Migration, EU/EFTA free movement: https://www.sem.admin.ch/sem/en/home/themen/fza_schweiz-eu-efta/eu-efta_buerger_schweiz/faq.html
30. Certification costs: https://markready.io/learn/fcc-certification-cost ; https://ecocomply.ai/blog/what-does-ce-marking-cost-for-your-product
31. Apple App Review Guidelines, 3.1.5: https://developer.apple.com/app-store/review/guidelines/
32. Google Play crypto policy and non-custodial wallets (Aug 2025): https://thepaypers.com/crypto-web3-and-cbdc/news/google-plays-new-policy-not-to-impact-non-custodial-crypto-wallets
33. Swiss treatment of non-custodial wallets and AMLA: https://notabene.id/world/switzerland ; FinCEN 2019 guidance (US, unhosted wallets): https://www.fincen.gov/system/files/2019-05/FinCEN%20Guidance%20CVC%20FINAL%20508.pdf
34. RPC pricing at scale, from `.omc/research/pitch-deck-research.md` (Alchemy and Helius public price lists).
35. Grafana relicensing to AGPL (20 Apr 2021): https://grafana.com/blog/2021/04/20/qa-with-our-ceo-on-relicensing/ ; Goodwin on source-available licensing (Sept 2024): https://www.goodwinlaw.com/en/insights/publications/2024/09/insights-practices-moving-away-from-open-source-trends-in-licensing
36. Redis 8 adds AGPLv3 (May 2025): https://www.phoronix.com/news/Redis-8.0-Goes-AGPLv3
