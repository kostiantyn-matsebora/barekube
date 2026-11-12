// console: the per-node and whole-cluster control plane for the k3s cluster.
// Every operation is a workflow (internal/flows) that moves only on what the
// observer sees. See console/docs/workflows.md.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"k8s.io/utils/clock"

	"github.com/kostiantyn-matsebora/barekube/console/internal/app"
	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
	"github.com/kostiantyn-matsebora/barekube/console/internal/memstat"
	"github.com/kostiantyn-matsebora/barekube/console/internal/nodeagent"
	"github.com/kostiantyn-matsebora/barekube/console/internal/store"
	"github.com/kostiantyn-matsebora/barekube/console/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if !cfg.NodeAgent.Mock {
		cfg.NodeAgent.Token = os.Getenv("CONSOLE_NODE_TOKEN")
	}
	agents, err := nodeagent.NewClients(cfg.NodeAgent, cfg.Nodes)
	if err != nil {
		log.Fatalf("node-agent clients: %v", err)
	}
	if cfg.NodeAgent.Mock {
		log.Printf("node-agent: MOCK mode, no real node will be touched")
	}

	if p := os.Getenv("CONSOLE_DB"); p != "" {
		cfg.Storage.Path = p
	}
	db, err := store.Open(cfg.Storage.Path)
	if err != nil {
		log.Fatalf("storage %s: %v", cfg.Storage.Path, err)
	}
	log.Printf("storage: %s, retention %s", cfg.Storage.Path, cfg.Storage.Retention)

	svc, err := app.NewServiceWithStore(cfg, agents, nil, db)
	if err != nil {
		log.Fatalf("service: %v", err)
	}
	ctx := context.Background()
	svc.Start(ctx)
	go memstat.Loop(ctx, time.Minute, os.ReadFile, log.Printf)
	go db.Housekeep(ctx, clock.RealClock{}, cfg.Storage.Retention, time.Hour, func(p store.Purged, err error) {
		if err != nil {
			log.Printf("housekeeping failed: %v", err)
			return
		}
		log.Printf("housekeeping: deleted %+v", p)
	})

	srv, err := web.New(svc, web.Tokens{
		UI:         uiToken(),
		Node:       cfg.NodeAgent.Token,
		Automation: os.Getenv("CONSOLE_AUTOMATION_TOKEN"),
	})
	if err != nil {
		log.Fatalf("web server: %v", err)
	}
	if os.Getenv("CONSOLE_AUTOMATION_TOKEN") == "" {
		log.Printf("CONSOLE_AUTOMATION_TOKEN not set: automation cannot start workflows")
	}
	log.Printf("console listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, srv.Handler()); err != nil {
		log.Fatalf("http server: %v", err)
	}
}

// uiToken reads CONSOLE_UI_TOKEN. Unset means local dev: a random one is
// generated and printed once, never stored.
func uiToken() string {
	token := os.Getenv("CONSOLE_UI_TOKEN")
	if token == "" {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		token = hex.EncodeToString(b)
		log.Printf("CONSOLE_UI_TOKEN not set, generated one-time token: %s", token)
	}
	return token
}
