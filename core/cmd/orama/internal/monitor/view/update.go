package view

import (
	"fmt"
	"time"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
	"github.com/DeBrosOfficial/network/pkg/updatenotice"
)

// noUpdateCell fills the UPDATE cell of a node whose auto-update agent has nothing to report.
const noUpdateCell = "-"

// maxReportedRunes bounds one field a node reports about an update or a chain upgrade.
const maxReportedRunes = 200

// reported is text a node reported (a release name, a refusal reason, an upgrade plan's name)
// made safe for the operator's terminal: on one line, without the characters that act on a
// terminal, and bounded. A node, a release repository or a governance proposal wrote it, so it is
// untrusted.
func reported(s string) string {
	return httputil.PrintableMax(httputil.OneLine(s), maxReportedRunes)
}

// UpdateCell is a node's auto-update finding in a few words: the release and what the policy does
// with it ("0.3.1 (notify)"), or that it was refused or rolled back.
func UpdateCell(t Theme, n *updatenotice.Notice) string {
	if n == nil {
		return noUpdateCell
	}
	candidate := reported(n.Candidate)
	if candidate == "" {
		candidate = unknownCell
	}
	switch n.State {
	case updatenotice.StateAvailable:
		return fmt.Sprintf("%s (%s)", candidate, n.Policy())
	case updatenotice.StateRefused:
		return t.Warn.Render("refused " + candidate)
	case updatenotice.StateFailed:
		return t.Warn.Render(candidate + " rolled back")
	}
	return unknownCell
}

// UpdateSentence is the finding as a sentence, with the policy's decision and why. It is empty
// when there is no finding.
func UpdateSentence(n *updatenotice.Notice) string {
	if n == nil {
		return ""
	}
	switch n.State {
	case updatenotice.StateAvailable:
		return fmt.Sprintf("release %s is available on the %s channel (running %s); the policy (%s) will %s: %s",
			reported(n.Candidate), reported(n.Channel), reported(n.Current), reported(n.Mode), n.Policy(), reported(n.Reason))
	case updatenotice.StateRefused:
		return fmt.Sprintf("release %s was refused on the %s channel (running %s): %s",
			orUnknown(reported(n.Candidate)), reported(n.Channel), reported(n.Current), reported(n.Reason))
	case updatenotice.StateFailed:
		return fmt.Sprintf("installing release %s failed and was rolled back (running %s): %s",
			orUnknown(reported(n.Candidate)), reported(n.Current), reported(n.Reason))
	}
	return ""
}

func orUnknown(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

// maxUpgradeETA is the longest time to an upgrade worth showing; a height a governance proposal
// set further out (or one that would overflow a Duration) shows its block count only.
const maxUpgradeETA = 10 * 365 * 24 * time.Hour

// UpgradeCountdown is how far a node's chain is from the height of the upgrade scheduled for it.
type UpgradeCountdown struct {
	// BlocksLeft is how many blocks remain; zero or negative once the height is reached.
	BlocksLeft int64
	// ETA is BlocksLeft at the chain's recent block time; HasETA is false when no block time was
	// measured or the height is reached.
	ETA    time.Duration
	HasETA bool
}

// CountdownTo is the distance from the chain's height to the upgrade's, at the node's average
// block time.
func CountdownTo(c *report.ChainReport, u *report.ChainUpgrade) UpgradeCountdown {
	left := u.Height - c.LatestHeight
	cd := UpgradeCountdown{BlocksLeft: left}
	if left > 0 && c.AvgBlockTimeSec > 0 {
		eta := float64(left) * c.AvgBlockTimeSec * float64(time.Second)
		if eta < float64(maxUpgradeETA) {
			cd.ETA = time.Duration(eta)
			cd.HasETA = true
		}
	}
	return cd
}

// UpgradeSentence is the chain upgrade scheduled for a node as a sentence: its name and height,
// the blocks and time remaining, and whether this node holds the binary. It is empty when no
// upgrade is scheduled and the plan could be read.
func UpgradeSentence(c *report.ChainReport) string {
	if c == nil {
		return ""
	}
	if c.UpgradeError != "" {
		return "the scheduled upgrade could not be read: " + reported(c.UpgradeError)
	}
	u := c.Upgrade
	if u == nil {
		return ""
	}
	cd := CountdownTo(c, u)
	var when string
	switch {
	case cd.BlocksLeft <= 0:
		when = "the height is reached"
	case cd.HasETA:
		when = fmt.Sprintf("%d blocks left, about %s", cd.BlocksLeft, FormatAge(cd.ETA))
	default:
		when = fmt.Sprintf("%d blocks left", cd.BlocksLeft)
	}
	staged := stagedSentence(u)
	return fmt.Sprintf("%s at height %d (%s); %s", reported(u.Name), u.Height, when, staged)
}

// stagedSentence says whether this node holds the binary for the upgrade and, when it does,
// whether it is the one the plan names. Everything a node reports is passed through reported.
func stagedSentence(u *report.ChainUpgrade) string {
	switch {
	case u.StageError != "":
		return "binary cannot be staged: " + reported(u.StageError)
	case !u.Staged:
		return "binary NOT staged on this node"
	}
	switch u.Checksums {
	case report.ChecksumsMatch:
		return "binary staged on this node and matches the plan"
	case report.ChecksumsMismatch:
		return "staged binary does NOT match the plan: " + reported(u.ChecksumProblem)
	case report.ChecksumsUnnamed:
		return "binary staged on this node, but the plan names no checksum to check it against: " + reported(u.ChecksumProblem)
	case report.ChecksumsUnchecked:
		return "binary staged on this node, but it could not be checked against the plan: " + reported(u.ChecksumProblem)
	}
	return "binary staged on this node"
}

// UpgradeNeedsAttention reports whether the upgrade's binary is missing on the node, cannot be
// staged, or is not the one the plan names: the cases an operator has to act on before the height.
func UpgradeNeedsAttention(u *report.ChainUpgrade) bool {
	if u == nil {
		return false
	}
	return !u.Staged || u.Checksums == report.ChecksumsMismatch || u.Checksums == report.ChecksumsUnnamed
}
