# Orama Network & RootWallet Pitch Deck

Seed round. $1.75M equity. Doha, Qatar.
team@orama.network

3 October 2026.

---

## A Quick Overview of what you will read

### The products are already built. They are working !
Orama Network ( including L1 Blockchain ) and RootWallet are already working and beeing tested today. The money goes to bring the products to market. 95% is already built. Mostly bug fixing, security audits and minor changes will take place.

### Revenue starts in month 3, not after 18 months.
RootWallet subscriptions and fees will go live within 3 months, we are already underway for releasing the app on public distribution on iOS and Android. The same is for Orama Network, fees will start as soon as we launch Testnet. Mainnet means "audited and out of beta". The network is already working we just want to make it secure.

### AnChat Already live, with real users on Orama Network using RootWallet SDK as embedeed wallet.

AnChat.io is an PQ E2E encrypted decentralized messenger. It supports video calls, voice calls, crypto transfers and many more feature. It runs its entire bakend stack on Orama Network and it has wallet only login ( without phone or email ) using RootWallet SDK. It has been live for more than six months with subscribers and people using it daily. With 0 downtime for over 90 days.

### Two products, one ecosystem.
- **Orama Network:** a privacy-first, decentralized cloud with its own L1 Blockchain with private transactions and smart contracts.
- **RootWallet:** one wallet for crypto, passwords, SSH keys and two-factor codes (OTP).

Every developer, operator and user of the network needs the wallet, and every wallet user can take part in the network.

### Nobody can shut us down.
We don't run on AWS, GCP or Cloudflare. Orama is built from scratch with decentralization as #1 priority. We have our own DNS, Certificates, Call Relays, Onion Routing, Encrpytion, Serverless engine, failover and many more. The network can't be shut down or suspended. We do not rely on any 3rd party service.

### Great Software margins, no server bill.
The servers are paid by the people running the network and the network pays them back. RootWallet runs on the users own device it does not require a server to run. Our only costs are a handful of servers, and our operating team costs.

### A team that ships.
We built everyting since January 2025. We are 2 engineers with no outside money. We did our own RnD for everything and we started before AI so nothing is vibe coded. Everything has real research behind it.

### A fair launch, so the equity is the investment.
ORAMA Token has no pre-mine and no allocation for founders or investors. It will start just like Bitcoin started. All of the company's value sits in its revenue, which makes this a clean equity investment in the business. Of course we earn from the network by contributing nodes and taking transactions fees.

### Privacy, with a business model.
Privacy matter now more than ever. Clouds suspend accounts, wallets get drained, and AI is producing more apps than ever. Privacy is a priority for us. With that said privacy comes with a real business model that can generate profit without giving away peoples data and investors can share in that. We deliver privacy as a working infrastructure.

---

## 1. The problem

### Developers build on rented ground

Most software today is built by developers and small teams. They usually ship SaaS prodcuts, mobile apps or side projects that turn into a business. They rent servers from AWS or GCP or they use services like Vercel or Supabase. All of these things can be switched off at any time and their data is sold or used for training AI. Also pricing on these platform can be changed at any time.

This keeps happening. On 19 May 2026 Google Cloud wrongly suspended Railway's account, and every app on Railway went down for eight hours. In October 2025 an AWS region went down for more than fifteen hours. In November 2025 a single faulty file took Cloudflare down across the world. Vercel has changed its pricing four times since 2024. Heroku stopped building new features in February 2026.

Crypto is still bolted in from outside. A developer who wants wallet login or crypto payments has to stitch together a cloud, a database, an auth provider, an RPC provider and a payment service from five different companies.

### Wallets don't protect their users

Wallets such as MetaMask or Phantom hold crypto keys and nothing else. People have a seperate app for passwords, two-factor codes and SSH Keys live in an un-ecrypted file on a laptop.

Most theft happens at the signing step. A wallet ask you to approve something you can't actually verify.
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

## 2. What we have built

We build one network with 2 layers ( Network & Blockchain ).

- RootWallet is who you are. One seed phrase holds your crypto, passwords, SSH keys and two-factor codes and it signs you in to everything on the network that you own.
- Orama is where your runs. It is a decentralized, privacy first cloud. Developers get all the tools they need to build a modern app, running on nodes that own or independent operators own.
- Orama L1 is the blockchain that powers all transactions on the network. Operators earn for providing storage, onion-routing and validation. Users can pay each other with private shielded transactions.
- Developers can deploy normal React or Node apps on the network

