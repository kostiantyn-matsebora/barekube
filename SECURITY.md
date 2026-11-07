# Security policy

## Supported versions

| Version | Supported |
|---|---|
| Latest `cli/release/barekube` on `master` | Yes |
| Older copies of the script | No, run `barekube upgrade` first |

## Reporting a vulnerability

Report privately through GitHub's private vulnerability reporting:
<https://github.com/kostiantyn-matsebora/barekube/security/advisories/new>

Do not open a public issue for a suspected vulnerability. This is a single-maintainer project, so the acknowledgement target is 7 days, best effort.

## Scope

Barekube runs with root privileges on your nodes and downloads installers (k3s, gVisor) from their upstream sources. A flaw in how barekube fetches, verifies or runs them is in scope. A vulnerability in k3s, gVisor, WireGuard or Longhorn themselves belongs to those projects.
