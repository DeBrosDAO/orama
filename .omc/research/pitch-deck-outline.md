# Pitch deck outline: Orama Network + RootWallet

**For:** Verdict Capital (first institutional money; generalist; AI and consumer tilt; pre-category companies)
**Format:** PDF, 16:9, black monochrome brand (Inter / Inter Tight / JetBrains Mono, amber only for "not yet")
**How to use this:** each slide has a goal, the one message to land, what goes on it, and what to avoid. Write your text under **Your text**, then I'll tighten it and check every claim against the code and the sources in [pitch-deck-research.md](pitch-deck-research.md).

**Rules for every slide**
- One message per slide. At most ~40 words of body text; the rest is a visual.
- Every number has a source (footnote or appendix).
- "Built" means it's in the code and running. Anything else is labelled "next" (amber).
- No names, no AnChat ownership claim, no "independent customer" claim.

---

## 1 · Title
- **Goal:** tell them in one line what this is.
- **Message:** a private, decentralized cloud and L1, plus the wallet that is your key to it.
- **On it:** logo, one line, "Pre-seed · €1.5M · Zug, Switzerland", contact info@orama.network.
- **Avoid:** buzzword lists ("Web3 AI DePIN cloud").
- **Your text:**

## 2 · Problem
- **Goal:** the pain of the developer we target, not the enterprise.
- **Message:** small teams build on rented land. A landlord can switch you off, read your data, and reprice you whenever it wants. Crypto is still bolted on from outside.
- **On it (pick 3):**
  - Railway, 19 May 2026: Google wrongly suspended Railway's account, and every app on Railway went down for 8 hours.
  - AWS us-east-1, 20 Oct 2025: down for 15+ hours.
  - Cloudflare, 18 Nov 2025: global outage.
  - Vercel bill shocks and 4 repricings since 2024; Heroku frozen in Feb 2026.
  - Bybit, Feb 2025: $1.5B stolen because the signers couldn't see what they were signing.
- **Avoid:** "we compete with AWS". Say who we serve: solo developers and small teams on Vercel and Supabase-style tools.
- **Your text:**

## 3 · Solution: one network, one economy
- **Goal:** the whole system in one diagram.
- **Message:** three layers that need each other.
  - RootWallet: who you are.
  - Orama cloud: where your app runs.
  - ORAMA L1: how the network pays the people who run it.
- **On it:** the layer diagram, plus the loop:
  - developers deploy;
  - operators run the nodes and earn;
  - users sign in and pay with RootWallet.
- **Avoid:** feature lists. Those go on slides 4–6.
- **Your text:**

## 4 · Orama Network
- **Goal:** what developers get, and why it's different.
- **Message:** a decentralized, privacy-first cloud. Anything built on it is decentralized and private by default.
- **On it:**
  - **The building blocks:**
    - hosting (static, Next.js, Node, Go)
    - replicated SQL database
    - cache
    - file storage
    - serverless functions
    - real-time messaging
    - voice and video (TURN/SFU/WebRTC)
    - push notifications
    - domains and HTTPS
    - built-in Tor anonymity
    - wallet login
  - **The cluster grows** from 3 to 5, 7, 9+ nodes. Every app is replicated, so it survives a node dying.
  - **Proof:** an encrypted messenger with voice and video runs entirely on these building blocks.
  - **Next (amber):** Orama One confidential nodes let developers rent other operators' hardware privately.
- **Avoid:**
  - Custom-domain certificates aren't issued yet, so don't claim them.
  - Don't say developers pick 5/7/9 replicas *per app*. Each app's database runs on 3 replicas inside the cluster.
- **Your text:**

## 5 · ORAMA L1
- **Goal:** show the chain is real and has a purpose, not a token grab.
- **Message:** fast finality, smart contracts like Ethereum, private transactions like Zcash, and an app ecosystem, because every app also gets a full backend.
- **On it:**
  - **Engine:** Cosmos SDK + CometBFT, with CosmWasm smart contracts.
  - **Private payments:** a shielded pool built on Zcash's audited Orchard circuits.
  - **Zero premine:** no founder, team or investor tokens. Operators earn ORAMA by providing storage, relay and validation.
  - **No admin keys:** two-house governance (token holders plus operators). Nobody, including us, can pause or freeze the chain.
  - **Extras:** compressed NFTs and user tokens.
  - **Status:** built in code, running on stagenet; mainnet after audits.
- **Avoid:**
  - "Faster than Ethereum" with no benchmark. Either we run one, or we say "seconds to finality".
  - Any price talk.
- **Your text:**

