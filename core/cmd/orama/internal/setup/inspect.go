package setup

import (
	"context"
	"fmt"

	"github.com/DeBrosOfficial/network/pkg/install"
)

// Inspection is what Inspect learned about one machine: what it has and whether
// it is enough for the profile.
type Inspection struct {
	IP    string
	Facts Facts
	// Installed says there is nothing left to install on it.
	Installed bool
	// Err is why the machine cannot be used: it could not be reached, or it is
	// below the floor of its profile.
	Err error
}

// Summary is a line for the operator: the hardware, and the verdict.
func (i Inspection) Summary() string {
	switch {
	case i.Err != nil:
		return fmt.Sprintf("%s: %v", i.IP, i.Err)
	case i.Installed:
		return fmt.Sprintf("%s: already installed", i.IP)
	}
	h := i.Facts.Hardware
	return fmt.Sprintf("%s: %d vCPU, %s, %.0f GiB free, linux/%s: enough", i.IP, h.CPUCores, gibText(h.RAMBytes), float64(h.FreeDiskBytes)/bytesPerGiB, i.Facts.Arch)
}

// Inspect reaches every machine of opts and reads its hardware, checking it
// against the profile, and changes nothing on any of them. The wizard shows the
// result before it asks to go ahead; Run does the same checks again.
func Inspect(ctx context.Context, opts Options, enroll Enroller) ([]Inspection, error) {
	if err := opts.Normalize(); err != nil {
		return nil, err
	}
	profile := install.ProfileFull
	if opts.ClusterOnly {
		profile = install.ProfileClusterOnly
	}
	out := make([]Inspection, 0, len(opts.IPs))
	for _, ip := range opts.IPs {
		out = append(out, inspectOne(ctx, opts, enroll, ip, profile))
	}
	return out, nil
}

func inspectOne(ctx context.Context, opts Options, enroll Enroller, ip string, profile install.Profile) Inspection {
	res := Inspection{IP: ip}
	req := MachineRequest{IP: ip, User: opts.User, HostKey: opts.HostKeys[ip], BootstrapKey: opts.BootstrapKey, Password: opts.Password, UsePassword: opts.UsePassword}
	if req.HostKey == "" {
		req.HostKey = opts.HostKeys[""]
	}
	m, err := enroll.Enroll(ctx, req)
	if err != nil {
		res.Err = fmt.Errorf("cannot reach it: %w", err)
		return res
	}
	defer m.Close()
	if res.Facts, err = m.Probe(ctx); err != nil {
		res.Err = fmt.Errorf("cannot read its hardware: %w", err)
		return res
	}
	n := &nodeRun{facts: res.Facts, plan: NodePlan{Profile: profile, StorageGB: opts.StorageGB}}
	if !n.needsInstall() {
		res.Installed = true
		return res
	}
	res.Err = install.CheckHardware(profile, opts.StorageGB, res.Facts.Hardware)
	return res
}
