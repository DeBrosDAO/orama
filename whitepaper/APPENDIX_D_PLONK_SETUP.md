# Appendix D: PLONK Trusted Setup Ceremony Specification

## Overview

Orama's privacy features (public/private transaction toggle) use PLONK-based zk-SNARKs. PLONK requires a one-time **universal trusted setup** that generates a Structured Reference String (SRS). This SRS is used by all provers and verifiers on the network.

The setup ceremony is performed once during testnet (Q1 2027) and the resulting SRS is embedded in the genesis block at mainnet launch.

## Why PLONK Over Groth16

| Property | Groth16 | PLONK |
|---|---|---|
| Trusted setup | Per-circuit (new ceremony for every change) | Universal (one ceremony, valid for all circuits) |
| Proof size | ~200 bytes | ~500 bytes |
| Verification time | ~3ms | ~5ms |
| Upgradeability | Requires new ceremony | Same SRS works for new circuits |
| Security assumption | 1-of-N honest participants | 1-of-N honest participants |

PLONK's universal setup means Orama can add new privacy features (new transaction types, private smart contract calls, private DEX orders) without ever running another ceremony.

## Ceremony Design

### Multi-Party Computation (MPC)

The setup uses a sequential MPC protocol where each participant:

1. Receives the output of the previous participant
2. Mixes in their own randomness (called "toxic waste")
3. Destroys their randomness
4. Passes the result to the next participant

**Security guarantee:** As long as **one** participant honestly destroys their randomness, the SRS is secure. An attacker would need to compromise every single participant — a practical impossibility with hundreds of participants.

### Participation Requirements

- **Minimum participants:** 200 (target: 500+)
- **Open to anyone:** Any person in the world can participate. No KYC, no approval needed.
- **Diverse hardware:** Participants should use different hardware, operating systems, and entropy sources to prevent correlated failures.
- **Verifiable:** Each participant's contribution is publicly verifiable on-chain after the ceremony.

### Ceremony Phases

**Phase 1: Powers of Tau (universal)**
Generates the universal SRS that works for any PLONK circuit up to a maximum size.

1. Coordinator publishes initial parameters
2. Each participant downloads current state (~1 GB)
3. Participant runs the contribution software (5–30 minutes depending on hardware)
4. Participant uploads their contribution
5. Contribution is verified automatically
6. Next participant begins

**Phase 2: Circuit-Specific Finalization**
Takes the universal SRS and finalizes it for Orama's specific circuits (private transfers, private contract calls).

### Entropy Sources

Participants are encouraged to use creative and unpredictable entropy sources:
- Hardware random number generators
- Atmospheric noise sensors
- Radioactive decay measurements
- Keystroke timing
- Any source that is physically impossible for an attacker to predict or reproduce

### Timeline

| Step | Date | Duration |
|---|---|---|
| Ceremony software published and audited | Q4 2026 | — |
| Public registration opens | Q1 2027 | 2 weeks |
| Phase 1: Powers of Tau | Q1 2027 | 4–6 weeks |
| Phase 2: Circuit finalization | Q2 2027 | 2 weeks |
| Final SRS published and verified | Q2 2027 | — |
| SRS embedded in testnet | Q2 2027 | — |
| SRS embedded in mainnet genesis block | Q4 2028 | — |

## Verification

After the ceremony:

1. The final SRS is published on IPFS (permanently available)
2. A hash of the SRS is committed to the Orama genesis block
3. Every participant can verify their contribution was included
4. Anyone can verify the mathematical validity of the final SRS
5. The verification software is open-source

## What If the Setup Is Compromised?

If all participants collude (practically impossible with 200+ independent participants), an attacker could:
- Create fake private transactions (forge proofs)
- This would NOT affect public transactions, the supply cap, or the BTC bridge

The scope of damage is limited to the privacy feature. In the extremely unlikely event of compromise, governance can vote to:
1. Disable private transactions temporarily
2. Run a new ceremony with more participants
3. Re-enable with the new SRS

This is a Tier 1 emergency action (24-hour response by DeBros Team NFT holders).
