# Appendix B: Orama One Hardware Specs

## Design Principles

Orama One is designed so that anyone, anywhere, can afford to run a node. The hardware requirements are intentionally modest — the protocol is optimized for accessibility, not raw performance. Scaling is achieved through more nodes, not bigger nodes.

## Orama One — Standard Node

**Form factor:** 3D-printed compact enclosure, fanless, suitable for home use.
**Open-source:** Enclosure design files, bill of materials, and assembly guide will be published so anyone can build their own.

### Minimum Specifications

| Component | Specification | Purpose |
|-----------|--------------|---------|
| **CPU** | 4+ cores, 2.0+ GHz (ARM or x86) | Block validation, WASM execution, consensus |
| **RAM** | 8 GB | Distributed cache, state management |
| **Storage** | 256 GB NVMe SSD | Blockchain state, IPFS storage, SQL database |
| **Network** | 1 Gbps Ethernet | Encrypted mesh, block propagation, data serving |
| **TPM** | TPM 2.0 module | Infrastructure attestation for OramaOS multiplier |
| **Power** | Low power draw (target: under 25W) | Continuous operation at minimal energy cost |

### Recommended Specifications

| Component | Specification |
|-----------|--------------|
| **CPU** | 6+ cores, 2.4+ GHz |
| **RAM** | 16 GB |
| **Storage** | 512 GB NVMe SSD |
| **Network** | 2.5 Gbps Ethernet |

## Cloud Node — Minimum Specs

For operators who prefer cloud hosting over physical hardware.

| Component | Specification |
|-----------|--------------|
| **vCPUs** | 2+ cores |
| **RAM** | 4 GB |
| **Storage** | 80 GB SSD |
| **Bandwidth** | 1 TB/month |
| **OS** | OramaOS image |

Cloud nodes receive the OramaOS multiplier (1.5×) the same as any other node running OramaOS. The multiplier is based on the operating system, not the hardware.

## Compute Provider Node — Additional Specs

For operators who want to participate in the AI Marketplace (optional).

| Component | Specification | Purpose |
|-----------|--------------|---------|
| **Accelerator** | High-performance GPU or AI accelerator | AI model inference |
| **Accelerator memory** | 24 GB+ | Model loading |
| **RAM** | 32 GB+ | Data preprocessing |
| **Storage** | 1 TB+ NVMe SSD | Model storage |
| **Network** | High-throughput connection recommended | API serving |

Compute provider nodes must also meet standard node specs. Compute capacity is registered separately on the AI Marketplace. Compute providers earn standard block rewards from their base node plus marketplace revenue from AI API calls.
