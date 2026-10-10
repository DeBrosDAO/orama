# Pitch deck research — Orama Network + RootWallet

Compiled 2026-10-03 for the Verdict Capital pitch. Every number carries a source. Tags: **[H]** primary (filing, company), **[M]** credible secondary, **[W]** weak/aggregator — never put [W] on a slide without re-checking.

---

## 0. The investor — Verdict Capital

- Founded Jan 2026 by Niko Bonatsos (ex-General Catalyst MD; Discord, Mercor) and Michael Fertik (Heroic Ventures). Raising Fund I, $250–300M. [Bloomberg](https://www.bloomberg.com/news/articles/2026-01-16/ex-general-catalyst-leader-bonatsos-targets-300-million-new-fund), [TFN](https://techfundingnews.com/ex-general-catalyst-and-heroic-ventures-execs-launch-verdict-capital-with-300m-target/)
- Thesis "First Money": first institutional check, happy to be the only investor, loves "pre-category companies (startups for which the category does not yet have a name)", young technical founders. [michaelfertik.com](https://www.michaelfertik.com/verdict-capital)
- Checks $1M–$10M, Seed + Series A; sectors fintech/payments, consumer/social, enterprise software, AI. SF / NYC / Israel. [VC Sheet](https://www.vcsheet.com/who/niko-bonatsos)
- Not a crypto fund → lead with product + team efficiency; chain second. Ask early whether a Swiss AG works for them (US funds often want a Delaware C-corp).

## 1. Budget (agreed with founder 2026-10-03)

| Line | € |
|---|---|
| 3 founders in Zug (€120k each loaded × 1.5 yr) | 540k |
| Cryptography + security engineer, Greece (~€5k/mo + ~22% employer cost, from month 3) | 190k |
| Official external audit — RootWallet | 80k |
| Orama One prototype + first batch | 150k |
| Servers, hardware, infra | 80k |
| Swiss AG, legal, accounting | 100k |
| Travel & networking | 80k |
| Relocation | 40k |
| **Subtotal** | **1.26M** |
| Contingency | 140–240k |
| **Ask** | **€1.4M (tight) / €1.5M (recommended)** |

Orama + ZK integration reviewed in-house by the two hires; the shielded pool reuses Zcash's already-audited Orchard circuits, so only our integration needs review.

## 2. Market

### Developers
- 47.2M active developers worldwide, +10%/yr. [SlashData 2025](https://www.slashdata.co/post/global-developer-population-trends-2025-how-many-developers-are-there) [H]
- ~34% work solo or in teams ≤20 → ~16M (our application of SlashData's breakdown) [M]
- 180M+ GitHub accounts, 36M new in 2025. [Octoverse 2025](https://github.blog/news-insights/octoverse/octoverse-a-new-developer-joins-github-every-second-as-ai-leads-typescript-to-1/) [H]
- Crypto devs SHRINKING: ~31k monthly active (2022) → ~23.6k (2024) → ~18–21k (2025). [CoinDesk 2026-03-12](https://www.coindesk.com/tech/2026/03/12/crypto-developer-activity-sinks-to-multi-year-low-as-ai-absorbs-github-s-talent-boom) [M] → don't pitch "web3 devs" as the market; pitch mainstream devs with crypto built in.

### Developer clouds (the comparables)
- Supabase: $500M Series F at $10.5B (Jun 2026), ~10M developers, >60% of new databases launched by AI tools. [CNBC](https://www.cnbc.com/2026/06/04/database-startup-supabase-raises-500-million-10point5-billion-valuation.html) [M]; ARR ~$170M (May 2026), +221%. [Sacra](https://sacra.com/research/supabase-170m-year-growing-221-yoy/) [M]
- Vercel: $300M Series F at $9.3B (Sep 2025). [GIC](https://www.gic.com.sg/newsroom/all/vercel-closes-series-f-at-9-3b-valuation-to-scale-the-ai-cloud/) [H]; ARR ~$340M (Feb 2026). [Sacra](https://sacra.com/c/vercel/) [M]
- Railway: $100M Series B (Jan 2026), 2M+ users, +200k/mo, 10M deploys/mo. [Railway](https://blog.railway.com/p/series-b) [H]
- Render: $100M at $1.5B (Feb 2026), 4.5M+ developers. [CNBC](https://www.cnbc.com/2026/02/17/render-raises-100-million-at-1point5-billion-valuation.html) [M]
- Netlify: 10M developers (Dec 2025). [Netlify](https://www.netlify.com/blog/10-million-developers/) [H]
- Cloudflare: 7.4M developers; Q2 2026 revenue $696M +36%. [8-K](https://www.sec.gov/Archives/edgar/data/0001477333/000147733326000053/q226exhibit991.htm) [H]
- Vibe-coding wave: Lovable 60M+ projects / 8M users (company page); Replit ~50M users. [lovable.dev](https://lovable.dev/guides/bolt-vs-replit-vs-lovable) [W]

### Decentralized compute (honest context)
- Entire DePIN sector earned ~$72M on-chain revenue in 2025. [BlockEden/Messari](https://blockeden.xyz/blog/2026/03/21/depin-march-2026-reality-check-650-projects-19b-market-cap-revenue/) [M]
- Akash Q1 2026 lease revenue $253k (−45% QoQ), 33.7% GPU utilization. [Messari](https://messari.io/report/state-of-akash-q1-2026-final) [H]
- Story: decentralized compute failed on developer experience, not on demand — they sell raw VMs, nobody sells the full developer stack.

### Wallets, vaults, privacy
- 741M crypto owners (2025). [Crypto.com](https://crypto.com/en/company-news/global-cryptocurrency-ownership-reaches-741-million-in-2025) [M]
- MetaMask 30M+ MAU [Blockworks](https://blockworks.com/news/metamask-monthly-active-users-blockaid) [M]; Phantom ~15–17M MAU, $3B valuation [Defiant](https://thedefiant.io/news/defi/phantom-wallet-raises-150-million-funding-round-doubles-valuation-to-3-billion-513eb3ca) [M]
- 1Password $400M+ ARR (Oct 2025). [CNBC](https://www.cnbc.com/2025/11/06/ryan-reynolds-backed-1password-tops-400-million-in-arr.html) [H]; Bitwarden 15M+ users. [Yahoo](https://finance.yahoo.com/technology/ai/articles/bitwarden-surpasses-15-million-users-160000253.html) [M]
- Zcash shielded supply ~11% (early 2025) → ~30% (2026); shielded share of tx 59% (Feb 2026). [CoinMarketCap](https://coinmarketcap.com/top-stories/69f4dd2af67947412863d0c5/) [M]

### TAM / SAM / SOM (bottom-up, assumptions labelled)
- **Orama** — TAM: 16M small-team devs × $300/yr = $4.8B. SAM: privacy/sovereignty-minded 3% (480k × $300 = $144M) + crypto-native devs (~21k × $600 = $12M) ≈ $155M. SOM yr3: ≈ $2M ARR (optimistic $7M).
- **RootWallet** — TAM: 741M owners × 10% self-custody × $36/yr ≈ $2.7B. SAM: power users (45M × 10% × $60 = $270M) + dev vault (16M × 10% × $36 = $58M) ≈ $330M. SOM yr3: ≈ $2M ARR.
- **Combined** — TAM ≈ $7.5B, SAM ≈ $0.5B, SOM ≈ $4M ARR yr3 (upside ~$12M). Own-assumption %s: 3%, 10%, 10% — be ready to defend.

### Numbers NOT to use
PaaS "$94B→$264B" (enterprise, vendors disagree 2×), VPN market $54–86B, DePIN "$3.5T by 2028", Akash "$20M annualized", ICP tx counts, agentic commerce "$1.5T by 2030", Trust Wallet 220M users, Phantom revenue figures (sources conflict $102M vs $326M), "94% fear lock-in" (enterprise survey).

## 3. Competition

### Incidents (the "why now")
- Railway 8-hour total outage 2026-05-19 — Google Cloud wrongly suspended its account, took down workloads on AWS and Railway Metal too. [Railway postmortem](https://blog.railway.com/p/incident-report-may-19-2026-gcp-account-outage) [H]
- AWS us-east-1 15+ hours, 2025-10-20. [ThousandEyes](https://www.thousandeyes.com/blog/aws-outage-analysis-october-20-2025) [M]
- Cloudflare global outage 2025-11-18. [Cloudflare](https://blog.cloudflare.com/18-november-2025-outage/) [H]
- Heroku → "sustaining engineering" (no new features) 2026-02-06. [DeployHQ](https://www.deployhq.com/blog/heroku-sustaining-engineering-alternatives) [M]
- Vercel bill shocks / 4 repricings since 2024 [bex](https://bex.co/blog/2026/08/04/vercel-four-repricings-hosted-platform-bill-shock) [W]
- Bybit $1.5B (2025-02-21): signers blind-signed a malicious tx shown as benign. [BleepingComputer](https://www.bleepingcomputer.com/news/security/lazarus-hacked-bybit-via-breached-safe-wallet-developer-machine/) [M]
- npm supply-chain attack swapping crypto addresses (2025-09-08). [Sygnia](https://www.sygnia.co/threat-reports-and-advisories/npm-supply-chain-attack-september-2025/) [M]
- Trust Wallet extension drained ~$7–8.5M (2025-12-24). [Halborn](https://www.halborn.com/blog/post/explained-the-trust-wallet-hack-december-2025) [M]
- LastPass 2022 vaults still being cracked; $35M+ stolen. [The Hacker News](https://thehackernews.com/2025/12/lastpass-2022-breach-led-to-years-long.html) [M]

### Orama 2×2 — "full developer stack" × "no vendor can switch you off"
- Broad stack, vendor-controlled: Vercel, Supabase, Railway, Render, Firebase, Heroku
- Narrow stack, vendor-controlled: Cloudflare
- Narrow stack, user-controlled: Akash, Flux, Fluence, io.net (raw compute); ICP (proprietary)
- **Broad stack, user-controlled: Orama — empty quadrant otherwise**

### Orama feature table (Y/P/N)
| | Orama | Vercel | Railway | Supabase | Cloudflare | Akash | ICP |
|---|---|---|---|---|---|---|---|
| App hosting | Y | Y | Y | N | Y | P | P |
| SQL database | Y | P | Y | Y | Y | N | P |
| Serverless | Y | Y | N | Y | Y | N | Y |
| Storage (IPFS) | Y | N | N | N | P | N | N |
| WebRTC TURN/SFU | Y | N | N | N | Y | N | N |
| Wallet login | Y | N | N | P | N | N | Y |
| Own your nodes | Y | N | N | N | N | Y | N |
| Own L1 + private tx | Y | N | N | N | N | N | N |
| Maturity / SLA | weak | strong | medium | strong | strong | weak | medium |

### Privacy L1 landscape
Zcash (privacy, no contracts), Monero (no contracts), Aztec (private contracts, alpha Apr 2026), Aleo ($228M raised), Midnight (mainnet Mar 2026), Secret/Oasis (TEE-based), Namada, Penumbra (small). Solana Confidential Balances hide amounts only. ORAMA position: contracts (CosmWasm) + Zcash-grade shielding + an app backend per namespace.

### RootWallet 2×2 — "breadth of secrets" × "native BTC+EVM+SOL"
- Wallet only, native multi-chain: Phantom, Exodus, Coinbase Wallet, Backpack
- Vault only: 1Password, Bitwarden, Proton Pass
- **Wallet + vault, native multi-chain: RootWallet — none found**

Agent signing is crowded in 2026: MetaMask Agent Wallet (Jun 2026, [CoinDesk](https://www.coindesk.com/tech/2026/06/08/metamask-launches-ai-agent-wallet-with-built-in-security-for-crypto-trades)), Coinbase Agentic Wallets (Feb 2026, [Coinbase](https://www.coinbase.com/developer-platform/discover/launches/agentic-wallets)), Phantom MCP, 1Password Unified Access (Mar 2026, [1Password](https://1password.com/press/2026/mar/1password-unified-access)). Our edge is local + self-custody + combined with the vault, not "agent support" alone.

## 4. Business model and margins

### Comparables
- Gross margin, own infra: DigitalOcean 55% (Q2 2026) [DO IR](https://investors.digitalocean.com/news/news-details/2026/DigitalOcean-Announces-Second-Quarter-2026-Financial-Results/default.aspx) [H]; Fastly 63% [8-K](https://www.sec.gov/Archives/edgar/data/0001517413/000151741326000212/ex991-fslypressrelease63026.htm) [H]; Cloudflare 72% [8-K](https://www.sec.gov/Archives/edgar/data/0001477333/000147733326000053/q226exhibit991.htm) [H]
- Prices: Vercel Pro $20/seat, Supabase Pro $25, Railway $5/$20 + usage, Render Pro $25, Fly $5/$29/$199. [Sacra](https://sacra.com/c/supabase/), [Costbench](https://costbench.com/software/developer-tools/railway/) [M]
- Free→paid in dev tools: 2–7% (blog benchmarks). [acceleroi](https://www.acceleroi.com/blog/benchmarks/saas-plg-free-to-paid-conversion-rate) [W]
- Compute marketplaces: Akash take 4% (AKT) / 20% (USDC) [Akash](https://akash.network/roadmap/aep-23/); Render 5%. Revenue small (Akash ~$3M 2025) → don't underwrite marketplace revenue early.
- Hardware: Umbrel ~$3.7M revenue, $599–949 units [W]; Start9 from $899; Ledger >$100M 2025 [W]. No sourced hardware margin → assume 25–35%.
- Wallets: swap fee standard 0.85% (Phantom) / 0.875% (MetaMask). Exodus FY2025 revenue $121.6M, 76% from swaps, MAU fell 2.3M → 1.5M while revenue held flat (≈$53–81 per MAU). [The Block](https://theblock.co/post/393356/exodus-2025-net-loss) [M]. MetaMask ≈ $1.4/MAU, Phantom ≈ $6–20/MAU (sources conflict).
- Vault subscriptions: 1Password $3.99/mo individual, ~$7.99/user business, >75% revenue from business, >90% gross retention [CNBC]; Bitwarden $19.80/yr; Proton Pass $1.99/mo.
- Token/equity precedent: Solana Labs (AG-like company) vs Solana Foundation (Zug); Interchain Foundation (Zug) owning Cosmos Labs; Zcash → ZODL raised $25M equity seed from Paradigm/a16z in Mar 2026 around a fair-launch chain. [CoinDesk](https://www.coindesk.com/business/2026/03/09/josh-swihart-s-zcash-open-development-lab-raises-usd25-million-in-seed-funding) [M]. Swiss AG needs CHF 100k capital (50k paid in).

### Illustrative month-36 model (assumptions, not sourced)
| | Conservative | Base | Upside |
|---|---|---|---|
| Orama paying teams × $/mo | 300 × $60 | 1,500 × $80 | 6,000 × $100 |
| Orama software ARR (GM 80–85%) | $0.22M | $1.44M | $7.2M |
| Orama One units × $600 (GM ~30%) | 0 | 1,000 → $0.6M | 8,000 → $4.8M |
| RootWallet MAU × ARPU | 50k × $2.5 | 250k × $6.7 | 1M × $11.5 |
| RootWallet revenue | $0.13M | $1.68M | $11.5M |
| **Total** | **~$0.3M** | **~$3.7M** | **~$23.5M** |

### Margin story
1. Operators bring and pay for the hardware → platform fees are close to pure software margin (inference: 80%+; to be proven with real cost data). Owned-infra peers only reach 55–72%.
2. No capex. DigitalOcean funds data centres to get 55%.
3. Wallet swap fees are proven but cyclical; subscription vault + developer agent is the stabiliser (password-manager model, not wallet model).
4. Zero-premine token → investors' value is 100% company cash flow. Clean, but they will ask what binds the company to the protocol.

### Questions investors will ask
- Orama is AGPL and BYO-nodes: what stops a team self-hosting for free? → needs a clear paid layer.
- Can you reach 30–50k free developers for 1,500 paying teams?
- Orama One volumes need pre-orders as evidence.
- Two products, two models at seed → focus.

## 5. Cost structure (added 2026-10-03)

- DigitalOcean FY25: 60% gross margin, 1,462 staff, $901M revenue. [10-K](https://www.sec.gov/Archives/edgar/data/1582961/000158296126000019/docn-20251231.htm) [H]
- Cloudflare FY25: 74.5% gross margin; Q2'26 71.8%. [10-K/A](https://www.sec.gov/Archives/edgar/data/0001477333/000147733326000026/cloud-20251231.htm) [H]
- Railway: about 30–35 staff for 2–3M users; claims about 70% margin on its own metal. [Latent Space](https://www.latent.space/p/railway) [M]
- Exodus FY25: $121.6M revenue; tech + development + support $62.9M; G&A $66.3M; about 215 FTE; net loss $11.4M; about $86 opex per MAU; cut 25% of staff in Jul 2026. [8-K](https://www.sec.gov/Archives/edgar/data/1821534/000182153426000008/exod-20260311xexx991.htm) [H]
- RPC list prices: Alchemy $0.525 per million CU [pricing](https://www.alchemy.com/pricing) [H]; Helius $49–999/mo tiers [pricing](https://www.helius.dev/pricing) [H]. At 1M MAU and 500 calls each, that is about $30–63k a year (about $0.03–0.06 per MAU), rising to about $0.6 at 10× usage.
- Lean companies: Hyperliquid about 11 people and $844M revenue (trading fees) [M]; Jupiter $184M revenue [M].
- Challenge case: companies behind decentralized networks are not small. Akash/Overclock has about 127 staff on about $3M; Helium/Nova about 128 on about $18M; Uniswap Labs about 236 and turned its interface fee to 0 in Dec 2025. [W/M]
- Password managers are not lean per head: 1Password about 2,800 staff on $400M ARR; Bitwarden about 281. [M/W]
- Confidential compute hardware:
  - AMD SEV-SNP needs EPYC 7003/8004/9004+. EPYC 4004/4005 and Ryzen are not supported, and consumer Ryzen lost TSME. [Ubuntu](https://ubuntu.com/server/docs/how-to/virtualisation/sev-snp/), [TechSpot](https://www.techspot.com/news/112791-amd-quietly-disabled-ram-encryption-consumer-ryzen-cpus.html) [M]
  - Intel TDX is Xeon Scalable 3rd–5th gen / Xeon 6 only. [Intel](https://www.intel.com/content/www/us/en/support/articles/000099708/processors/intel-xeon-processors.html) [H]
  - The cheapest SEV-SNP box is an EPYC 8004 1U at about $2.5–4.5k. [Supermicro](https://store.supermicro.com/us_en/1u-amd-epyc-8004-compact-server-as-1115s-fwtrt.html) [M]
  - So a consumer-priced confidential mini PC is not feasible today.
- Illustrative company cost at 1M MAU / 5k operators / 250k developers: team of 12 plus infrastructure, RPC, audits, legal and insurance comes to about $3.9–4.7M a year, or about $4–5 per MAU. That compares with about $86 per MAU at Exodus.

## 6. Orama One hardware (added 2026-10-03)

- The existing design is Rev A (orama-one/hardware): a 270×185×94 mm aluminium unit (4.7 L) with a COM-HPC Mini module bay at 15–35 W. The SoC is not chosen yet; the design requires TME/TSME memory encryption.
- No module that fits that bay can run real confidential VMs.
  - The COM-HPC Mini options are Intel Core Ultra (TME only, no TDX) and Qualcomm Dragonwing (no Arm CCA).
  - Example: ADLINK COM-HPC-mMTL devkit, $2,233. [ADLINK](https://shop.adlinktech.com/products/com-hpc-mini-type-meteor-lake-devkit-with-com-hpc-mmtl-125h-16g-with-carrier-thsf-and-accessories) [H]
- Real confidential VMs need server-class CPUs.
  - AMD SEV-SNP: EPYC 7003/8004/9004+. EPYC 4004/4005 lack SEV. Cheapest route: EPYC 8024P ($409 list, 90 W) on an ASRock Rack SIENAD8UD3 board (~$522). [Newegg](https://www.newegg.com/asrock-rack-sienad8ud3-supports-amd-epyc-8004-series-processors/p/N82E16813140154R) [M]
  - Intel TDX: 5th-gen Xeon / Xeon 6, needs 8 DIMMs per socket. Phala's reference server costs from ~$3,763. [Phala docs](https://docs.phala.com/dstack/hardware-requirements) [H]
- Arm CCA: no shipping silicon you can buy. RISC-V CoVE: draft spec only. TrustZone on SBCs doesn't isolate tenant VMs.
- Physical attacks break every TEE.
  - Battering RAM: a ~$50 DDR4 interposer. [The Hacker News](https://thehackernews.com/2025/10/50-battering-ram-attack-breaks-intel.html) [M]
  - TEE.fail: a DDR5 interposer under $1k. It breaks TDX and SEV-SNP on patched machines. [tee.fail](https://tee.fail/) [H]
  - Intel and AMD say physical attacks are out of scope. So "the owner can't read tenant memory" can't be promised on a box in someone's home.
- TME/TSME protect against theft at rest (cold boot, pulling DIMMs) but give no isolation from the OS. AMD removed TSME from consumer Ryzen in June 2026, then restored it. [TechPowerUp](https://www.techpowerup.com/350142/amd-to-restore-tsme-memory-encryption-on-consumer-ryzen-processors-after-backlash) [M]
- Candidate designs (estimates from component prices, not quotes):
  - (c) Rev A brick, Core Ultra TME: BOM $600–900, retail $899–1,199, ~30–35% gross margin. Promises: encrypted disk and RAM against theft, sealed OS, attested boot.
  - (a) EPYC 8024P SEV-SNP small server: BOM $1.5–1.9k, retail $2.4–2.9k, ~35% gross margin. Promises hardware VM isolation against remote and software attackers, but not against the physical owner.
  - (b) Xeon TDX 1U: BOM $4.3–5k, retail $5.9–6.9k, Phala-compatible.
- Recommended deck wording: the brick is a privacy-hardened node (sealed, encrypted, no shell). Tenants who need confidentiality go on attested server-class nodes. Never claim the owner can't read tenant data.

## 7. Wallet problem stats (added 2026-10-03)

- Wallet drainer phishing in 2025: $83.85M lost by about 106,000 victims (down 83% from $494M in 2024). Permit signatures caused 38% of losses in incidents over $1M. [Scam Sniffer](https://drops.scamsniffer.io/scam-sniffer-2025-crypto-phishing-losses-fall-83-to-84-million/) [M]
- Personal wallet compromises in 2025: 158,000 incidents against 80,000 unique victims, almost 3× the 54,000 incidents of 2022; $713M stolen. Total crypto theft in 2025 was $3.41B. [Chainalysis](https://www.chainalysis.com/blog/crypto-hacking-stolen-funds-2026/) [H]
