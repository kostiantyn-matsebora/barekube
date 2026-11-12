package memstat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func files(m map[string]string) FileReader {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, errors.New("absent")
	}
}

const status = "Name:\tconsole\nVmRSS:\t   102400 kB\nRssAnon:\t 40960 kB\nRssFile:\t 20480 kB\nRssShmem:\t 4096 kB\n"

func TestReportCgroupV2(t *testing.T) {
	got := Report(files(map[string]string{
		"/proc/self/status":             status,
		"/sys/fs/cgroup/memory.current": "104857600\n",
		"/sys/fs/cgroup/memory.stat":    "anon 41943040\nfile 52428800\nkernel 1048576\nshmem 2097152\nslab 524288\n",
	}))
	for _, want := range []string{"heapAlloc=", "goroutines=", "rss=100.0MiB", "anon=40.0MiB", "file=20.0MiB",
		"cgroup v2: current=100.0MiB", "anon=40.0MiB", "file=50.0MiB", "kernel=1.0MiB", "shmem=2.0MiB", "slab=0.5MiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestReportCgroupV1(t *testing.T) {
	got := Report(files(map[string]string{
		"/proc/self/status":                           status,
		"/sys/fs/cgroup/memory/memory.usage_in_bytes": "20971520",
		"/sys/fs/cgroup/memory/memory.stat":           "rss 10485760\ncache 8388608\nmapped_file 1048576\n",
	}))
	for _, want := range []string{"cgroup v1: usage=20.0MiB", "rss=10.0MiB", "cache=8.0MiB", "mappedFile=1.0MiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}

func TestReportSaysSoWhenAViewIsUnreadable(t *testing.T) {
	got := Report(files(nil))
	if !strings.Contains(got, "proc: unreadable") || !strings.Contains(got, "cgroup: unreadable") {
		t.Fatalf("%s", got)
	}
}

func TestKvSkipsMalformedLines(t *testing.T) {
	s := kv([]byte("good: 5 kB\nnocolon\nempty:\nbad: x\n"), ":")
	if s["good"] != 5 || len(s) != 1 {
		t.Fatalf("%v", s)
	}
}

func TestLoopReportsAtStartAndOnEachTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	lines := make(chan string, 8)
	done := make(chan struct{})
	go func() {
		Loop(ctx, 5*time.Millisecond, files(nil), func(f string, a ...any) { lines <- a[0].(string) })
		close(done)
	}()
	<-lines // at start
	<-lines // on a tick
	cancel()
	<-done
}
