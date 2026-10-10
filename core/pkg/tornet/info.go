package tornet

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Files tor keeps in a DataDirectory that a node report reads.
const (
	dataDirFingerprint      = "fingerprint"
	dataDirFingerprintEd    = "fingerprint-ed25519"
	dataDirMicrodescConsens = "cached-microdesc-consensus"
	onionDir                = "onion"
	onionHostnameFile       = onionDir + "/hostname"
)

// flagWord is what a consensus flag looks like.
var flagWord = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)

// onionAddress is a v3 onion address: 56 base32 characters and ".onion".
var onionAddress = regexp.MustCompile(`^[a-z2-7]{56}\.onion$`)

// NodeInfo is what one tor process of this host knows about itself: the
// identity it was given (what a relay registration and an onion endpoint
// need) and the consensus it holds (whether the network lists it).
type NodeInfo struct {
	Home     string `json:"home"`
	Nickname string `json:"nickname,omitempty"`
	// Fingerprint is the relay RSA identity digest, 40 uppercase hex digits.
	Fingerprint string `json:"fingerprint,omitempty"`
	// Ed25519ID is the relay's ed25519 identity, unpadded base64.
	Ed25519ID string `json:"ed25519_id,omitempty"`
	// Onion is the v3 onion service address of a validator.
	Onion     string         `json:"onion,omitempty"`
	Consensus *ConsensusInfo `json:"consensus,omitempty"`
	// Error is why this role's DataDirectory could not be read; the others are still shown.
	Error string `json:"error,omitempty"`
}

// ConsensusInfo summarises the consensus a process holds.
type ConsensusInfo struct {
	Flavor     string    `json:"flavor"`
	ValidAfter time.Time `json:"valid_after"`
	FreshUntil time.Time `json:"fresh_until"`
	ValidUntil time.Time `json:"valid_until"`
	Fresh      bool      `json:"fresh"`
	Valid      bool      `json:"valid"`
	Signatures int       `json:"signatures"`
	Relays     int       `json:"relays"`
	Running    int       `json:"running"`
	Exits      int       `json:"exits"`
	// ExitsWithoutPorts is how many of the Exits the consensus summarises as
	// accepting no port, which no client uses (full consensus only).
	ExitsWithoutPorts int `json:"exits_without_ports,omitempty"`
	Guards            int `json:"guards"`
	// HSDirIntervalMinutes is the length of the time period an onion service
	// publishes for, as Tor uses it: the consensus's hsdir_interval clamped to
	// Tor's accepted range. Zero when the authorities vote none, which leaves
	// Tor's 1440 minutes in force.
	HSDirIntervalMinutes int `json:"hsdir_interval_minutes,omitempty"`
	// HSDirIntervalVotedMinutes is the hsdir_interval the authorities voted, set
	// only when it lies outside Tor's range and HSDirIntervalMinutes is the
	// clamped value (a pointer: a voted zero is a clamped value too).
	HSDirIntervalVotedMinutes *int64 `json:"hsdir_interval_voted_minutes,omitempty"`
	// Listed is true when the consensus lists this process's own relay.
	Listed      bool     `json:"listed"`
	ListedFlags []string `json:"listed_flags,omitempty"`
}

