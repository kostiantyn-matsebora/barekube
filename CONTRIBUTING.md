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

5. Open a pull request. CI runs the tests and fails when the committed release differs from a fresh regeneration.

Pull requests are squash-merged and the branch is deleted after the merge.
