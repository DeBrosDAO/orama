// Package updatepolicy is a cluster's automatic-update policy as it is stored
// in cluster_settings and checked where it is written. It has no
// dependencies of its own beyond releaseverify's repository URL check, so the
// gateway that accepts a setting and the agent that reads it agree on what a
// valid one is without sharing a heavy package.
package updatepolicy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/releaseverify"
)

// The cluster_settings keys.
const (
	KeyMode    = "auto_update"
	KeyChannel = "update_channel"
	KeyWindow  = "update_window"
	KeyRepo    = "release_repo"
)

// Keys lists the keys, in the order a settings listing shows them.
var Keys = []string{KeyMode, KeyChannel, KeyWindow, KeyRepo}

// The modes.
const (
	// ModeOff does not look for a release.
	ModeOff = "off"
	// ModeNotify reports a newer release and does not install it. It is the
	// default.
	ModeNotify = "notify"
	// ModeAuto installs, one node at a time, inside the maintenance window.
	ModeAuto = "auto"
)

// Defaults of a cluster that has stored nothing.
const (
	DefaultMode    = ModeNotify
	DefaultChannel = "stable"
	// DefaultWindow is no window: auto may run at any hour.
	DefaultWindow = ""
	// DefaultRepo is no repository: nothing is looked up.
	DefaultRepo = ""
)

// hoursInDay bounds a window's hours.
const hoursInDay = 24

var channelPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Window is a maintenance window in whole hours, UTC. Start equal to End means
// no window; a Start after the End wraps midnight.
type Window struct {
	Start, End int
}

// ValidMode reports whether mode is off, notify or auto.
func ValidMode(mode string) error {
	switch mode {
	case ModeOff, ModeNotify, ModeAuto:
		return nil
	}
	return fmt.Errorf("auto-update is off, notify or auto, not %q", mode)
}

// ValidChannel reports whether channel can name a release channel.
func ValidChannel(channel string) error {
	if !channelPattern.MatchString(channel) {
		return fmt.Errorf("a channel is 1 to 32 characters of a-z, 0-9 and -, not %q", channel)
	}
	return nil
}

// ParseWindow reads "start-end" (hours 0 to 23, UTC) or "" for no window.
func ParseWindow(s string) (Window, error) {
	if s == "" {
		return Window{}, nil
	}
	startText, endText, ok := strings.Cut(s, "-")
	start, errStart := strconv.Atoi(startText)
	end, errEnd := strconv.Atoi(endText)
	if !ok || errStart != nil || errEnd != nil || start < 0 || start >= hoursInDay || end < 0 || end >= hoursInDay {
		return Window{}, fmt.Errorf("a maintenance window is start-end in whole hours 0 to 23 (UTC), for example 1-5, or empty for none; not %q", s)
	}
	return Window{Start: start, End: end}, nil
}

// ValidRepo reports whether repo is empty (none) or a release repository URL.
func ValidRepo(repo string) error {
	if repo == "" {
		return nil
	}
	if _, err := releaseverify.ParseRepositoryURL(repo); err != nil {
		return err
	}
	return nil
}

// Validate checks the value for the stored key.
func Validate(key, value string) error {
	switch key {
	case KeyMode:
		return ValidMode(value)
	case KeyChannel:
		return ValidChannel(value)
	case KeyWindow:
		_, err := ParseWindow(value)
		return err
	case KeyRepo:
		return ValidRepo(value)
	}
	return fmt.Errorf("%q is not an auto-update setting", key)
}