The first app on the network is already live. AnChat (anchat.io) is an PQ E2E encrypted messenger with voice and video calls, and its entire backend runs on Orama and RootWallet. It has a working paid subscription, and subscribers can pay in crypto, including ORAMA tokens. Every AnChat subscription paid in ORAMA creates demand for the token and strengthens the network.

## 3. The products are working

The most important thing to know about us , is that we are not raising money to build something from scratch, We have real working products with users that are beeing tested for well over a year and used daily by us.

- **Orama Network** runs on our devnet and testnet and carries a production messenger every day.
- **The ORAMA L1** is built and running on our stagenet. That covers smart contracts, private transactions, governance, storage deals and operator rewards.
- **RootWallet** Running on MacOS, Linux, iOS, Android, CLI and SDK. We have everything a wallet needs both for customers and developers.

The money goes to 3 things: independent audits so people can trust us, the first Orama One hardware node and setting up the company in Qatar.

## 4. Orama Network

Orama Network is a decentralized, privacy-first cloud. Developers get the building blocks every modern app needs, all through one CLI and SDK:

- hosting static sites, Next.js, Node and Go
- Distributed SQL Database ( RQLite )
- Distributed Cache ( Olric )
- File and Object Store encrypted on IPFS ( Private Swarm )
- Custom Serverless Function Engine (WASM), triggered by HTTP, WebSockets, pub/sub messages or a schedule
- Real-Time Messaging and presence
- Voice/Video Calls, through our own encrypted TURN and SFU servers
- Stealth calls, where call traffic looks like ordinary HTTPS, so calls work even on networks that block them ( UAE, China etc )
- Push notification service for iOS, Android and the web, including our own self-hosted push server
- Custom DNS servers, no need for cloudflare
- HTTPS and our own SSL Certificates
- Secure Storage for saving secrets, api keys and credentials
- Onion Routing built-in for anonymity on outbound requests
- Wallet-based login, with no emails and no passwords

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

- about 160 serverless functions
- the database
- encrypted attachments
- push notifications
- voice and video calls

The network has run for 90+ days with zero downtime.

### Orama One Coming Next

Today developers bring their own nodes. The next step for us is to ship Orama One hardware nodes. We have designed our own custom mini-PC box that you plug into power and the internet and it runs as a sealed operating system and contributes to the network. Once Orama One nodes are on the network, developers can rent capacity on other people's nodes and pay in crypto, the way they rent a server today. Apps that need hardware-enforced confidential computing go on server-class confidential nodes (AMD SEV-SNP or Intel TDX).

Orama is not trying to replace AWS. A company that needs five hundred servers won't come to us. The other 99% of developers, who want to ship their product with privacy and crypto built in, will.

## 5. Nobody can shut us down

Every other developer cloud depends on someone else. Vercel runs on AWS. Railway was taken down by Google. Most of the internet goes through Cloudflare.

Orama depends on none of them.

- **Machines.** The servers belong to independent operators.
- **DNS and certificates.** We run our own DNS Servers and issue our own certificates.
- **Calls.** Voice and video go through our own relays.
- **Payments.** Payments and rewards run on our own chain. Supporting payments with BTC, SOL, EVM and ORAMA
- **No admin keys.** Nobody holds a key to the chain, and nobody can pause or freeze it, including us.
- **Paid in crypto.** There is no bank account or card processor that can be cut off.

If we disappeared tomorrow, the network would keep running.

## 6. The ORAMA L1

ORAMA is our own layer-one blockchain. It is built and running on our stagenet.

It runs on Cosmos SDK and CometBFT, so blocks finalize in seconds. It supports smart contracts through CosmWasm. Private transactions with similar with Zcash's Orchard circuits, which have already been audited. Compressed NFTs and user-issued tokens are built in.

The economics are fair. There is no pre-mine and nothing is allocated to founders or investors. Every ORAMA in existence will have been earned by someone running the network. Just like Bitcoin did when it started. Emission halves every two years, then settles into a small permanent tail.

What sets it apart from other chains is the app ecosystem. On other chains a smart-contract developer still needs a server, a database and a storage provider from somewhere else. On Orama every app also gets a full backend from the same network: database, functions, storage and real-time messaging.

## 7. RootWallet

RootWallet is THE wallet for developers, and the way into Orama network.

One seed phrase for everything. Supporting BTC, EVM, SOL and ORAMA natively with HD accounts. From the same seed it also supports your passwords, SSH Keys and Two-Factor Authentication codes. Nothing leaves your device the wallet is non-custodial.

