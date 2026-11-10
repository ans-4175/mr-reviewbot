#!/bin/sh
# Installs the reviewbot binary from a release. Setup itself is `reviewbot init` (see SETUP.md).
#
#   curl -fsSL https://raw.githubusercontent.com/<owner>/mr-reviewbot/main/scripts/install.sh | REVIEWBOT_REPO=<owner>/mr-reviewbot sh
#
# Env: REVIEWBOT_REPO     GitHub owner/repo to download from
#      REVIEWBOT_VERSION  release tag (default: latest)
#      REVIEWBOT_BIN_DIR  install dir (default: ~/.local/bin)
#      REVIEWBOT_BASE_URL download from here instead of GitHub (a mirror, or file:///path/to/dist)
set -eu

bin_dir="${REVIEWBOT_BIN_DIR:-$HOME/.local/bin}"
version="${REVIEWBOT_VERSION:-latest}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=arm64 ;;
  x86_64|amd64) arch=amd64 ;;
  *) echo "unsupported CPU: $(uname -m)" >&2; exit 1 ;;
esac

if [ -n "${REVIEWBOT_BASE_URL:-}" ]; then
  base="$REVIEWBOT_BASE_URL"
elif [ -n "${REVIEWBOT_REPO:-}" ]; then
  if [ "$version" = latest ]; then
    base="https://github.com/$REVIEWBOT_REPO/releases/latest/download"
  else
    base="https://github.com/$REVIEWBOT_REPO/releases/download/$version"
  fi
else
  echo "set REVIEWBOT_REPO=<owner>/mr-reviewbot (or REVIEWBOT_BASE_URL)" >&2
  exit 1
fi

name="mr-reviewbot_${os}_${arch}"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "downloading $base/$name.tar.gz"
curl -fsSL "$base/$name.tar.gz" -o "$tmp/$name.tar.gz"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

want="$(grep " $name.tar.gz\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then
  got="$(sha256sum "$tmp/$name.tar.gz" | cut -d' ' -f1)"
else
  got="$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d' ' -f1)"
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "checksum mismatch for $name.tar.gz" >&2
  exit 1
fi

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
mkdir -p "$bin_dir"
install -m 0755 "$tmp/$name/reviewbot" "$bin_dir/reviewbot.new"
mv "$bin_dir/reviewbot.new" "$bin_dir/reviewbot"

echo "installed $("$bin_dir/reviewbot" version) to $bin_dir/reviewbot"
case ":$PATH:" in *":$bin_dir:"*) ;; *) echo "note: $bin_dir is not on your PATH" ;; esac
echo "next: reviewbot init --repo group/project   (see SETUP.md)"
