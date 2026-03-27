# Appendix A: Emission Curve & Halving Schedule

## Parameters

- **Total supply:** 210,000,000 $ORAMA (hard cap)
- **Block time:** 6 seconds
- **Blocks per day:** 14,400
- **Blocks per year:** 5,256,000
- **Block reward split:** 80% to block proposer, 20% to bonding curve inventory (capped at 21M total to curve)
- **Halving interval:** Every 2 years (~10,512,000 blocks)

## Detailed Emission Table

| Era | Years | Block Reward | Blocks in Era | Era Emission | To Miners (80%) | To Curve (20%) | Cumulative Supply | % of Total |
|-----|-------|-------------|---------------|-------------|-----------------|----------------|-------------------|-----------|
| 1 | 1–2 | 100 $ORAMA | 10,512,000 | 105,120,000 | 84,096,000 | 21,024,000 | 105,120,000 | 50.03% |
| 2 | 3–4 | 50 $ORAMA | 10,512,000 | 52,560,000 | 42,048,000 | 10,512,000 | 157,680,000 | 75.04% |
| 3 | 5–6 | 25 $ORAMA | 10,512,000 | 26,280,000 | 21,024,000 | 5,256,000 | 183,960,000 | 87.52% |
| 4 | 7–8 | 12.5 $ORAMA | 10,512,000 | 13,140,000 | 10,512,000 | 2,628,000 | 197,100,000 | 93.76% |
| 5 | 9–10 | 6.25 $ORAMA | 10,512,000 | 6,570,000 | 5,256,000 | 1,314,000 | 203,670,000 | 96.88% |
| 6 | 11–12 | 3.125 $ORAMA | 10,512,000 | 3,285,000 | 2,628,000 | 657,000 | 206,955,000 | 98.44% |
| 7 | 13–14 | 1.5625 $ORAMA | 10,512,000 | 1,642,500 | 1,314,000 | 328,500 | 208,597,500 | 99.22% |
| 8 | 15–16 | 0.78125 $ORAMA | 10,512,000 | 821,250 | 657,000 | 164,250 | 209,418,750 | 99.61% |
| 9 | 17–18 | 0.390625 $ORAMA | 10,512,000 | 410,625 | 328,500 | 82,125 | 209,829,375 | 99.81% |
| 10 | 19–20 | 0.195313 $ORAMA | 10,512,000 | 205,313 | 164,250 | 41,063 | 210,034,688 | 99.90% |

*Emission continues halving indefinitely. When the remaining emittable supply is less than the block reward, the block reward equals the remaining supply — ensuring the 210,000,000 hard cap is never exceeded.*

## Cumulative Supply Over Time

```
Year 1:   52,560,000 $ORAMA  (25.0%)
Year 2:  105,120,000 $ORAMA  (50.0%)  ← First halving
Year 4:  157,680,000 $ORAMA  (75.0%)  ← Second halving
Year 6:  183,960,000 $ORAMA  (87.5%)
Year 8:  197,100,000 $ORAMA  (93.8%)
Year 10: 203,670,000 $ORAMA  (96.9%)
Year 20: ~210,000,000 $ORAMA (99.9%)
```

## Daily Emission by Era

| Era | Daily Total Emission | Daily to Miners | Daily to Curve |
|-----|---------------------|----------------|----------------|
| 1 | 1,440,000 $ORAMA | 1,152,000 | 288,000 |
| 2 | 720,000 $ORAMA | 576,000 | 144,000 |
| 3 | 360,000 $ORAMA | 288,000 | 72,000 |
| 4 | 180,000 $ORAMA | 144,000 | 36,000 |
| 5 | 90,000 $ORAMA | 72,000 | 18,000 |

## Per-Node Earnings Estimates (Era 1)

Assumes equal Effective Power across all nodes (simplified):

| Total Nodes | Daily per Node | Monthly per Node |
|-------------|---------------|-----------------|
| 300 | 3,840 $ORAMA | 115,200 $ORAMA |
| 500 | 2,304 $ORAMA | 69,120 $ORAMA |
| 1,000 | 1,152 $ORAMA | 34,560 $ORAMA |
| 5,000 | 230 $ORAMA | 6,912 $ORAMA |
| 10,000 | 115 $ORAMA | 3,456 $ORAMA |

*Actual earnings vary by Effective Power (stake × contribution × infrastructure multiplier).*

## Key Properties

1. **50% emitted in first 2 years** — rewards early risk-takers who secure the network when it's most vulnerable.
2. **75% emitted by year 4** — strong incentive to join early.
3. **96.9% emitted by year 10** — after a decade, the network runs primarily on transaction fee revenue.
4. **Never reaches 210M exactly** — the halving creates an asymptotic approach, just like Bitcoin's 21 million.
5. **Predictable at every block** — anyone can calculate the exact circulating supply at any block height with simple arithmetic.