## 6 · RootWallet
- **Goal:** THE developer wallet, and the way into the network.
- **Message:** one seed phrase covers crypto, passwords, SSH keys and 2FA. It's built for developers and for AI agents.
- **On it:**
  - **Chains:** BTC, EVM, Solana and ORAMA natively, with HD accounts.
  - **Vault:** passwords, SSH keys and TOTP, all derived from the same seed.
  - **Desktop agent:**
    - CLIs and AI agents ask the wallet to sign.
    - A human approves every request.
    - The wallet checks *which program* is asking, using the operating system.
    - It decodes and shows the transaction, so nothing is blind-signed.
  - **Platforms:** desktop (macOS/Linux), `rw` CLI, mobile (in development).
- **Avoid:**
  - "Safest wallet" as an unproven superlative. Show the mechanisms; the audit is funded in this round.
  - Swaps, until the code is back on main (see open questions).
- **Your text:**

## 7 · How they work together (the flywheel)
- **Goal:** why the two products ship together.
- **Message:** every Orama developer, operator and end user needs a wallet, and RootWallet is built in.
- **On it:**
  - Sign in to Orama with a signature: no email, no password.
  - Operators keep their SSH keys and node secrets in the wallet.
  - Build archives are signed by the wallet and verified by every node.
  - Rewards for operators and payments for users go through the same wallet.
- **Your text:**

## 8 · Real R&D, not vibe coding
- **Goal:** the team and its process are the moat.
- **Message:** built from research, not templates. The architecture is ours.
- **On it (proof points, all checked in the repo):**
  - **Research first:** three written design studies before the chain was coded (engine options, full feature design, decentralization and size).
  - **No Docker or Kubernetes:** each app gets its own database cluster, isolated with systemd, kept in shape by a self-healing loop.
  - **Self-audits:** 150+ findings from our own security reviews, fixed in code.
  - **Tests:**
    - ~10,700 unit tests and 81 end-to-end suites, including chaos tests that kill nodes and cut the network;
    - a test that fails the build if the docs claim something the code can't do.
  - **Pace:** ~2,600 commits in 14 months across both products, by 3 people with no outside funding.
- **Visual:** the architecture diagram.
- **Avoid:** lines-of-code counts, and the word "AI-generated" anywhere.
- **Your text:**

## 9 · Live in production
- **Goal:** it works with real users.
- **Message:** a production messenger has run entirely on Orama + RootWallet for 6 months.
- **On it:**
  - **Usage depth:** ~120 serverless functions, voice and video calls, push, encrypted attachments, and 2.8M+ database writes.
  - **Reliability:** 78 days with zero downtime.
  - **Users:** ~1,000 downloads and ~150 active users. Show these small, and lead with the usage depth.
  - **What funding unlocks:** this app can grow the network fast.
- **Avoid:** saying the app is independent, or calling it an external customer.
- **Your text:**

## 10 · Market
- **Goal:** a big, growing, reachable market.
- **Message:** 16M developers work solo or in small teams, AI is multiplying the apps they ship, and developer clouds are valued at $1.5–10.5B.
- **On it:**
  - 47.2M developers, of whom ~16M work solo or in small teams.
  - More than 60% of new Supabase databases are created by AI tools.
  - Supabase: $10.5B valuation, 10M developers, ~$170M ARR. Vercel: $9.3B.
  - 741M crypto owners; 1Password $400M ARR.
  - **Market size, bottom-up:**
    - TAM (everyone who could buy): $7.5B
    - SAM (the segment we can reach): $0.5B
    - SOM (what we can win by year 3): $4M ARR
- **Avoid:** top-down "$419B cloud" and "$264B PaaS" figures.
- **Your text:**

## 11 · Competition
- **Goal:** an empty quadrant.
- **On it:** two 2×2 charts.
  - **Orama:** full developer stack × no vendor can switch you off.
    - Vercel, Supabase and Railway have the full stack, but a vendor controls them.
    - Akash and Flux are user-controlled, but sell raw servers only.
    - Orama is alone in the top-right.
  - **RootWallet:** native BTC+EVM+SOL × vault.
    - Phantom and Exodus have the chains but no vault.
    - 1Password and Bitwarden have the vault but no chains.
    - RootWallet is alone in the top-right.
- **Avoid:** saying nobody does agent signing. MetaMask, Coinbase, Phantom and 1Password all shipped it in 2026. Our edge is local, self-custodial approval combined with the vault.
- **Your text:**