It works on all devices and operating system. On desktop it runs a local agent to make developers and AI Agents life easier. Everything is always approved by a real human. Before it answers, the agent checks *which program* is asking, using the operating system itself, not information the program supplies. It pins its approval to that program's exact binary. It decodes every transaction and shows it in plain terms before signing, so nothing is ever signed blind. 

That is the failure behind the Bybit theft.

This is how we build our own products. AI agents deploy to Orama and sign with RootWallet, and a human approves every step.

## 8. Orama & RootWallet working together

Every interaction with Orama goes through RootWallet.

- Developers sign in with a wallet signature instead of an email and password.
- Operators keep their SSH keys, secrets etc in the wallet. On device 
- Operator rewards and user payments on the ORAMA chain go through the same wallet.

## 9. Real research, not vibe coding

Nothing here is created from templates or vide coded. Each part was researched, designed and tested by real people before we shipped it. We use AI to our advantage.

- **Research first.** Before writing anyting we wrote three design studies for it
- **Our own isolation model.** Orama network uses no Docker or Kubernetes. We build our own decentralized system. Each app gets its own replicated database cluster
- **Tests.**
  - About 10,700 unit tests.
  - 100+ end-to-end test suites, including chaos tests that kill nodes and cut the network in the middle of operations.
  - A test that fails the build if the documentation claims something the code can't do.
- **Pace.** Two engineers made about 3,500 commits across both products since January 2025, with no outside money.

## 10. AnChat: live in production

AnChat is an PQ E2E encrypted messenger, live on Android and iOS for six months. Its entire backend runs on Orama, and its accounts run on RootWallet.

- About 160 serverless functions run its features.
- Messages use post-quantum key exchange.
- It has voice and video calls through Orama's own relays, encrypted attachments, and push notifications.
- It has made over 2.8 million database writes on the network.
- It has a paid subscription, paid in crypto.
- It has about 1,000 downloads and 150 active users.

The user numbers are small. The point is that a real app, with real users and real payments, runs on our stack every day.

**Try it yourself.** Download AnChat from https://anchat.io, create your account, and message us there. It's the quickest way to see everything in this document working.

## 11. Privacy, at the right time

There are over 45 million developers in the world, and about 16 million of them work alone or in small teams (SlashData, 2025).

AI is generating more apps than ever and they ship more. More than 60% of new databases on Supabase are now created by AI tools. Lovable alone hosts more than 60 million projects. Every one of these apps needs somewhere simple, cheap and safe to run.

Investors already value this market:

- Supabase raised at $10.5 billion in June 2026, with about 10 million developers.
- Vercel raised at $9.3 billion. Render raised at $1.5 billion. Railway passed two million users.

Wallets and vaults are just as large. There are 741 million crypto owners. MetaMask has more than 30 million monthly users, and Phantom was valued at $3 billion. 1Password passed $400 million ARR.

Privacy has moved from a niche to a demand. The share of Zcash supply held in shielded form rose from about 11% to about 30% in just over a year. Developers have watched clouds suspend accounts, wallets drained by transactions their owners couldn't read, and password vaults cracked years after a breach.

We have the right products for this moment. A huge market is moving toward privacy, and we offer it as working infrastructure with a real business model. Investors can make money from it.

## 12. Competition

Cloud developers are renting machines on GCP, AWS, Vercel, Supabase, Railway or Render can all be switched off by their vendor or they can change pricing whenever they want. This creates a monopoly from big-tech that cannot easily big broken.

Akash earned about $253,000 in the first quarter of 2026. The decentralized compute sector as a whole earned about $70 million in 2025. Decentralized cloud didn't fail for lack of demand. It failed on developer experience. Orama fixes that developer experience.

Orama is the only platform we found that offers the full developer stack on infrastructure nobody can switch off.

The same is for wallets. Phantom, MetaMask, Exodus and Coinbase hold crypto keys and nothing else. 1Password or BitWarden hold passwords and nothing else.

RootWallet is the only product that holds BTC, EVM, SOL alongside passwords, ssh keys and two-factor authentication codes all from one seed.

Signing for AI agents is now crowded. MetaMask, Coinbase, Phantom and 1Password all shipped agent features in 2026. Our difference is that approval is local and self-custodial, and it comes with the vault in the same product.

## 13. Business model: software margins

We make money from several lines that reinforce each other:

