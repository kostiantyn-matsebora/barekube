package nodeagent

import (
	"fmt"
	"os"
	"strings"

	"github.com/kostiantyn-matsebora/barekube/console/internal/config"
)

// NewClients builds one Client per configured node: Mock for every node if
// cfg.Mock is set, otherwise Real. All nodes share the SAME bearer token
// (cfg.Token — one shared secret, same model as the UI's single-token
// login) but each has its OWN pinned cert fingerprint, read from
// cfg.CredentialsDir/<node-name> (just the fingerprint — `barekube install node-agent`
// writes it; the shared token still has to reach every node's systemd
// environment and this orchestrator's config by hand, out of band).
func NewClients(cfg config.NodeAgentConfig, nodes []config.Node) (map[string]Client, error) {
	clients := make(map[string]Client, len(nodes))
	for _, n := range nodes {
		if cfg.Mock {
			clients[n.Name] = &Mock{NodeName: n.Name}
			continue
		}
		if cfg.Token == "" {
			return nil, fmt.Errorf("node-agent: token is required (one shared token for all nodes)")
		}
		fp, err := readFingerprint(cfg.CredentialsDir, n.Name)
		if err != nil {
			return nil, err
		}
		port := n.NodeAgentPort
		if port == 0 {
			port = 9091
		}
		clients[n.Name] = &Real{
			Host:             n.IP,
			Port:             port,
			Token:            cfg.Token,
			PinnedCertSHA256: fp,
		}
	}
	return clients, nil
}

func readFingerprint(dir, nodeName string) (string, error) {
	path := dir + "/" + nodeName
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading node-agent cert fingerprint %s: %w", path, err)
	}
	fp := strings.TrimSpace(string(data))
	if fp == "" {
		return "", fmt.Errorf("node-agent cert fingerprint %s is empty", path)
	}
	return fp, nil
}