// ReadNodeInfo reads a tor DataDirectory. A file tor has not written yet (the
// process has not started, or has not found a consensus) leaves its fields
// empty; a file that is there and unreadable is an error. The directory is
// readable by the Tor account only, so the caller is root.
func ReadNodeInfo(home string, now time.Time) (NodeInfo, error) {
	info := NodeInfo{Home: home}
	if raw, ok, err := readIfExists(filepath.Join(home, dataDirFingerprint)); err != nil {
		return NodeInfo{}, err
	} else if ok {
		if info.Nickname, info.Fingerprint, err = parseFingerprintFile(raw); err != nil {
			return NodeInfo{}, fmt.Errorf("%s: %w", dataDirFingerprint, err)
		}
	}
	if raw, ok, err := readIfExists(filepath.Join(home, dataDirFingerprintEd)); err != nil {
		return NodeInfo{}, err
	} else if ok {
		_, id, _ := strings.Cut(strings.TrimSpace(string(raw)), " ")
		if err := checkEd25519ID(strings.TrimSpace(id)); err != nil {
			return NodeInfo{}, fmt.Errorf("%s: %w", dataDirFingerprintEd, err)
		}
		info.Ed25519ID = strings.TrimSpace(id)
	}
	if st, err := os.Lstat(filepath.Join(home, onionDir)); err == nil && !st.IsDir() {
		return NodeInfo{}, fmt.Errorf("%s is not a directory", onionDir)
	}
	if raw, ok, err := readIfExists(filepath.Join(home, onionHostnameFile)); err != nil {
		return NodeInfo{}, err
	} else if ok {
		info.Onion = strings.TrimSpace(string(raw))
		if !onionAddress.MatchString(info.Onion) {
			return NodeInfo{}, fmt.Errorf("%s is not a v3 onion address", onionHostnameFile)
		}
	}
	c, err := readHeldConsensus(home)
	if err != nil {
		return NodeInfo{}, err
	}
	if c != nil {
		info.Consensus = summarise(*c, info.Fingerprint, now)
	}
	return info, nil
}

// readHeldConsensus is the consensus in the DataDirectory: the full one an
// authority or directory cache keeps, else the microdescriptor one a client keeps.
func readHeldConsensus(home string) (*Consensus, error) {
	for _, name := range []string{DataDirConsensus, dataDirMicrodescConsens} {
		raw, ok, err := readIfExists(filepath.Join(home, name))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		c, err := ParseConsensus(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return &c, nil
	}
	return nil, nil
}

// effectiveHSDirInterval is the time period length Tor derives from the
// consensus's hsdir_interval: the voted value clamped to [minHSDirIntervalMinutes,
// maxHSDirIntervalMinutes]. It returns zero when none is voted, and the voted
// value as the second result only when it was clamped.
func effectiveHSDirInterval(c Consensus) (minutes int, clampedFrom *int64) {
	voted, ok := c.Params[paramHSDirInterval]
	switch {
	case !ok:
		return 0, nil
	case voted < minHSDirIntervalMinutes:
		return minHSDirIntervalMinutes, &voted
	case voted > maxHSDirIntervalMinutes:
		return maxHSDirIntervalMinutes, &voted
	}
	return int(voted), nil
}

func summarise(c Consensus, fingerprint string, now time.Time) *ConsensusInfo {
	out := &ConsensusInfo{
		Flavor: c.Flavor, ValidAfter: c.ValidAfter, FreshUntil: c.FreshUntil, ValidUntil: c.ValidUntil,
		Fresh: c.Fresh(now), Valid: c.Valid(now), Signatures: c.Signatures,
		Relays: len(c.Relays), Running: c.Running(), Exits: c.Exits(), ExitsWithoutPorts: c.ExitsWithoutPorts(), Guards: c.Guards(),
	}
	out.HSDirIntervalMinutes, out.HSDirIntervalVotedMinutes = effectiveHSDirInterval(c)
	if fingerprint != "" {
		if r, ok := c.Listed(fingerprint); ok {
			out.Listed = true
			for _, f := range r.Flags {
				// A flag is a word; the file is the Tor account's and the reader prints it as root.
				if flagWord.MatchString(f) {
					out.ListedFlags = append(out.ListedFlags, f)
				}
			}
		}
	}
	return out
}

// parseFingerprintFile reads "nickname 0123 4567 ...": tor writes the
// fingerprint in groups of four.
func parseFingerprintFile(raw []byte) (nickname, fingerprint string, err error) {
	nick, rest, ok := strings.Cut(strings.TrimSpace(string(raw)), " ")
	fp := strings.ToUpper(strings.ReplaceAll(rest, " ", ""))
	if !ok || !nicknamePattern.MatchString(nick) {
		return "", "", errors.New("not a nickname followed by a fingerprint")
	}
	if err := checkFingerprint("fingerprint", fp); err != nil {
		return "", "", err
	}
	return nick, fp, nil
}

// readIfExists reads a file of the DataDirectory. The directory belongs to the
// Tor account and the caller is root, so a link in it is refused and the size
// is bounded: a compromised tor must not be able to point root at another file.
func readIfExists(path string) ([]byte, bool, error) {
	data, err := readLimited(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	return data, true, nil
}
