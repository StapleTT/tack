package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// userHZ is the unit of /proc/<pid>/stat start times (USER_HZ, 100 on every
// Linux architecture we care about).
const userHZ = 100

// watchSunshine emits Sunshine's start time (clock ticks since boot) whenever
// a new Sunshine process appears, so the bridge can tell when Sunshine has
// forgotten its cursor flag.
func watchSunshine(ctx context.Context, out chan<- uint64) {
	var last uint64
	for {
		if start := sunshineStart(); start != 0 && start != last {
			last = start
			out <- start
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return
		}
	}
}

// sunshineStart returns the start time of the oldest running sunshine
// process, or 0 if none runs.
func sunshineStart() uint64 {
	dirs, _ := filepath.Glob("/proc/[0-9]*")
	var oldest uint64
	for _, d := range dirs {
		comm, err := os.ReadFile(d + "/comm")
		if err != nil || strings.TrimSpace(string(comm)) != "sunshine" {
			continue
		}
		stat, err := os.ReadFile(d + "/stat")
		if err != nil {
			continue
		}
		if t := statStart(string(stat)); t != 0 && (oldest == 0 || t < oldest) {
			oldest = t
		}
	}
	return oldest
}

// statStart parses field 22 (starttime) of /proc/<pid>/stat. The command
// name in field 2 may hold spaces, so count from its closing parenthesis.
func statStart(stat string) uint64 {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(stat[i+1:]) // field 3 onwards
	if len(f) < 20 {
		return 0
	}
	t, _ := strconv.ParseUint(f[19], 10, 64)
	return t
}

// sunshineUptime turns a start time into how long Sunshine has run.
func sunshineUptime(start uint64) time.Duration {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	up, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return time.Duration(up*float64(time.Second)) - time.Duration(start)*time.Second/userHZ
}
