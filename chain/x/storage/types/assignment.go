package types

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"sort"
)

// AssignmentSeed is H(block_hash(assign_at_height−1) ‖ deal ‖ j). deal is 8
// big-endian bytes and j is 4 big-endian bytes, concatenated with the raw
// previous-block hash. There is no extra domain tag: the spec's preimage is
// exactly those three fields.
func AssignmentSeed(prevBlockHash []byte, dealID uint64, slot uint32) []byte {
	h := sha256.New()
	h.Write(prevBlockHash)
	var deal [8]byte
	binary.BigEndian.PutUint64(deal[:], dealID)
	h.Write(deal[:])
	var j [4]byte
	binary.BigEndian.PutUint32(j[:], slot)
	h.Write(j[:])
	return h.Sum(nil)
}

// SampleSeed domains the epoch sample so it cannot collide with an assignment seed.
func SampleSeed(epoch uint64, nodeID string) []byte {
	h := sha256.New()
	h.Write([]byte("ORAMA/storage/sample/v1"))
	var e [8]byte
	binary.BigEndian.PutUint64(e[:], epoch)
	h.Write(e[:])
	h.Write([]byte(nodeID))
	return h.Sum(nil)
}

// LeafChallengeSeed is the preimage whose digest, mod the real leaf count, is
// the challenged leaf. Padding leaves sit at indexes >= real count, so they
// are never selected.
func LeafChallengeSeed(epoch, dealID uint64, slot uint32, nodeID string) []byte {
	h := sha256.New()
	h.Write([]byte("ORAMA/storage/leaf/v1"))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], epoch)
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], dealID)
	h.Write(buf[:])
	var j [4]byte
	binary.BigEndian.PutUint32(j[:], slot)
	h.Write(j[:])
	h.Write([]byte(nodeID))
	return h.Sum(nil)
}

// Candidate is one storage node the picker may assign.
type Candidate struct {
	ID        string
	Operator  string
	Network16 string
	ASN       uint32
	Probation bool
	Active    bool
	Declared  uint64
	Reserved  uint64
}

// PickRules is the diversity and capacity filter for one slot.
type PickRules struct {
	PieceBytes         uint64
	RepairOperator     string
	ExcludedOperator   string
	Protocol           bool
	UsedOperators      map[string]struct{}
	UsedNetworks       map[string]struct{}
	UsedASNs           map[uint32]struct{}
	ProbationNodes     map[string]uint32
	ProbationOperators map[string]uint32
	ProbationNetworks  map[string]uint32
	ProbationASNs      map[uint32]uint32
	ProbationSlots     uint32
	ProbationOpCap     uint32
	ProbationNetCap    uint32
	ProbationASNCap    uint32
}

// PickCandidate chooses a node from indexed operator buckets. The seed selects
// the starting bucket; within a bucket, nodes are tried in a seeded rotation.
// Distinct operators are required. Protocol deals also require a distinct /16
// and ASN, and they honor probation caps.
func PickCandidate(cands []Candidate, seed []byte, rules PickRules) (Candidate, bool) {
	if len(cands) == 0 {
		return Candidate{}, false
	}
	byOp := map[string][]Candidate{}
	var ops []string
	seen := map[string]bool{}
	for _, c := range cands {
		byOp[c.Operator] = append(byOp[c.Operator], c)
		if !seen[c.Operator] {
			seen[c.Operator] = true
			ops = append(ops, c.Operator)
		}
	}
	sort.Strings(ops)
	for op, nodes := range byOp {
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
		byOp[op] = nodes
	}
	start := seedMod(seed, len(ops))
	for step := 0; step < len(ops); step++ {
		op := ops[(start+step)%len(ops)]
		if _, used := rules.UsedOperators[op]; used {
			continue
		}
		if rules.RepairOperator != "" && op == rules.RepairOperator {
			continue
		}
		if rules.ExcludedOperator != "" && op == rules.ExcludedOperator {
			continue
		}
		bucket := byOp[op]
		nstart := seedMod(seed, len(bucket))
		for nstep := 0; nstep < len(bucket); nstep++ {
			c := bucket[(nstart+nstep)%len(bucket)]
			if eligible(c, rules) {
				return c, true
			}
		}
	}
	return Candidate{}, false
}

func eligible(c Candidate, rules PickRules) bool {
	if !c.Active {
		return false
	}
	if c.Reserved > ^uint64(0)-rules.PieceBytes {
		return false
	}
	if c.Reserved+rules.PieceBytes > c.Declared {
		return false
	}
	if rules.Protocol {
		if _, used := rules.UsedNetworks[c.Network16]; used {
			return false
		}
		if _, used := rules.UsedASNs[c.ASN]; used {
			return false
		}
		if c.Probation {
			if rules.ProbationNodes[c.ID] >= rules.ProbationSlots {
				return false
			}
			if rules.ProbationOperators[c.Operator] >= rules.ProbationOpCap {
				return false
			}
			if rules.ProbationNetworks[c.Network16] >= rules.ProbationNetCap {
				return false
			}
			if rules.ProbationASNs[c.ASN] >= rules.ProbationASNCap {
				return false
			}
		}
	}
	return true
}

func seedMod(seed []byte, n int) int {
	if n <= 1 {
		return 0
	}
	if len(seed) == 0 {
		return 0
	}
	i := new(big.Int).SetBytes(seed)
	i.Mod(i, big.NewInt(int64(n)))
	return int(i.Int64())
}
