---
title: "What is Orama Network? A decentralized cloud, explained"
description: "Orama is an open-source cloud built to run on servers owned by independent people: hosting, database, storage, functions and calls, with no accounts."
date: 2026-10-09
tags: [decentralized-cloud, platform, privacy]
---

Most software today runs on computers owned by a handful of cloud companies. That is convenient. It also gives those companies a full view of the data, full control over the service, and a single point of failure for everything built on top.

Orama Network is an open-source platform that provides the building blocks an application needs (hosting, a SQL database, a cache, file storage, messaging, serverless functions, domains, and voice and video relays) and is built to run them on machines owned by independent operators instead of one company's data centers.

This post explains what that means in practice, how it works, and, just as plainly, what is not finished yet.

## The problem with one company in the middle

The commercial cloud solved a real problem: nobody wants to rack servers again. But the arrangement has costs that are rarely stated.

- **Concentration.** A small number of providers host a very large share of the internet. When one of them has a bad day, a large part of the web has one too.
- **A failure domain you can't engineer away.** An account can be suspended, a region withdrawn, a price raised. No amount of work on your side removes the provider as a single point of failure.
- **Privacy.** The provider runs the hypervisor, the storage and the network. And because every service sits behind an account tied to a legal identity and a payment method, the provider knows who you are, and often who your users are.

Self-hosting avoids all three, and brings back the operational burden the cloud removed, at the reliability of one machine. There has been little in between. Orama is an attempt at that middle: a network of machines owned by many parties that still gives you a database, a cache, a place for files, a function runtime and a domain with a certificate.

## What you get

Developers use the `orama` command-line tool and an SDK (TypeScript or Go) and get familiar building blocks:

| Service | What it does | Status |
|---|---|---|
| Hosting | Static sites, Next.js (static and SSR), Node.js and Go backends | Live |
| Database | A SQL database replicated across three machines | Live |
| Cache | Distributed key-value maps with expiry | Live |
| File storage | Content-addressed storage, pinned across the cluster | Live |
| Pub/sub | Topic messaging over WebSocket and REST | Live |
| Functions | WebAssembly functions triggered by HTTP, WebSocket, pub/sub or cron | Live |
| Voice and video | A media server plus TURN relays, relay-only by design | Live |
| Push notifications | Apple, Android and web push | Live |
| Custom domains | Verified by a TXT record; certificates not issued yet | Partial |

There is no web dashboard. Humans use the CLI; programs use the SDK and the HTTP API. The full list, with what each service is built on, is on the [platform page](/platform).

## How it works

Orama is built from well-understood open-source parts: SQLite replicated with Raft (RQLite), an in-memory distributed cache (Olric), IPFS for content storage, libp2p for messaging, a WebAssembly runtime (wazero), WebRTC and TURN (Pion), CoreDNS, Caddy and WireGuard. A Go control layer ties them into a multi-tenant platform. Three design decisions explain most of it.

### Every app gets its own small cluster

In most platforms, a customer is a row in a shared table. In Orama, each app's space, called a *namespace*, is its own cluster. Creating one picks three machines, weighted by free capacity, and starts a database, a cache and a gateway for that namespace alone. There is no cross-tenant query, because there is no shared table to query across. Lose one of the three machines and the other two carry on.

### Nothing travels on the open internet between machines

When a server joins the network, it first builds an encrypted WireGuard tunnel to every other node. Only then do the database, cache, storage and gateway start, and they talk to their peers through those tunnels only. Services that are not meant to be public listen only on the private mesh.

### No accounts, no passwords

Sign-in is a wallet signature. The platform never collects an email address, a password or a phone number. Keys, sessions and permissions all derive from that signature, which is what [RootWallet](/apps), the network's key app, manages.

## What runs on it today

[AnChat](/apps), a private messenger in open beta, runs its whole backend on Orama: about 120 serverless functions, plus the database, storage, calls and push notifications. Calls go through TURN relays by design, so two people on a call never learn each other's IP addresses.

## What Orama is not

Being straight about limits matters more than any feature list:

- **Not finished.** Orama is alpha software running on small networks of invited nodes. It is not open for public sign-up yet.
- **Not a blockchain in the request path.** A separate ledger exists, but hosting, databases, functions and storage do not settle on it, and you don't need its token to deploy or call your app.
- **Not censorship-proof.** Nodes are ordinary servers at ordinary hosting providers. Orama is designed so that losing any single node does not take an app down. That is resilience, not immunity.
- **Not a defence against a hostile hypervisor.** Anyone who can read a running server's memory can read what that server is processing.

## Where it goes next

The [roadmap](/roadmap) runs from today's working network to a stable one, then OramaOS (a locked-down node operating system, in development), and Orama One: node hardware in people's hands.

If you want the full technical picture, read the [whitepaper](/whitepaper). To try deploying something, start with the [documentation](/docs). The code is open source, and every claim on this site is written against it.
