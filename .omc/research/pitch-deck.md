# Orama Network & RootWallet

Seed round. $1.75M equity. Zug, Switzerland.
info@orama.network

Draft, 3 October 2026. Items marked [CONFIRM] still need an answer before this goes out.

---

## At a glance

### The products are already built.
Orama Network, the ORAMA L1 and RootWallet are working and tested today. This round pays for going to market, not for building. From here the engineering work is audits and bug fixing.

### Revenue starts in month 3, not after mainnet.
RootWallet subscriptions and swaps go live within three months of funding. Mainnet means "audited and out of beta". It is not the day the money starts.

### Already live, with real users.
AnChat (anchat.io), an encrypted messenger with voice and video calls, runs its entire backend on Orama and its accounts on RootWallet. It has been live for six months, it sells a paid subscription in crypto, and the network has run 87 days with zero downtime.

### Three products, one economy.
- **Orama Network:** a privacy-first, decentralized cloud.
- **ORAMA L1:** our own blockchain, with private transactions and smart contracts.
- **RootWallet:** one wallet for crypto, passwords, SSH keys and two-factor codes.

Every developer, operator and user of the network needs the wallet, and every wallet user can take part in the network.

### Nobody can shut us down.
We don't run on AWS, Google or Cloudflare. Orama runs its own DNS, its own certificates, its own call relays and its own blockchain on independent nodes, and everything is paid in crypto. There is no account for anyone to suspend.

### Software margins, no server bill.
The people who run the network pay for its servers, and the network pays them back. RootWallet runs on the user's own device. Our costs are a small team and a handful of servers. A cloud that owns its servers keeps 55–72% gross margin. We don't own any.

### A team that ships.
Three engineers built all of this in 14 months, with about 2,600 commits and no outside money. We have raised before, more than £500,000, and sold our stake a year later.

### A fair launch, so the equity is the investment.
ORAMA has no premine and no allocation for founders or investors. All of the company's value sits in its revenue, which makes this a clean equity investment in the business that earns from the network.

### Privacy, with a business model.
Privacy matters more now than ever. Clouds suspend accounts, wallets get drained by transactions people can't read, and AI is producing more apps than ever. We deliver privacy as working infrastructure that earns money, and investors can share in that.

---

## 1. The problem

### Developers build on rented ground

Most software today is built by solo developers and small teams. They ship a SaaS product, a mobile app, or a side project that turns into a business. They don't build on their own ground. They rent it from a few platforms, and those platforms can switch them off, read their data, and change the price whenever they like.

This keeps happening. On 19 May 2026 Google Cloud wrongly suspended Railway's account, and every app on Railway went down for eight hours. In October 2025 an AWS region went down for more than fifteen hours. In November 2025 a single faulty file took Cloudflare down across the world. Vercel has changed its pricing four times since 2024. Heroku stopped building new features in February 2026.

Crypto is still bolted on from outside. A developer who wants wallet login and crypto payments has to stitch together a cloud, a database, an auth provider, an RPC provider and a payment service from five different companies.

### Wallets don't protect the people who use them

Popular wallets such as MetaMask and Phantom hold crypto keys and nothing else. Passwords live in one app, two-factor codes in another, SSH keys in a file on the laptop. Every extra app is another place to be attacked.

Most theft happens at the moment of signing. A wallet asks you to approve something you can't actually read.
- In February 2025 the signers at Bybit approved what looked like a routine transaction, and $1.5 billion was stolen.
- Wallet drainers hit about 106,000 victims in 2025, and malicious "permit" signatures caused 38% of the largest losses (Scam Sniffer).
- Chainalysis counted 158,000 personal-wallet compromises in 2025 against 80,000 victims, almost three times the number in 2022.

The software around wallets is under attack too:
- In September 2025 a compromised set of npm packages swapped crypto addresses inside apps.
- In December 2025 a poisoned update to the Trust Wallet browser extension drained about $8 million.
- Password managers aren't safe either. Vaults stolen from LastPass in 2022 are still being cracked, with more than $35 million taken so far.

