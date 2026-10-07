#!/bin/sh
set -eu

repo=afjcjsbx/picoclaw
os=$(uname -s)
machine=$(uname -m)

case "$os" in
  Linux|Darwin) ;;
  *) echo "Unsupported operating system: $os" >&2; exit 1 ;;
esac

case "$machine" in
  x86_64|amd64) arch=x86_64 ;;
  aarch64|arm64) arch=arm64 ;;
  armv6l) arch=armv6 ;;
  armv7l) arch=armv7 ;;
  loongarch64) arch=loong64 ;;
  mipsel) arch=mipsle ;;
  riscv64|s390x) arch=$machine ;;
  *) echo "Unsupported architecture: $machine" >&2; exit 1 ;;
esac

if [ "$os" = Darwin ] && [ "$arch" != x86_64 ] && [ "$arch" != arm64 ]; then
  echo "Unsupported macOS architecture: $machine" >&2
  exit 1
fi

asset="picoclaw_${os}_${arch}.tar.gz"
base="https://github.com/$repo"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

asset_url=$(curl -fsSLo "$tmp/$asset" -w '%{url_effective}' "$base/releases/latest/download/$asset")
tag=${asset_url#*/releases/download/}
tag=${tag%%/*}
if [ "$tag" = "$asset_url" ]; then
  echo "Could not determine the downloaded release version" >&2
  exit 1
fi

checksums="picoclaw_${tag#v}_checksums.txt"
curl -fsSL "$base/releases/download/$tag/$checksums" -o "$tmp/$checksums"
expected=$(awk -v name="$asset" '$2 == name || $2 == "*" name { print $1; exit }' "$tmp/$checksums")
if [ -z "$expected" ]; then
  echo "No checksum found for $asset" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
else
  echo "Install sha256sum or shasum to verify the download" >&2
  exit 1
fi
if [ "$actual" != "$expected" ]; then
  echo "Checksum verification failed for $asset" >&2
  exit 1
fi

mkdir "$tmp/extracted"
tar -xzf "$tmp/$asset" -C "$tmp/extracted"
if [ ! -f "$tmp/extracted/picoclaw" ] || [ ! -f "$tmp/extracted/picoclaw-launcher" ]; then
  echo "Release archive does not contain both PicoClaw binaries" >&2
  exit 1
fi

install_dir="$HOME/.local/bin"
mkdir -p "$install_dir"
install -m 755 "$tmp/extracted/picoclaw" "$install_dir/picoclaw"
install -m 755 "$tmp/extracted/picoclaw-launcher" "$install_dir/picoclaw-launcher"
nohup "$install_dir/picoclaw-launcher" >/dev/null 2>&1 </dev/null &
echo "PicoClaw installed in $install_dir; the Web UI is starting in your browser."
