// node-agent: the daemon that runs on each node (see internal/agentd). It
// exposes a fixed, narrow set of endpoints and pushes a heartbeat to console.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kostiantyn-matsebora/barekube/console/internal/agentd"
)

func main() {
	addr := flag.String("addr", ":9091", "listen address")
	certFile := flag.String("cert", "/etc/barekube-agent/cert.pem", "TLS certificate")
	keyFile := flag.String("key", "/etc/barekube-agent/key.pem", "TLS private key")
	kubectlBin := flag.String("kubectl-bin", "k3s", `binary to exec; real nodes use "k3s" (with -kubectl-subcommand kubectl), local dev uses "kubectl" (with -kubectl-subcommand "")`)
	kubectlSubcommand := flag.String("kubectl-subcommand", "kubectl", `subcommand to prepend before kubectl args; empty if -kubectl-bin is already kubectl`)
	kubectlContext := flag.String("kubectl-context", "", "optional --context for kubectl (local dev only)")
	k3sUnit := flag.String("k3s-unit", "k3s-agent", `systemd unit of this node's k3s: "k3s" on masters, "k3s-agent" on agents`)
	etcdMetrics := flag.String("etcd-metrics-url", "http://127.0.0.1:2381/metrics", "local etcd metrics endpoint, for the leader probe")
	nodeName := flag.String("node-name", "", "this node's name as known to console's config.yaml, required with -console-addr")
	consoleAddr := flag.String("console-addr", "", `console's address to push heartbeats to; empty disables heartbeating`)
	heartbeatInterval := flag.Duration("heartbeat-interval", 5*time.Second, "how often to push a heartbeat")
	flag.Parse()

	token := os.Getenv("NODE_AGENT_TOKEN")
	if token == "" {
		log.Fatal("NODE_AGENT_TOKEN must be set")
	}
	s := agentd.New(agentd.Config{Token: token, KubectlBin: *kubectlBin, KubectlSubcommand: *kubectlSubcommand,
		KubectlContext: *kubectlContext, K3sUnit: *k3sUnit, EtcdMetricsURL: *etcdMetrics})

	if *consoleAddr != "" {
		if *nodeName == "" {
			log.Fatal("-node-name is required when -console-addr is set")
		}
		go s.RunHeartbeat(context.Background(), *consoleAddr, *nodeName, token, *heartbeatInterval)
	} else {
		log.Printf("heartbeat: disabled (-console-addr not set)")
	}

	log.Printf("node-agent listening on %s", *addr)
	if err := http.ListenAndServeTLS(*addr, *certFile, *keyFile, s.Handler()); err != nil {
		log.Fatal(err)
	}
}
