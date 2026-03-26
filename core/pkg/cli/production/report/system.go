package report

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// collectSystem gathers system-level metrics using local commands and /proc files.
func collectSystem() *SystemReport {
	r := &SystemReport{}

	// 1. Uptime seconds: read /proc/uptime, parse first field
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 1 {
			if f, err := strconv.ParseFloat(fields[0], 64); err == nil {
				r.UptimeSeconds = int64(f)
			}
		}
	}

	// 2. Uptime since: run `uptime -s`
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "uptime", "-s"); err == nil {
			r.UptimeSince = strings.TrimSpace(out)
		}
	}

	// 3. CPU count: run `nproc`
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "nproc"); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.CPUCount = n
			}
		}
	}

	// 4. Load averages: read /proc/loadavg, parse first 3 fields
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			if f, err := strconv.ParseFloat(fields[0], 64); err == nil {
				r.LoadAvg1 = f
			}
			if f, err := strconv.ParseFloat(fields[1], 64); err == nil {
				r.LoadAvg5 = f
			}
			if f, err := strconv.ParseFloat(fields[2], 64); err == nil {
				r.LoadAvg15 = f
			}
		}
	}

	// 5 & 6. Memory and swap: run `free -m`, parse Mem: and Swap: lines
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "free", "-m"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				if len(fields) >= 4 && fields[0] == "Mem:" {
					// Mem: total used free shared buff/cache available
					if n, err := strconv.Atoi(fields[1]); err == nil {
						r.MemTotalMB = n
					}
					if n, err := strconv.Atoi(fields[2]); err == nil {
						r.MemUsedMB = n
					}
					if n, err := strconv.Atoi(fields[3]); err == nil {
						r.MemFreeMB = n
					}
					if len(fields) >= 7 {
						if n, err := strconv.Atoi(fields[6]); err == nil {
							r.MemAvailMB = n
						}
					}
					if r.MemTotalMB > 0 {
						r.MemUsePct = (r.MemTotalMB - r.MemAvailMB) * 100 / r.MemTotalMB
					}
				}
				if len(fields) >= 3 && fields[0] == "Swap:" {
					if n, err := strconv.Atoi(fields[1]); err == nil {
						r.SwapTotalMB = n
					}
					if n, err := strconv.Atoi(fields[2]); err == nil {
						r.SwapUsedMB = n
					}
				}
			}
		}
	}

	// 7. Disk usage: run `df -h /` and `df -h /opt/orama`, use whichever has higher usage
	{
		type diskInfo struct {
			total   string
			used    string
			avail   string
			usePct  int
		}

		parseDf := func(out string) *diskInfo {
			lines := strings.Split(out, "\n")
			if len(lines) < 2 {
				return nil
			}
			fields := strings.Fields(lines[1])
			if len(fields) < 5 {
				return nil
			}
			pctStr := strings.TrimSuffix(fields[4], "%")
			pct, err := strconv.Atoi(pctStr)
			if err != nil {
				return nil
			}
			return &diskInfo{
				total:  fields[1],
				used:   fields[2],
				avail:  fields[3],
				usePct: pct,
			}
		}

		ctx1, cancel1 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel1()
		rootDisk := (*diskInfo)(nil)
		if out, err := runCmd(ctx1, "df", "-h", "/"); err == nil {
			rootDisk = parseDf(out)
		}

		ctx2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel2()
		optDisk := (*diskInfo)(nil)
		if out, err := runCmd(ctx2, "df", "-h", "/opt/orama"); err == nil {
			optDisk = parseDf(out)
		}

		best := rootDisk
		if optDisk != nil && (best == nil || optDisk.usePct > best.usePct) {
			best = optDisk
		}
		if best != nil {
			r.DiskTotalGB = best.total
			r.DiskUsedGB = best.used
			r.DiskAvailGB = best.avail
			r.DiskUsePct = best.usePct
		}
	}

	// 8. Inode usage: run `df -i /`, parse Use% from second line
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "df", "-i", "/"); err == nil {
			lines := strings.Split(out, "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 5 {
					pctStr := strings.TrimSuffix(fields[4], "%")
					if n, err := strconv.Atoi(pctStr); err == nil {
						r.InodePct = n
					}
				}
			}
		}
	}

	// 9. OOM kills: run `dmesg 2>/dev/null | grep -ci 'out of memory'` via bash -c
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "bash", "-c", "dmesg 2>/dev/null | grep -ci 'out of memory'"); err == nil {
			if n, err := strconv.Atoi(strings.TrimSpace(out)); err == nil {
				r.OOMKills = n
			}
		}
		// On error, OOMKills stays 0 (zero value)
	}

	// 10. Kernel version: run `uname -r`
	{
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if out, err := runCmd(ctx, "uname", "-r"); err == nil {
			r.KernelVersion = strings.TrimSpace(out)
		}
	}

	// 11. Current unix timestamp
	r.TimeUnix = time.Now().Unix()

	return r
}
