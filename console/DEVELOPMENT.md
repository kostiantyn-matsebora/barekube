# Development

## Local development (no real nodes touched)

```sh
go run ./cmd/console -config config.local.yaml
```

Pure mock mode — no process, no network, no node, fastest iteration loop
for the orchestrator/state-machine/UI logic. Visit `http://localhost:8080`
— the one-time UI token is printed to stdout on first run if
`CONSOLE_UI_TOKEN` isn't set.

To exercise a real node-agent process (safe endpoints only — never run
`/reboot` against a node-agent on your own dev machine, default polkit
policy can let the active console user reboot without a prompt):

```sh
NODE_AGENT_TOKEN=<token> go run ./cmd/node-agent \
  -addr :9199 -cert <cert.pem> -key <key.pem> \
  -kubectl-bin kubectl -kubectl-subcommand "" -kubectl-context rancher-desktop
```

then `curl` its endpoints directly, or point a `console` config at it
with `nodeAgent.mock: false`.

For a disposable target where `/reboot` is safe to actually call, use the cluster test
(`cli/tests/e2e`): it installs the real node-agent, with `barekube install node-agent`,
on LXD containers, and runs the console against them.


## Layout

```
cmd/console/main.go     wiring only: config, node-agent clients, service, web server
cmd/node-agent/main.go  flags and TLS listener for the node-side daemon
deploy/                 docker-compose.yml, token generator, check-running.sh
internal/config/        node registry, per-node operations, timing, cluster shutdown policy
internal/events/        the typed event log, a ring buffer with filters
internal/observe/       the observer, the four facts per node and the derived state
internal/flows/         the five workflows, declared as specs on luno/workflow
internal/app/           ops over the node-agents, the prober, and the service the web layer uses
internal/agentd/        the node-agent handlers, with an injectable command runner
internal/nodeagent/     client (HTTPS, pinned certificate, bearer token), master pool, mock
internal/web/           UI, JSON API, the three auth doors, embedded mermaid
```


## Tests

```sh
GOTOOLCHAIN=auto go test -race -cover ./internal/...
```

The bar is 85 % statement coverage per package. Workflow tests run every branch
against a simulated cluster and a fake clock, so no test waits in real time.
`cmd/` holds no logic and is not counted.
