// Package memstat reports where the process memory goes, from three views:
// the Go runtime, the process (/proc/self/status) and the container's cgroup.
// The router shows one number for the container. Without this there is no way
// to tell the Go heap from cached files or kernel memory.
package memstat

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// FileReader reads a file. It is os.ReadFile in production.
type FileReader func(path string) ([]byte, error)

const mib = 1 << 20

func kv(data []byte, sep string) map[string]uint64 {
	out := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, sep)
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		if n, err := strconv.ParseUint(f[0], 10, 64); err == nil {
			out[strings.TrimSpace(k)] = n
		}
	}
	return out
}

func m(n uint64) string { return fmt.Sprintf("%.1f", float64(n)/mib) }

// Report returns one log line.
func Report(read FileReader) string {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	line := fmt.Sprintf("mem go: heapAlloc=%sMiB heapInuse=%sMiB heapIdle=%sMiB heapReleased=%sMiB stackInuse=%sMiB sys=%sMiB gcs=%d goroutines=%d",
		m(ms.HeapAlloc), m(ms.HeapInuse), m(ms.HeapIdle), m(ms.HeapReleased), m(ms.StackInuse), m(ms.Sys), ms.NumGC, runtime.NumGoroutine())

	if b, err := read("/proc/self/status"); err == nil {
		s := kv(b, ":") // values are in kB
		line += fmt.Sprintf(" | proc: rss=%sMiB anon=%sMiB file=%sMiB shmem=%sMiB",
			m(s["VmRSS"]*1024), m(s["RssAnon"]*1024), m(s["RssFile"]*1024), m(s["RssShmem"]*1024))
	} else {
		line += " | proc: unreadable"
	}

	switch {
	case has(read, "/sys/fs/cgroup/memory.current"):
		cur, _ := read("/sys/fs/cgroup/memory.current")
		st, _ := read("/sys/fs/cgroup/memory.stat")
		s := kv(st, " ")
		line += fmt.Sprintf(" | cgroup v2: current=%sMiB anon=%sMiB file=%sMiB kernel=%sMiB shmem=%sMiB slab=%sMiB",
			m(first(kv(cur, "\x00"), cur)), m(s["anon"]), m(s["file"]), m(s["kernel"]), m(s["shmem"]), m(s["slab"]))
	case has(read, "/sys/fs/cgroup/memory/memory.usage_in_bytes"):
		cur, _ := read("/sys/fs/cgroup/memory/memory.usage_in_bytes")
		st, _ := read("/sys/fs/cgroup/memory/memory.stat")
		s := kv(st, " ")
		line += fmt.Sprintf(" | cgroup v1: usage=%sMiB rss=%sMiB cache=%sMiB mappedFile=%sMiB",
			m(first(kv(cur, "\x00"), cur)), m(s["rss"]), m(s["cache"]), m(s["mapped_file"]))
	default:
		line += " | cgroup: unreadable"
	}
	return line
}

func has(read FileReader, p string) bool { _, err := read(p); return err == nil }

// first parses a file holding a single number.
func first(_ map[string]uint64, raw []byte) uint64 {
	n, _ := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	return n
}

// Loop logs Report at start and then every interval until ctx ends.
func Loop(ctx context.Context, interval time.Duration, read FileReader, logf func(string, ...any)) {
	logf("%s", Report(read))
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			logf("%s", Report(read))
		}
	}
}
