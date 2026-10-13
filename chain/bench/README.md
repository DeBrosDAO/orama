# Microbenchmark baseline

`make bench` writes `latest.txt` here (git-ignored). `make bench-baseline`, run on a
stable linux/amd64 machine with at least 8 CPUs, writes `baseline.txt`, which is
committed at a release. `make bench-compare BASE=bench/baseline.txt` compares the two
and fails on a regression of more than 25% in time per operation.

The first line of each file is the `# benchgate:` stamp that says where it was
measured and whether it may serve as a release baseline. Do not edit it, and do not
commit a baseline measured on a laptop. See `website/src/docs/contributor/performance.mdx`.

The 10x state measurements (`make state-gen`, `state-boot`, `state-snapshot`, `state-export`,
`state-indexer`, `state-prune`, `state-measure`) write their JSON to `build/state/results/` and are
not part of this baseline: see "10x state and soak" in the same page.