AI agents now need to sign transactions and use credentials. Today the usual way to give them access is to paste a private key or an API secret into a config file.

The business model is fragile as well. Most wallets are free and live on swap fees, so their revenue rises and falls with the market. Exodus lost a third of its monthly users in 2025.

### How we solve both

| The problem | Our answer |
|---|---|
| A vendor can switch your app off. | Orama runs on independent nodes with its own DNS, certificates, relays and chain. There is no account to suspend. |
| Your data sits with someone else. | Apps on Orama are private by default: an encrypted mesh, files encrypted before upload, relay-only calls, and built-in Tor. |
| Crypto is stitched together from five companies. | Wallet login, a database, functions, storage and a chain with private payments all come from one network. |
| Keys, passwords, two-factor codes and SSH keys live in different apps. | RootWallet holds all of them, derived from one seed, on your own device. |
| You sign transactions you can't read. | RootWallet decodes every transaction and shows it before signing. Nothing is signed blind. |
| Malicious software asks for signatures. | RootWallet's agent checks which program is asking, using the operating system, and pins its approval to that exact binary. |
| AI agents get raw keys. | Agents ask RootWallet for each signature, and a human approves every request. The key never leaves the wallet. |
| Wallet revenue swings with the market. | RootWallet earns from subscriptions to the vault and developer features as well as from swaps. |

## 2. What we built

We built one network with three layers that depend on each other.

RootWallet is who you are. One seed phrase holds your crypto, passwords, SSH keys and two-factor codes, and it signs you in to everything on the network.

Orama is where your app runs. It is a decentralized, privacy-first cloud. Developers get everything a modern app needs, running on nodes that they or independent operators own.

The ORAMA L1 is how the network pays the people who run it. Operators earn for providing storage, relay and validation, and users can pay each other privately.

Developers deploy apps. Operators run the nodes and earn. Users sign in and pay with RootWallet. Each new developer brings users, each new user needs a wallet, and each new app needs more nodes.

The first app on the network is already live. AnChat (anchat.io) is an encrypted messenger with voice and video calls, and its entire backend runs on Orama and RootWallet. It has a working paid subscription, and subscribers can pay in crypto, including ORAMA tokens. Every AnChat subscription paid in ORAMA creates demand for the token and strengthens the network.

## 3. The products are built

This is the most important thing to know about us. We are not raising money to build a product. The products exist, they are tested, and real people use them.

- **Orama Network** runs on our devnet and testnet and carries a production messenger every day.
- **The ORAMA L1** is built and running on our stagenet. That covers smart contracts, private transactions, governance, storage deals and operator rewards.
- **RootWallet** works on macOS and Linux desktop, on mobile, and as a command-line tool, with Bitcoin, EVM, Solana and ORAMA.

The money goes to four things: independent audits so people can trust us, people to sell the product and design it well, the first Orama One hardware, and setting up the company in Switzerland.

## 4. Orama Network

Orama is a decentralized, privacy-first cloud. Developers get the building blocks every modern app needs, all through one CLI and SDK:

- hosting for static sites, Next.js, Node and Go, with one-command rollback
- a replicated SQL database, plus a separate SQLite database per app
- a distributed cache
- file storage on IPFS, with files encrypted before they leave the device
- serverless functions, triggered by HTTP, WebSockets, pub/sub messages or a schedule
- real-time messaging and presence
- voice and video, through our own TURN and SFU servers
- stealth calls, where call traffic looks like ordinary HTTPS, so calls work even on networks that block them
- push notifications to iOS, Android and the web, including our own self-hosted push server
- our own DNS servers, so domains need no Cloudflare or any other provider
- domains with HTTPS
- a secrets manager for API keys and credentials
- built-in Tor anonymity for outbound requests
- wallet-based login, with no emails and no passwords

These blocks combine into any kind of app. Because they run on independent nodes, every app built from them is decentralized and private by default, with no extra work.