## 12 · Business model & cost structure
- **Goal:** many revenue lines on a tiny cost base.
- **Message:** the network's servers are paid for by the people who run them. Our costs are the team plus a handful of servers, so margins look like software, not like a cloud.
- **On it:**
  - **Revenue lines:** wallet subscriptions; wallet swap, on-ramp and staking fees; Orama One hardware; take on the compute marketplace; network fees (see open question 1).
  - **Cost comparison:**
    - DigitalOcean owns its servers and keeps 55% gross margin; Cloudflare 72%.
    - We own no servers.
    - Cost per user comes from the cost research (pending).
  - **Revenue scenarios at month 36:** $0.3M conservative, $3.7M base, $23.5M upside (assumptions in the appendix).
- **Avoid:** "10M developers means more ARR than Supabase". Developers who self-host pay *us* nothing. They earn from the network. Our edge is **margin and resilience**, not revenue per developer. Revenue comes from the wallet, hardware and the marketplace.
- **Your text:**

## 13 · Roadmap (18 months)
- **Goal:** what this money buys, and when.
- **On it (months after funding):**
  - **0–3:** Swiss AG set up; team of 5; testnet open to developers; wallet subscriptions and swaps live; revenue starts.
  - **3–9:** RootWallet external audit; mobile on both app stores; Orama One prototype with pre-orders.
  - **9–12:** in-house audits of Orama and the ZK integration finished; mainnet for both products.
  - **12–18:** first Orama One batch shipped; compute marketplace on confidential nodes; next round.
- **Avoid:** dates you can't hit. Confirm each one.
- **Your text:**

## 14 · Team
- **Goal:** these founders have done this before.
- **Message:** three engineers with 8+ years building multi-tenant infrastructure for other people. Orama is that experience turned into a product.
- **On it:**
  - **Founder 1, architect and platform engineer:**
    - built a multi-tenant platform from scratch with per-tenant infrastructure; the business was acquired;
    - built an agentic-AI platform for regulated finance.
  - **Founder 2, full-stack and AI engineer:**
    - postgraduate degree in AI; led a team of 5;
    - shipped native apps for an Athens-listed company.
  - **Founder 3, network engineer:** certifications and background still needed.
  - **Hiring:** a cryptography engineer and a security engineer.
  - **Track record:** our own consumer app is live on both app stores.
- **Your text:**

## 15 · The ask
- **Message:** €1.5M equity, 18 months, a team of 5, to get to production and real revenue.
- **On it:** the use-of-funds chart.
  - founders: 540k
  - two hires: 190k
  - RootWallet audit: 80k
  - Orama One: 150k
  - servers and infrastructure: 80k
  - Swiss AG and legal: 100k
  - travel: 80k
  - relocation: 40k
  - contingency: 240k
- **Avoid:** putting the valuation on the slide.
- **Your text:**

## Appendix
- **A1 · Security architecture:** the wallet agent's trust model, how keys are derived from the seed, and how build archives are signed and checked.
- **A2 · Chain design:** emission schedule, governance, the shielded pool, fees.
- **A3 · Risks and answers:** regulation of private transactions, single-cluster scale, key-person risk, focus.
- **A4 · Revenue model assumptions:** the month-36 scenario inputs.
- **A5 · Sources.**

---

## Open questions (they block slides 6, 12 and 13)

1. **Network fees.** As the chain is coded today, the company earns *nothing* from L1 fees:
   - the base fee is 100% burned and tips go to the block proposer (`x/fees`);
   - NFT market sales pay only royalty and seller, with no protocol cut (`x/market`);
   - zero premine;
   - the only company-adjacent money is the 5% development ceiling, and only a vote of both governance houses can mint it (`x/emission/keeper/development.go`, `x/houses`).

   So "we earn from network fees" must become one of:
   - (a) the company runs validators and nodes and earns like any operator;
   - (b) development-fund grants voted by governance;
   - (c) a service fee charged in RootWallet or on the marketplace (an interface fee, like Uniswap Labs).

   Which ones?
2. **Swaps.** LI.FI swaps existed but were removed from `main` on 2026-09-10 (commit `e329f90`, "cut WalletConnect, LI.FI swap and the vault notes/cards UI"). They survive only on the old branch `jarvis/host-port-blocks`. Restore them before the pitch, or say "re-enabling"?
3. **Revenue before mainnet.** Testnet tokens have no value, so what pays before mainnet: wallet subscriptions, swaps, Orama One pre-orders, paid managed clusters?
4. **Wallet servers.** RootWallet calls Helius, Alchemy, mempool.space and CoinGecko, which are paid at scale. Serve these from the Orama network itself, so operators carry the cost and "no servers" holds?
5. **Founder 3's** certifications and background, for slide 14.
