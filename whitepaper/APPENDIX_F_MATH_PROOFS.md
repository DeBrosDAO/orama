# Appendix F: Effective Power & Slashing Math

## Effective Power Formula

$$
\text{EP}_i = S_i \times (1 + C_i) \times M_i
$$

Where for node $i$:
- $S_i$ = Staked $ORAMA (minimum 1,000 at mainnet)
- $C_i$ = Contribution Score (0.0 to 1.0, recalculated every epoch)
- $M_i$ = Infrastructure Multiplier (1.0 or 1.5)

## Contribution Score Calculation

The Contribution Score is a weighted average of four metrics, measured every epoch (1 hour = 600 blocks):

$$
C_i = 0.4 \times U_i + 0.3 \times B_i + 0.2 \times W_i + 0.1 \times R_i
$$

Where:
- $U_i$ = Uptime ratio (blocks produced / blocks expected), range [0, 1]
- $B_i$ = Bandwidth ratio (bytes served / network average), normalized to [0, 1]
- $W_i$ = Work ratio (compute + storage + SQL queries served / network average), normalized to [0, 1]
- $R_i$ = Reliability ratio (successful responses / total requests), range [0, 1]

### Normalization

Bandwidth and work metrics are normalized relative to the network average:

$$
B_i = \min\left(\frac{b_i}{\bar{b}}, 1.0\right)
$$

Where $b_i$ is node $i$'s bandwidth served and $\bar{b}$ is the network average. Capped at 1.0 to prevent nodes from gaming the score by self-generating traffic.

### Score Examples

| Scenario | Uptime | Bandwidth | Work | Reliability | Score |
|---|---|---|---|---|---|
| Perfect node | 1.0 | 1.0 | 1.0 | 1.0 | **1.0** |
| Good node (some downtime) | 0.95 | 0.8 | 0.7 | 0.98 | **0.85** |
| Average node | 0.9 | 0.5 | 0.5 | 0.95 | **0.71** |
| Poor node | 0.7 | 0.3 | 0.2 | 0.8 | **0.49** |
| Minimal node (just online) | 0.8 | 0.1 | 0.05 | 0.9 | **0.44** |

## Infrastructure Multiplier

| Configuration | Multiplier ($M_i$) |
|---|---|
| Any node without OramaOS | 1.0× |
| Any node running OramaOS | **1.5×** |

The multiplier is based solely on running OramaOS, verified via TPM attestation. Orama One ships pre-loaded with OramaOS and thus receives the 1.5× multiplier out of the box, but any hardware running OramaOS receives the same bonus.

## Block Reward Distribution

Each block reward ($R$ = 100 $ORAMA in Era 1) is distributed:

$$
\text{Miner share} = 0.8 \times R = 80 \text{ \$ORAMA}
$$

$$
\text{Curve share} = 0.2 \times R = 20 \text{ \$ORAMA}
$$

The block proposer is selected proportionally to Effective Power:

$$
P(\text{node } i \text{ proposes block}) = \frac{\text{EP}_i}{\sum_{j=1}^{N} \text{EP}_j}
$$

## Effective Power Comparison Examples

### Scenario 1: Whale vs Active Small Node Runner

| | Whale | Small Runner |
|---|---|---|
| Stake | 100,000 $ORAMA | 2,000 $ORAMA |
| Contribution Score | 0.3 (minimal work) | 0.9 (active node) |
| Infrastructure | No OramaOS (1.0×) | OramaOS (1.5×) |
| **Effective Power** | 100,000 × 1.3 × 1.0 = **130,000** | 2,000 × 1.9 × 1.5 = **5,700** |
| **Ratio** | 22.8× more power | — |

The whale has 50× more stake but only 22.8× more Effective Power. Contribution and OramaOS close the gap significantly.

### Scenario 2: Two Equal-Stake Nodes

| | Without OramaOS | With OramaOS |
|---|---|---|
| Stake | 5,000 $ORAMA | 5,000 $ORAMA |
| Contribution Score | 0.7 | 0.7 |
| Infrastructure | 1.0× | 1.5× |
| **Effective Power** | 5,000 × 1.7 × 1.0 = **8,500** | 5,000 × 1.7 × 1.5 = **12,750** |
| **Ratio** | — | **1.5× more power** |

Same stake, same contribution, but OramaOS earns 50% more block rewards.

### Scenario 3: Active Small Node vs Lazy Whale

| | Lazy Whale | Active Small Node |
|---|---|---|
| Stake | 500,000 $ORAMA | 1,000 $ORAMA |
| Contribution Score | 0.1 (barely online) | 1.0 (perfect) |
| Infrastructure | No OramaOS (1.0×) | OramaOS (1.5×) |
| **Effective Power** | 500,000 × 1.1 × 1.0 = **550,000** | 1,000 × 2.0 × 1.5 = **3,000** |

The whale still dominates in raw Effective Power — but they have 500× more stake and only 183× more power. The contribution score and OramaOS multiplier reduce the whale's advantage by 63%. The small node runner is earning proportionally more per token staked.

## Slashing Rules

### Slashing Schedule

| Offense | Slash Amount | Scope | Recovery |
|---|---|---|---|
| Double-signing / equivocation | **100%** of stake | Stake + contribution score reset to 0 | Permanent ban from validation |
| Downtime > 20% in epoch | **5–30%** progressive | Stake only | Can resume after restaking |
| Downtime 20–40% | 5% | Stake | — |
| Downtime 40–60% | 10% | Stake | — |
| Downtime 60–80% | 20% | Stake | — |
| Downtime > 80% | 30% | Stake | — |
| False hardware/OS attestation | **50%** of stake | Stake + permanent flag | Infrastructure Multiplier permanently revoked |

### Progressive Downtime Slashing Formula

$$
\text{Slash \%} = \begin{cases}
0\% & \text{if downtime} \leq 20\% \\
5\% + 25\% \times \frac{\text{downtime} - 0.2}{0.6} & \text{if } 20\% < \text{downtime} \leq 80\% \\
30\% & \text{if downtime} > 80\%
\end{cases}
$$

### Slashing Examples

**Example 1: Node goes offline for 45 minutes in an epoch (75% uptime, 25% downtime)**

$$
\text{Slash} = 5\% + 25\% \times \frac{0.25 - 0.2}{0.6} = 5\% + 2.08\% = 7.08\%
$$

On a 10,000 $ORAMA stake: 708 $ORAMA slashed.

**Example 2: Node has 50% downtime**

$$
\text{Slash} = 5\% + 25\% \times \frac{0.5 - 0.2}{0.6} = 5\% + 12.5\% = 17.5\%
$$

On a 10,000 $ORAMA stake: 1,750 $ORAMA slashed.

**Example 3: Double-signing**

100% slash. On a 10,000 $ORAMA stake: all 10,000 $ORAMA slashed. Contribution score reset to 0. Node is permanently banned from validation.

### Where Slashed Tokens Go

All slashed $ORAMA is **burned** (permanently removed from circulating supply). Slashing is deflationary — bad actors make the token more scarce for everyone else.

## Epoch Transition

At the end of each epoch (every 600 blocks / 1 hour):

1. Contribution scores are recalculated for all active validators
2. Infrastructure attestations are verified
3. Effective Power is updated for all validators
4. Slashing conditions are evaluated
5. Block proposer selection probabilities are updated for the next epoch
6. Bridge fees are distributed to validators and NFT holders