A developer's cluster starts at three nodes and grows to five, seven, nine or more. Every app is replicated across the cluster, so when a node dies the app keeps running. Nodes talk to each other only through an encrypted WireGuard mesh.

### What people can build

- **Private messengers and communities.** AnChat already does this.
- **Video consultations and support lines.** Calls are relayed, so callers never see each other's IP address.
- **SaaS products** with wallet login and payments in crypto built in.
- **Games** with real-time multiplayer, and in-game items as NFTs on the ORAMA chain.
- **Creator and membership platforms**, with subscriptions paid in crypto.
- **Backends for AI agents.** Functions, a database and storage, with every payment and key signed through RootWallet.
- **Encrypted file sharing and backups.**
- **Apps for people on censored networks.** Stealth calls and built-in Tor keep them reachable.

### Live today: AnChat

AnChat (anchat.io) is a live, working app built on these technologies. Its whole backend runs on Orama:

- about 120 serverless functions
- the database
- encrypted attachments
- push notifications
- voice and video calls

The network has run for 87 days with zero downtime.

### Next: Orama One

Today developers bring their own nodes. The next step is Orama One, our own hardware node: a small, silent box you plug into power and the internet. It runs a sealed operating system with no shell to log into. Its memory and disk are encrypted, and the disk key is split across other nodes. Once Orama One nodes are on the network, developers can rent capacity on other people's nodes and pay in crypto, the way they rent a server today. Apps that need hardware-enforced confidential computing go on server-class confidential nodes (AMD SEV-SNP or Intel TDX).

Orama is not trying to replace AWS. A company that needs five hundred servers won't come to us. The other 99% of developers, who want to ship their product with privacy and crypto built in, will.

## 5. Nobody can shut us down

Every other developer cloud depends on someone else. Vercel runs on AWS. Railway was taken down by Google. Most of the internet goes through Cloudflare.

Orama depends on none of them.

- **Machines.** The servers belong to independent operators, not to us and not to any one provider.
- **DNS and certificates.** We run our own DNS and issue our own certificates.
- **Calls.** Voice and video go through our own relays.
- **Payments.** Payments and rewards run on our own chain. Supporting payments with BTC, SOL, EVM and ORAMA
- **No admin keys.** Nobody holds a key to the chain, and nobody can pause or freeze it, including us.
- **Paid in crypto.** There is no bank account or card processor that can be cut off.

If we disappeared tomorrow, the network would keep running.

## 6. The ORAMA L1

ORAMA is our own layer-one blockchain. It is built and running on our stagenet.

It runs on Cosmos SDK and CometBFT, so blocks finalize in seconds [CONFIRM: benchmark]. It supports smart contracts through CosmWasm. Private transactions use a shielded pool built on Zcash's Orchard circuits, which have already been audited. Compressed NFTs and user-issued tokens are built in.

Its economics are deliberately fair. There is no premine and nothing is allocated to founders, the team or investors. Every ORAMA in existence will have been earned by someone running the network. Emission halves every two years, then settles into a small permanent tail.

There are no admin keys. Governance has two houses, one for token holders and one for node operators, with timelocks. Nobody, including us, can pause or freeze the chain.

What sets it apart from other chains is the app ecosystem. On other chains a smart-contract developer still needs a server, a database and a storage provider from somewhere else. On Orama every app also gets a full backend from the same network: database, functions, storage and real-time messaging.

## 7. RootWallet

RootWallet is the wallet for developers, and the way into the Orama network.

One seed phrase holds everything. It supports Bitcoin, every EVM chain, Solana and ORAMA natively, with HD accounts. From the same seed it also holds your passwords, your SSH keys and your two-factor codes. Nothing leaves your device.

