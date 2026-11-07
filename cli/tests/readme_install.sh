#!/usr/bin/env bash
# Runs the installation instructions from the README, exactly as written, in a
# clean HOME, then checks that barekube is installed and works.
#
#   readme_install.sh local            download from the locally built files
#                                      (a stand-in wget serves cli/release/*)
#   readme_install.sh release VERSION  download from the published GitHub
#                                      release, and run the verification too
#
# The README commands are extracted from its fenced Bash blocks, so the
# instructions cannot drift from what is tested.
set -euo pipefail

MODE="${1:-local}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
README="$ROOT/README.md"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# Fenced Bash blocks of a README section, in order. $1: first heading, $2: the
# heading that ends the range ("" = through the next heading of any level).
blocks_between() {
  awk -v start="$1" -v stop="$2" '
    $0 == start { on = 1; next }
    on && stop != "" && $0 == stop { exit }
    on && stop == "" && /^#+ / { exit }
    on && /^ *```[Bb]ash$/ { inblock = 1; next }
    on && /^ *```$/ { inblock = 0; print ""; next }
    on && inblock { sub(/^  /, ""); print }
  ' "$README"
}

INSTALL_STEPS="$(blocks_between '## Installation' '### Verifying the download')"
VERIFY_STEPS="$(blocks_between '### Verifying the download' '## Usage')"
[[ -n "$INSTALL_STEPS" ]] || fail "no Bash blocks found in the README Installation section"

case "$MODE" in
  local)
    EXPECTED="$(sed -n 's/^version: *//p' "$ROOT/cli/bashly.yml" | tr -d '\r')"
    # A wget that serves the locally built release files instead of GitHub.
    mkdir -p "$WORK/stub" "$WORK/served"
    cp "$ROOT/cli/release/barekube" "$WORK/served/barekube"
    (cd "$WORK/served" && sha256sum barekube >SHA256SUMS)
    cat >"$WORK/stub/wget" <<'STUB'
#!/usr/bin/env bash
out=""
urls=()
while (($#)); do
  case "$1" in
    -O) out="$2"; shift ;;
    -q) ;;
    *) urls+=("$1") ;;
  esac
  shift
done
for url in "${urls[@]}"; do
  file="${url##*/}"
  [[ -f "$SERVED_DIR/$file" ]] || { echo "wget stub: nothing served for $url" >&2; exit 8; }
  cp "$SERVED_DIR/$file" "${out:-$file}"
done
STUB
    chmod +x "$WORK/stub/wget"
    export SERVED_DIR="$WORK/served"
    export PATH="$WORK/stub:$PATH"
    ;;
  release)
    VERSION="${2:?usage: readme_install.sh release VERSION}"
    EXPECTED="${VERSION#v}"
    ;;
  *) fail "unknown mode: $MODE" ;;
esac

export HOME="$WORK/home"
mkdir -p "$HOME"
: >"$HOME/.bashrc"

echo "== Running the README installation steps ($MODE)"
printf '%s\n' "$INSTALL_STEPS"
echo "== Output"
(set -e; cd "$HOME"; eval "$INSTALL_STEPS")

[[ -x "$HOME/bin/barekube" ]] || fail "$HOME/bin/barekube is not an executable file"

echo "== Checking the installed CLI"
version="$("$HOME/bin/barekube" --version)"
[[ "$version" == "$EXPECTED" ]] || fail "version is [$version], expected [$EXPECTED]"

# The README also creates an alias; an interactive shell must pick it up.
via_alias="$(bash -ic 'barekube --version' 2>/dev/null)" || fail "the barekube alias does not work in a new shell"
[[ "$via_alias" == "$EXPECTED" ]] || fail "alias printed [$via_alias], expected [$EXPECTED]"

help_text="$("$HOME/bin/barekube" --help)" || fail "barekube --help failed"
for word in install k3s upgrade; do
  [[ "$help_text" == *"$word"* ]] || fail "--help does not mention [$word]"
done

if [[ "$MODE" == "release" ]]; then
  [[ -n "$VERIFY_STEPS" ]] || fail "no Bash blocks found in the README verification section"
  echo "== Running the README verification steps"
  printf '%s\n' "$VERIFY_STEPS"
  echo "== Output"
  (set -e; eval "$VERIFY_STEPS")
fi

echo "OK: the README installation steps install barekube $EXPECTED and it works"
