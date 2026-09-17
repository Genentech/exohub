#!/bin/sh
set -e

GITHUB_REPO="Genentech/exohub"
S5CMD_REPO="peak/s5cmd"
S5CMD_VERSION="${S5CMD_VERSION:-2.3.0}"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

OS=$(uname | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
  ARCH=amd64
elif [ "$ARCH" = "aarch64" ]; then
  ARCH=arm64
fi

version=latest
dest_arg=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    -d|--dest)
      dest_arg="$2"
      shift 2
      ;;
    -v|--version)
      version="$2"
      shift 2
      ;;
    *)
      if [ "$1" != "" ] && [ "$version" = "latest" ]; then
        version="$1"
      fi
      shift
      ;;
  esac
done

# Resolve the exo release tag from GitHub Releases API.
# Releases are tagged as exo/vX.Y.Z; the API returns the tag_name field.
resolve_exo_tag() {
  if [ "$version" = "latest" ]; then
    tag=$(curl --silent "https://api.github.com/repos/${GITHUB_REPO}/releases/latest" \
      | grep '"tag_name"' | head -1 | sed 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
    if [ -z "$tag" ]; then
      echo "Failed to resolve latest release tag from GitHub" >&2
      exit 1
    fi
    echo "$tag"
  else
    echo "$version"
  fi
}

# Extract the semver portion from a tag like exo/v1.2.3 → 1.2.3
tag_to_version() {
  echo "$1" | sed 's|.*/v||; s|^v||'
}

install_dir=""
if [ "$dest_arg" != "" ]; then
  install_dir="$dest_arg"
elif [ "$(id -u)" -eq 0 ] && [ -w /usr/local/bin ]; then
  install_dir="/usr/local/bin"
else
  install_dir="$HOME/.local/bin"
fi

mkdir -p "$install_dir"

# Determine release tag and semver for exo binaries
exo_tag=$(resolve_exo_tag)
exo_ver=$(tag_to_version "$exo_tag")

echo "Installing exo ${exo_ver} from github.com/${GITHUB_REPO} (tag: ${exo_tag})"
echo "Install directory: ${install_dir}"

RELEASE_BASE="https://github.com/${GITHUB_REPO}/releases/download/${exo_tag}"

# verify_checksum downloads the checksums file and verifies the archive.
# Hard-fails if the checksums file cannot be fetched or the entry is absent.
# Usage: verify_checksum <tmp_dir> <archive_path> <archive_filename> <checksum_url>
verify_checksum() {
  tmp_dir="$1"
  archive_path="$2"
  archive="$3"
  checksum_url="$4"

  checksum_file="${tmp_dir}/checksums.txt"
  if ! curl --fail -sS -L "$checksum_url" -o "$checksum_file"; then
    echo "Failed to download checksums from ${checksum_url}" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi

  expected=$(grep "  ${archive}$" "$checksum_file" | awk '{print $1}')
  if [ -z "$expected" ]; then
    echo "Checksum entry for ${archive} not found in ${checksum_url}" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$archive_path" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$archive_path" | awk '{print $1}')
  else
    echo "No sha256sum or shasum found; cannot verify checksum" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi

  if [ "$expected" != "$actual" ]; then
    echo "Checksum mismatch for ${archive}: expected ${expected}, got ${actual}" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi
}

# install_exo_binary downloads a goreleaser-produced archive and extracts the binary.
# goreleaser v2 default archive: <project>_<version>_<os>_<arch>.tar.gz
# containing a binary named <binary_name>.
install_exo_binary() {
  project="$1"
  binary_name="$2"
  outname="$3"

  archive="${project}_${exo_ver}_${OS}_${ARCH}.tar.gz"
  url="${RELEASE_BASE}/${archive}"
  checksum_url="${RELEASE_BASE}/${project}_${exo_ver}_checksums.txt"

  echo "Downloading ${binary_name} ${exo_ver} (${OS}/${ARCH})"
  tmp_dir=$(mktemp -d)
  archive_path="${tmp_dir}/${archive}"

  if ! curl --fail -sS -L "$url" -o "$archive_path"; then
    echo "Error downloading ${url}" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi

  verify_checksum "$tmp_dir" "$archive_path" "$archive" "$checksum_url"

  tar -xzf "$archive_path" -C "$tmp_dir" "$binary_name" 2>/dev/null \
    || tar -xzf "$archive_path" -C "$tmp_dir" 2>/dev/null
  binary_path="${tmp_dir}/${binary_name}"
  if [ ! -f "$binary_path" ]; then
    binary_path=$(find "$tmp_dir" -name "$binary_name" -type f | head -1)
  fi
  if [ -z "$binary_path" ] || [ ! -f "$binary_path" ]; then
    echo "Binary ${binary_name} not found in archive ${archive}" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi
  chmod +x "$binary_path"
  mv "$binary_path" "$outname"
  rm -rf "$tmp_dir"
  echo "Installed ${binary_name} -> ${outname}"
}