On the desktop, RootWallet runs a local agent. Command-line tools and AI agents ask it for keys and signatures, and a human approves each request. Before it answers, the agent checks *which program* is asking, using the operating system itself, not information the program supplies. It pins its approval to that program's exact binary. It decodes every transaction and shows it in plain terms before signing, so nothing is ever signed blind. That is the failure behind the Bybit theft. Release builds can only be signed for their stated purpose, so a hostile server can't trick the wallet into signing something else.

This is how we build our own products. AI agents deploy to Orama and sign with RootWallet, and a human approves every step.

RootWallet runs on macOS and Linux desktop, on iOS and Android, and as a command-line tool. It supports swaps and subscriptions for the vault and developer features. Next come on-ramp and staking.

## 8. How the two products work together

Every interaction with Orama goes through RootWallet.

- Developers sign in with a wallet signature instead of an email and password.
- Operators keep their SSH keys and node secrets in the wallet.
- Every release we build is signed in the wallet and checked by every node before it installs.
- Operator rewards and user payments on the ORAMA chain go through the same wallet.

That is why the products ship together. Every developer, operator and end user of the network needs a wallet, and each RootWallet user can take part in the network.

## 9. Real research, not vibe coding

Nothing here was assembled from templates. Each part of the system was researched, designed and tested before it shipped.

- **Research first.** Before writing the chain we wrote three design studies: the choice of engine, the full feature design, and decentralization and chain size.
- **Our own isolation model.** Orama uses no Docker and no Kubernetes. Each app gets its own replicated database cluster, isolated by the operating system and kept healthy by a loop that repairs it continuously.
- **Security reviews.** Our own reviews produced more than 150 findings, and they were fixed in code.
- **Tests.**
  - About 10,700 unit tests.
  - 81 end-to-end test suites, including chaos tests that kill nodes and cut the network in the middle of operations.
  - A test that fails the build if the documentation claims something the code can't do.
- **Pace.** Three engineers made about 2,600 commits across both products in 14 months, with no outside money.

## 10. AnChat: live in production

AnChat is an encrypted messenger, live on Android and iOS for six months. Its entire backend runs on Orama, and its accounts run on RootWallet.

- About 120 serverless functions run its features.
- Messages use post-quantum key exchange.
- It has voice and video calls through Orama's own relays, encrypted attachments, and push notifications.
- It has made over 2.8 million database writes on the network.
- It has a paid subscription, paid in crypto.
- It has about 1,000 downloads and 150 active users.

The user numbers are small. The point is that a real app, with real users and real payments, runs on our stack every day.

**Try it yourself.** Download AnChat from anchat.io, sign in with RootWallet, and message us there. It's the quickest way to see everything in this document working. [CONFIRM: the AnChat handle or contact investors should message]

## 11. Privacy, at the right time

There are 47.2 million developers in the world, and about 16 million of them work alone or in small teams (SlashData, 2025). They are the people we build for.

AI is multiplying the apps they ship. More than 60% of new databases on Supabase are now created by AI tools. Lovable alone hosts more than 60 million projects. Every one of these apps needs somewhere simple, cheap and safe to run.

Investors already value this market highly:

- Supabase raised at $10.5 billion in June 2026, with about 10 million developers.
- Vercel raised at $9.3 billion. Render raised at $1.5 billion. Railway passed two million users.

Wallets and vaults are just as large. There are 741 million crypto owners. MetaMask has more than 30 million monthly users, and Phantom was valued at $3 billion. 1Password passed $400 million ARR.

Privacy has moved from a niche to a demand. The share of Zcash supply held in shielded form rose from about 11% to about 30% in just over a year. Developers have watched clouds suspend accounts, wallets drained by transactions their owners couldn't read, and password vaults cracked years after a breach.

We have the right products for this moment. A huge market is moving toward privacy, and we offer it as working infrastructure with a real business model. Investors can make money from it.

## 12. Competition

The developer clouds have the full stack but sit on rented ground. Vercel, Supabase, Railway and Render can all be switched off by a vendor, and they can reprice whenever they want. The decentralized compute networks can't be switched off, but they sell raw servers, not a developer platform. Akash earned about $253,000 in the first quarter of 2026. The decentralized compute sector as a whole earned about $72 million in 2025. Decentralized cloud didn't fail for lack of demand. It failed on developer experience.

