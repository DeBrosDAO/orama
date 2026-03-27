# Appendix C: Bonding Curve Price Table & BTC Reserve Projections

## Curve Formula

$$
\text{Price (BTC)} = k \times \sqrt{n}
$$

Where:
- `k = 0.0000000006 BTC` (genesis constant)
- `n` = total number of $ORAMA tokens sold from the curve

## Total Cost Formula

The cumulative BTC required to purchase the first N tokens from the curve:

$$
\text{Total BTC} = k \times \frac{2}{3} \times n^{3/2}
$$

## Detailed Price Table

| Tokens Sold (n) | Price per $ORAMA (BTC) | Price (USD at $100K BTC) | Cumulative BTC Spent | Cumulative USD Spent |
|---|---|---|---|---|
| 1 | 0.0000000006 | $0.00006 | 0.0000000004 | $0.00004 |
| 100 | 0.000000006 | $0.0006 | 0.0000004 | $0.04 |
| 1,000 | 0.000000019 | $0.0019 | 0.000012 | $1.27 |
| 10,000 | 0.00000006 | $0.006 | 0.0004 | $40 |
| 50,000 | 0.000000134 | $0.0134 | 0.00447 | $447 |
| 100,000 | 0.00000019 | $0.019 | 0.0127 | $1,265 |
| 500,000 | 0.000000424 | $0.0424 | 0.141 | $14,142 |
| 1,000,000 | 0.0000006 | $0.06 | 0.4 | $40,000 |
| 2,000,000 | 0.000000849 | $0.0849 | 1.131 | $113,137 |
| 5,000,000 | 0.00000134 | $0.134 | 4.47 | $447,214 |
| 10,000,000 | 0.0000019 | $0.19 | 12.65 | $1,264,911 |
| 15,000,000 | 0.00000232 | $0.232 | 23.24 | $2,323,790 |
| 21,000,000 | 0.00000275 | $0.275 | 38.49 | $3,849,002 |

## BTC Protocol Reserve Accumulation

As tokens are purchased from the curve, BTC accumulates in the protocol reserve:

| Milestone | BTC in Reserve | USD Value | What This Backs |
|---|---|---|---|
| First 100K tokens sold | 0.013 BTC | $1,265 | Minimal — early days |
| First 1M tokens sold | 0.4 BTC | $40K | Small but growing reserve |
| First 5M tokens sold | 4.47 BTC | $447K | Meaningful bridge backing |
| First 10M tokens sold | 12.65 BTC | $1.27M | Substantial reserve |
| Full curve (21M sold) | **38.49 BTC** | **$3.85M** | Full reserve capacity |

## Curve Inventory Supply

The curve's sell-side inventory comes from 20% of block rewards, **capped at 21,000,000 $ORAMA total**. Once the curve has accumulated 21M tokens (whether sold or unsold), the 20% share redirects to the block proposer.

| Era | Daily Curve Inventory Added | Monthly | Era Total (2 years) |
|---|---|---|---|
| 1 (Years 1–2) | 288,000 $ORAMA | 8,640,000 | 21,024,000 |
| 2 (Years 3–4)* | 144,000 $ORAMA | 4,320,000 | 10,512,000 |

*The 21M cap will likely be reached during Era 1. Once reached, no further tokens flow to the curve regardless of era.*

## Curve vs Order Book Interaction

The curve and the order book coexist. The real market price is determined by organic supply and demand on the order book:

**Scenario: Curve price > Order book price (typical early on)**
- Miners sell on the order book at prices below the curve
- Buyers purchase from the order book (cheaper)
- The curve sits unused, accumulating inventory
- This is normal — miners have surplus tokens and need to sell

**Scenario: Order book price > Curve price (as demand grows)**
- Order book is more expensive than the curve
- Buyers purchase from the curve instead
- BTC flows into the protocol reserve
- The curve provides a price ceiling in this scenario

**Scenario: After sunset**
- Curve stops receiving new inventory
- All trading happens on the order book
- The curve may still have leftover inventory available at its last price point
- Pure free market

## Sunset Conditions

The curve's 20% share of block rewards drops to 0% when:
1. Average daily order book volume exceeds the governance-defined threshold
2. This threshold is maintained for 30 consecutive days
3. The change is automatic — no vote required

After sunset, the 20% that previously went to the curve flows directly to block proposers (miners receive 100% of block rewards).