# s5cmd archive naming from peak/s5cmd releases:
#   Linux amd64: s5cmd_<ver>_Linux-64bit.tar.gz
#   Linux arm64: s5cmd_<ver>_Linux-arm64.tar.gz
#   macOS amd64: s5cmd_<ver>_macOS-64bit.tar.gz
#   macOS arm64: s5cmd_<ver>_macOS-arm64.tar.gz
install_s5cmd() {
  outname="$1"
  ver="$S5CMD_VERSION"

  if [ "$OS" = "linux" ] && [ "$ARCH" = "amd64" ]; then
    archive="s5cmd_${ver}_Linux-64bit.tar.gz"
  elif [ "$OS" = "linux" ] && [ "$ARCH" = "arm64" ]; then
    archive="s5cmd_${ver}_Linux-arm64.tar.gz"
  elif [ "$OS" = "darwin" ] && [ "$ARCH" = "amd64" ]; then
    archive="s5cmd_${ver}_macOS-64bit.tar.gz"
  elif [ "$OS" = "darwin" ] && [ "$ARCH" = "arm64" ]; then
    archive="s5cmd_${ver}_macOS-arm64.tar.gz"
  else
    echo "s5cmd: unsupported platform ${OS}/${ARCH}" >&2
    exit 1
  fi

  url="https://github.com/${S5CMD_REPO}/releases/download/v${ver}/${archive}"
  checksum_url="https://github.com/${S5CMD_REPO}/releases/download/v${ver}/s5cmd_${ver}_checksums.txt"

  echo "Downloading s5cmd ${ver} (${OS}/${ARCH})"
  tmp_dir=$(mktemp -d)
  archive_path="${tmp_dir}/${archive}"

  if ! curl --fail -sS -L "$url" -o "$archive_path"; then
    echo "Error downloading ${url}" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi

  verify_checksum "$tmp_dir" "$archive_path" "$archive" "$checksum_url"

  tar -xzf "$archive_path" -C "$tmp_dir" s5cmd 2>/dev/null \
    || tar -xzf "$archive_path" -C "$tmp_dir" 2>/dev/null
  binary_path="${tmp_dir}/s5cmd"
  if [ ! -f "$binary_path" ]; then
    binary_path=$(find "$tmp_dir" -name "s5cmd" -type f | head -1)
  fi
  if [ -z "$binary_path" ] || [ ! -f "$binary_path" ]; then
    echo "s5cmd binary not found in archive ${archive}" >&2
    rm -rf "$tmp_dir"
    exit 1
  fi
  chmod +x "$binary_path"
  mv "$binary_path" "$outname"
  rm -rf "$tmp_dir"
  echo "Installed s5cmd -> ${outname}"
}

install_exo_binary "exo"                              "exo"                              "$install_dir/exo"
install_exo_binary "exo-credential-helper"            "exo-credential-helper"            "$install_dir/exo-credential-helper"
install_exo_binary "git-annex-remote-s5cmd"           "git-annex-remote-s5cmd"           "$install_dir/git-annex-remote-s5cmd"
install_exo_binary "git-annex-remote-drive"           "git-annex-remote-drive"           "$install_dir/git-annex-remote-drive"
install_exo_binary "git-annex-remote-artifactdb-export" "git-annex-remote-artifactdb-export" "$install_dir/git-annex-remote-artifactdb-export"
install_s5cmd "$install_dir/s5cmd"

echo ""
echo "git-annex is not bundled in this installer."
echo "Install it via your system package manager:"
echo "  macOS:  brew install git-annex"
echo "  Debian/Ubuntu: sudo apt-get install git-annex"
echo "  Other:  https://git-annex.branchable.com/install/"

if ! echo ":$PATH:" | grep -q ":$install_dir:"; then
  echo ""
  echo "WARNING: $install_dir is not in your PATH."
  echo "Add it with:"
  echo "  export PATH=\"$install_dir:\$PATH\""
  echo "Then add that line to your shell profile (e.g., ~/.bashrc or ~/.zshrc)."
fi

echo ""
ls -lah \
  "$install_dir/exo" \
  "$install_dir/exo-credential-helper" \
  "$install_dir/git-annex-remote-s5cmd" \
  "$install_dir/git-annex-remote-drive" \
  "$install_dir/git-annex-remote-artifactdb-export" \
  "$install_dir/s5cmd"