Orama is the only platform we found that offers the full developer stack on infrastructure nobody can switch off.

Wallets are split the same way. Phantom, Exodus and Coinbase Wallet hold crypto keys and nothing else. 1Password and Bitwarden hold passwords and nothing else. RootWallet is the only product we found that holds Bitcoin, EVM and Solana keys alongside passwords, SSH keys and two-factor codes, all from one seed.

Signing for AI agents is now crowded. MetaMask, Coinbase, Phantom and 1Password all shipped agent features in 2026. Our difference is that approval is local and self-custodial, and it comes with the vault in the same product.

## 13. Business model: software margins

We make money from several lines that reinforce each other:

1. **RootWallet subscriptions** for the vault and developer features: passwords, SSH keys, two-factor codes and the desktop agent. Password managers charge $2–4 a month, so this is software revenue with almost no cost behind it.
2. **RootWallet transaction fees** on swaps (the market standard is 0.85%), on-ramp partnerships and staking.
3. **Orama One hardware.** A node you plug in that joins the network, earns rewards and hosts apps. The design is already done (Rev A): a 4.7-litre aluminium unit drawing 15–35 W, expected to retail around $899–1,199 at roughly a 30–35% gross margin. A confidential edition on AMD EPYC, at about $2,400–2,900, can follow for operators who host the most sensitive apps. Both prices are estimates from component costs, to be confirmed with suppliers.
4. **The compute marketplace.** When Orama One nodes are live, developers rent capacity on other operators' nodes, pay by usage in crypto, and we take a share.
5. **Network participation.** [CONFIRM: as coded today, the base fee is burned, tips go to block proposers, and the market takes no protocol fee. Choose: the company runs nodes and earns like any operator / development-fund grants voted by governance / a service fee in the wallet or marketplace.]

Our cost structure is what makes this business unusual. We don't own the network's servers. Developers and operators pay for their own nodes, and operators are paid by the network for the capacity they provide. RootWallet runs on the user's device. Our costs are the team, an office and a handful of servers. A cloud company that owns its hardware keeps 55% to 72% of its revenue as gross margin (DigitalOcean 55%, Cloudflare 72%). Our margins are those of a software company.

At scale the gap gets wider. Exodus, a self-custody wallet listed in the US, spent about $86 per monthly user in 2025 on technology and administration, with about 215 staff. Our own model puts us at about $4–5 per user, for one million wallet users, 5,000 operators and 250,000 developers. That covers a team of about twelve, a few servers of our own, the third-party services the wallet uses, audits and legal. Lean infrastructure teams already exist: Railway serves two to three million users with about 35 people.

That also makes us resilient. There is no data-centre bill, no single account to suspend, and no server we can lose.

## 14. Roadmap: revenue from month 3

**The products are built. This money takes them to market.**

What this round pays for, in months after funding:

- **Months 0 to 3.** Set up the Swiss AG and grow to a team of seven. Open the testnet to developers. Launch RootWallet subscriptions and swaps. **Revenue starts in month 3.**
- **Months 3 to 9.** Independent audit of RootWallet, the mobile app on both app stores, sales outreach to developers and operators, and an Orama One prototype with pre-orders.
- **Months 9 to 12.** Finish our own audits of Orama and the zero-knowledge integration. Mainnet for both products. Mainnet means out of beta, audited and secure. It is not the start of revenue.
- **Months 12 to 18.** Ship the first batch of Orama One nodes, open the compute marketplace, and raise the next round.


## 15. Team

We are three engineers who have spent eight years building multi-tenant infrastructure, apps and networks for other companies. Orama and RootWallet turn that experience into products.

We have done this before. We raised more than £500,000 for a previous company and sold our stake a year later.