1. **RootWallet subscriptions** for the vault and developer features: passwords, SSH keys, two-factor codes and the desktop agent. Password managers charge $2–4 a month, so this is software revenue with almost no cost behind it.
2. **RootWallet transaction fees** on swaps (the market standard is 0.85%), on-ramp partnerships and staking.
3. **Orama One hardware.** A node you plug in that joins the network, earns rewards and hosts apps. The design is already done (Rev A): a 4.7-litre aluminium unit drawing 15–35 W, expected to retail around $899–1,199 at roughly a 30–35% gross margin. A confidential edition on AMD EPYC, at about $2,400–2,900, can follow for operators who host the most sensitive apps. Both prices are estimates from component costs, to be confirmed with suppliers.
4. **The compute marketplace.** When Orama One nodes are live, developers rent capacity on other operators' nodes, pay by usage in crypto, and we take a share.
5. **Network participation.** We hold a small fee from network transactions

Our business model is profit first. We don't own the networks servers. Developers and operators pay for their own nodes and they are paid by the network for the capacity they provide. RootWallet require's 0 servers it only needs a few serverless functions on Orama Network to run nothing else.

Our only costs are the team, office and a handful of servers. A cloud company that owns its hardware keeps 55% to 72% of its revenue as gross margin (DigitalOcean 55%, Cloudflare 72%). Our margins are those of a software company.

At scale the gap gets even bigger: Exodus, a self-custody wallet listed in the US, spent about $86 per monthly user in 2025 on technology and administration, with about 215 staff. Our own model puts us at about $4–5 per user. Lean infrastructure teams already exist: Railway serves two to three million users with about 35 people. Don't forget about Cursor as well which started with under 10 people.

That also makes us resilient. There is no data-centre bill, no single account to suspend, and no server we can lose.

## 14. Roadmap: revenue from month 3

**The products are built. This money takes them to market.**

What this round pays for, in months after funding:

- **Months 0 to 3.** Set up the company in Qatar and grow the team to 6 people. Open testnet to developers + Launch RootWallet subscriptions and swaps. **Revenue starts in month 3.**
- **Months 3 to 9.** Independent audit of RootWallet, the mobile app on both app stores, sales outreach to developers and operators, and an Orama One prototype with pre-orders.
- **Months 9 to 12.** Finish our own audits of Orama and the zero-knowledge integration. Mainnet for both products. Mainnet means out of beta, audited and secure. It is not the start of revenue.
- **Months 12 to 18.** Ship the first batch of Orama One nodes, open the compute marketplace, and raise the next round.


## 15. Team

We are two engineers who have spent eight years building multi-tenant infrastructure, apps and networks as contractors. Orama and RootWallet turn that experience into products.

- **Architect and platform engineer.**
  - Eight years in architecture, distributed systems, cloud infrastructure, CI/CD and delivery.
  - Built a multi-tenant platform from scratch, with infrastructure provisioned automatically for each customer. The business was later acquired.
  - Architected an agentic-AI platform for regulated finance and insurance.
  - Associate degree in software engineering; otherwise self-taught.
- **Network and infrastructure engineer.**
  - More than seven years building and running fiber-optic networks, servers and business networks for major Greek providers.
  - Certifications: Cisco CCNA, Cisco IT Essentials, Panduit CPT, FOA CFOT (Certified Fiber Optic Technician), FOA CPCT (Certified Premises Cabling Technician), Yeastar Certified Technician (S-Series and P-Series), and Aginode qualified supervisor for business installations.

Together we have delivered for product teams in Germany, the UK, Greece and Dubai, and we have consumer apps live on Apple and Google Store.

### Hiring with this round

- **Cryptography engineer.** The shielded pool, the wallet's key management, and our own audit of the zero-knowledge integration.
- **Cyber Security Expert** Our own audits of Orama, the bug bounty, and incident response.
- **UX/UI designer.** Design everything make products look more attractive.
- **Sales and partnerships.** Calls, outreach and partnerships with developers, operators and apps.

That is 6 people. We don't plan to grow more than that before the next round. What we have built since January 2025 shows how much a small team can build.

## 16. We ask for $1.75 million in equity for 18 months of runway

We are raising $1.75 million in equity for 18 months of runway. That takes both products to market and real revenue, with a team of six, based in Doha, Qatar.

Use of funds:

| Item | Amount |
|---|---|
| Two founders, full-time in Doha | $610,000 |
| Cryptography and security engineers | $215,000 |
| UX/UI designer and sales lead | $90,000 |
| Independent audit of RootWallet | $90,000 |
| Orama One prototype and first batch | $170,000 |
| Servers, hardware and infrastructure | $90,000 |
| Qatar company, legal and accounting | $115,000 |
| Travel and networking | $90,000 |
| Relocation to Qatar | $45,000 |
| Contingency | $235,000 |
| **Total** | **$1,750,000** |

team@orama.network

---
