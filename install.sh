#!/bin/sh
# zedrun-go installer — fetch a release binary and put it on your PATH.
#
#   curl -fsSL https://raw.githubusercontent.com/zedrun/zedrun-go/main/install.sh | sh
#
# Environment overrides:
#   ZEDRUN_REPO      owner/repo         (default zedrun/zedrun-go)
#   ZEDRUN_BASE_URL  release asset base (default the latest GitHub release)
#   ZEDRUN_HOME      install directory  (default $HOME/.local/bin)
#   ZEDRUN_OS        force OS: linux|darwin
#   ZEDRUN_ARCH      force arch: amd64|arm64
set -eu

BIN=zedrun-go
REPO=${ZEDRUN_REPO:-zedrun/zedrun-go}
BASE=${ZEDRUN_BASE_URL:-https://github.com/$REPO/releases/latest/download}
PREFIX=${ZEDRUN_HOME:-${PREFIX:-$HOME/.local/bin}}

usage() {
	cat <<EOF
zedrun-go installer

usage: install.sh [--uninstall] [--help]

  --uninstall   remove $BIN from $PREFIX
  --help        show this message

environment:
  ZEDRUN_REPO      owner/repo          (default $REPO)
  ZEDRUN_BASE_URL  release asset base  (default $BASE)
  ZEDRUN_HOME      install directory   (default $PREFIX)
  ZEDRUN_OS        force OS: linux|darwin
  ZEDRUN_ARCH      force arch: amd64|arm64
EOF
}

uninstall=0
for arg in "$@"; do
	case "$arg" in
		-h|--help) usage; exit 0 ;;
		--uninstall) uninstall=1 ;;
		*) echo "unknown option: $arg" >&2; usage >&2; exit 2 ;;
	esac
done

if [ "$uninstall" = 1 ]; then
	if [ -f "$PREFIX/$BIN" ]; then
		rm -f "$PREFIX/$BIN"
		echo "removed $PREFIX/$BIN"
	else
		echo "nothing to remove at $PREFIX/$BIN"
	fi
	exit 0
fi

detect_os() {
	if [ -n "${ZEDRUN_OS:-}" ]; then echo "$ZEDRUN_OS"; return; fi
	case "$(uname -s)" in
		Linux) echo linux ;;
		Darwin) echo darwin ;;
		*) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
	esac
}

detect_arch() {
	if [ -n "${ZEDRUN_ARCH:-}" ]; then echo "$ZEDRUN_ARCH"; return; fi
	case "$(uname -m)" in
		x86_64|amd64) echo amd64 ;;
		arm64|aarch64) echo arm64 ;;
		*) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
	esac
}

os=$(detect_os)
arch=$(detect_arch)
asset="$BIN-$os-$arch"
url="$BASE/$asset"

tmp=$(mktemp "${TMPDIR:-/tmp}/zedrun-go.XXXXXX")
trap 'rm -f "$tmp"' EXIT INT TERM

echo "downloading $url"
if command -v curl >/dev/null 2>&1; then
	curl -fsSL "$url" -o "$tmp"
elif command -v wget >/dev/null 2>&1; then
	wget -qO "$tmp" "$url"
else
	echo "need curl or wget on PATH" >&2
	exit 1
fi

chmod +x "$tmp"
if ! "$tmp" --version >/dev/null 2>&1; then
	echo "the downloaded binary did not run; leaving your system unchanged" >&2
	exit 1
fi

mkdir -p "$PREFIX"
mv "$tmp" "$PREFIX/$BIN"
trap - EXIT INT TERM

echo "installed $("$PREFIX/$BIN" --version) to $PREFIX/$BIN"
case ":$PATH:" in
	*":$PREFIX:"*) ;;
	*) echo "note: add $PREFIX to your PATH to run $BIN" ;;
esac