- **Architect and platform engineer.**
  - Eight years in architecture, distributed systems, cloud infrastructure, CI/CD and delivery.
  - Built a multi-tenant platform from scratch, with infrastructure provisioned automatically for each customer. The business was later acquired.
  - Architected an agentic-AI platform for regulated finance and insurance.
  - Associate degree in software engineering; otherwise self-taught.
- **Full-stack and AI engineer.**
  - Eight years. Previously led a team of five engineers.
  - Shipped native iOS and Android apps for a company listed on the Athens Stock Exchange.
  - Co-built a custom search engine in Go for a Berlin travel platform.
  - Postgraduate degree in Artificial Intelligence, BSc in Computer Software Engineering, and Certified AI Engineer.
  - Further certifications in AI agents and chatbots, deep learning and NLP, and Python.
- **Network and infrastructure engineer.**
  - More than seven years building and running fiber-optic networks, servers and business networks for major Greek providers.
  - Certifications: Cisco CCNA, Cisco IT Essentials, Panduit CPT, FOA CFOT (Certified Fiber Optic Technician), FOA CPCT (Certified Premises Cabling Technician), Yeastar Certified Technician (S-Series and P-Series), and Aginode qualified supervisor for business installations.

Together we have delivered for product teams in Germany, the UK, Greece and Dubai, and our own consumer app is live on the App Store and Google Play.

### Hiring with this round

- **Cryptography engineer.** The shielded pool, the wallet's key management, and our own audit of the zero-knowledge integration.
- **Security engineer.** Our own audits of Orama, the bug bounty, and incident response.
- **UX/UI designer.** RootWallet, the developer dashboard and the website.
- **Sales and partnerships.** Calls, outreach and partnerships with developers, operators and apps.

That makes seven people. We don't plan to grow beyond that before the next round. The last fourteen months show how much a small team can build.

## 16. The ask

We are raising $1.75 million in equity for 18 months of runway. That takes both products to market and real revenue, with a team of seven, based in Zug with hires in Greece.

Use of funds:

| Item | Amount |
|---|---|
| Three founders, full-time in Zug | $610,000 |
| Cryptography and security engineers | $215,000 |
| UX/UI designer and sales lead | $90,000 |
| Independent audit of RootWallet | $90,000 |
| Orama One prototype and first batch | $170,000 |
| Servers, hardware and infrastructure | $90,000 |
| Swiss company, legal and accounting | $115,000 |
| Travel and networking | $90,000 |
| Relocation to Switzerland | $45,000 |
| Contingency | $235,000 |
| **Total** | **$1,750,000** |

info@orama.network

---

## Appendix

### A. Security architecture
[To write: how the wallet agent decides whom to trust, how keys are derived from the seed, how build archives are signed and verified, and how nodes verify each other on the mesh.]

### B. Chain design
[To write: the emission schedule, two-house governance, the shielded pool, fees and burn.]

### C. Risks and how we answer them
[To write: regulation of private transactions, scale beyond small and mid-sized apps, depending on a small team, keeping focus across two products.]

### D. Revenue model assumptions
[To write: the scenario inputs, as backup only. Not shown in the main document.]

### E. Sources
Full list with links: pitch-deck-research.md.

---

## Editor's notes (delete before sending)

- **Exchange rate.** USD figures use EUR/USD 1.125 (2 October 2026). The plan in euros is €1.55M.
- **New hires.** The two Greek hires are €2,000 a month each plus about 22% employer cost, from month 3: about €78k, which is $90k.
- **Degrees without institutions.** I left out the institution names so you aren't identifiable before the meeting.
- **How AnChat is described.** "Built by a partner" was replaced with a neutral description. AnChat is ours. Calling it a partner's app in a fundraising document, to an investor who will do due diligence, is a misstatement of fact. It's the kind of thing that ends deals and creates legal exposure. Two honest options:
  - leave the builder unnamed, as written above;
  - write "built by a related company", if AnChat sits in a separate company you own.

  Either way, say who built it in the meeting.
