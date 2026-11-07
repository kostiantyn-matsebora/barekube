# Contributing

Thank you for looking. The [code of conduct](CODE_OF_CONDUCT.md) applies here, and a security problem goes to [SECURITY.md](SECURITY.md), never to an issue.

## Start with an issue or a discussion

- **A bug:** open an issue with the bug template.
- **A feature:** open an issue with the feature template. Say what you are trying to do before what you want built.
- **A question or an idea:** use [Discussions](https://github.com/kostiantyn-matsebora/barekube/discussions).

Please open an issue before a large pull request.

## Layout

The CLI is written with [bashly](https://bashly.dev) under `cli/`:

| Path | Is |
|---|---|
| `cli/bashly.yml` | command definitions |
| `cli/commands/` | one script per command |
| `cli/src/` | shared functions |
| `cli/tests/` | unit and end-to-end tests |
| `cli/release/barekube` | the generated single-file CLI users download |
| `orchestration/ansible/` | the Ansible collection `kostiantyn_matsebora.barekube` (playbooks in `playbooks/`, the role in `roles/`) that builds, changes and removes a whole cluster with the CLI, and `play.sh`, which runs it from the release archive (in Docker if Ansible is not installed); `ansible-lint` checks it in CI |

## Working on a change

1. Branch from `master`.
2. Change the sources, never `cli/release/barekube` by hand.
3. Regenerate the release with the version CI pins:

   ```bash
   cd cli
   docker run --rm --user "$(id -u):$(id -g)" --volume "$PWD:/app" dannyben/bashly:1.2.13 generate
   ```

4. Run the tests:

   ```bash
   cli/tests/run.sh --coverage
   ```

5. Open a pull request. CI runs the tests, fails when the committed release differs from a fresh regeneration, and runs the installation steps from the README to check that they still install a working barekube. You can run that check yourself with `cli/tests/readme_install.sh local`.

Pull requests are squash-merged and the branch is deleted after the merge.

## The cluster test

The `E2E cluster` workflow builds a real cluster of three masters and two agents (LXD containers on one GitHub runner) with the barekube script of the commit, then checks it end to end. It builds it four times: with the barekube commands run node by node, and with the Ansible playbook (the release archive, run through `play.sh` in Docker over SSH), each with and without gVisor as the default. The checks cover the control plane, the WireGuard mesh, pod and service networking, gVisor, the Longhorn prerequisites, failover and the day-two commands. It runs every day, on demand from the Actions tab, and before every release; a release is not published unless it passes. It is not part of the pull request checks because it takes about ten minutes.

The scripts are in `cli/tests/e2e/`.

## Releasing

Releases are published by CI from a version tag, never by hand.

1. In a pull request, set the new `version` in `cli/bashly.yml`, `barekube_version` (with the `v`) in `orchestration/ansible/roles/barekube/defaults/main.yml` and `version` in `orchestration/ansible/galaxy.yml`, regenerate the release as above, and merge it.
2. Tag the merge commit on `master` and push the tag:

   ```bash
   git tag v0.3.0
   git push origin v0.3.0
   ```

3. The release workflow runs the tests and the five-node cluster test, checks that the tag matches the version, builds the script, and publishes it to GitHub Releases with a checksum, a Sigstore signature and a provenance attestation, together with `barekube-ansible.tar.gz` (the packed playbooks and `play.sh`, from `orchestration/package.sh`) and the Ansible collection, each with the same. A last job publishes the collection to Ansible Galaxy, but only if the repository has a `GALAXY_API_KEY` secret (an API token from your Galaxy profile, for the `kostiantyn_matsebora` namespace); without it the job only prints a notice.

The workflow refuses a tag that is not on `master`, that does not match `cli/bashly.yml`, or whose version differs from `barekube_version` in `orchestration/ansible/roles/barekube/defaults/main.yml` or from `version` in `orchestration/ansible/galaxy.yml`. Change all three versions in the release commit.
