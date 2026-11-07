# Security policy

## Supported versions

| Version | Supported |
|---|---|
| Latest GitHub release | Yes |
| Older releases | No, run `barekube upgrade` first |

## Reporting a vulnerability

Report privately through GitHub's private vulnerability reporting:
<https://github.com/kostiantyn-matsebora/barekube/security/advisories/new>

Do not open a public issue for a suspected vulnerability. This is a single-maintainer project, so the acknowledgement target is 7 days, best effort.

## Release integrity

Each release carries a SHA-256 checksum, a keyless [Sigstore](https://www.sigstore.dev) signature and a build provenance attestation. They are produced by the release workflow in this repository, never on a developer machine. `barekube upgrade` verifies the checksum and the signature before it replaces the installed script, and leaves it alone if either check fails. The README shows how to verify a download by hand.

## Scope

Barekube runs with root privileges on your nodes and downloads installers (k3s, gVisor) from their upstream sources. A flaw in how barekube fetches, verifies or runs them is in scope. A vulnerability in k3s, gVisor, WireGuard or Longhorn themselves belongs to those projects.
