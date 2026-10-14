#!/bin/bash
set -e

# Update vendorHash from Nix output, then verify the resulting default.nix.
# Run from the repository root with local Nix or Docker.

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}==>${NC} $1"
}

log_success() {
    echo -e "${GREEN}✓${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}⚠${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1" >&2
}

# Check if we're in the repo root
if [ ! -f "default.nix" ]; then
    log_error "Must run from repository root (default.nix not found)"
    exit 1
fi

# Determine if we should use Docker for Nix
USE_DOCKER=false

if ! command -v nix &> /dev/null; then
    log_warning "nix command not found locally"

    # Check if Docker is available
    if command -v docker &> /dev/null; then
        log_info "Docker detected - will use nixos/nix Docker image"
        USE_DOCKER=true

        # Pull the Nix Docker image if not present
        if ! docker image inspect nixos/nix:latest &> /dev/null; then
            log_info "Pulling nixos/nix:latest Docker image..."
            docker pull nixos/nix:latest
        fi
    else
        log_error "Neither nix nor docker found."
        log_error "Please install one of:"
        log_error "  - Nix: https://nixos.org/download.html"
        log_error "  - Docker: https://docs.docker.com/get-docker/"
        exit 1
    fi
fi

# Function to run nix commands (either locally or via Docker)
run_nix() {
    if [ "$USE_DOCKER" = true ]; then
        # Get absolute path of current directory
        ABS_PWD=$(cd "$(pwd)" && pwd)

        # Run nix in Docker container with current directory mounted
        # Mount at both /workspace and the original path (for git worktree compatibility)
        DOCKER_VOLUMES=(-v "$ABS_PWD:/workspace" -v "$ABS_PWD:$ABS_PWD")

        # If this is a git worktree, mount the main git directory too
        if [ -f .git ] && grep -q "^gitdir: " .git; then
            GIT_COMMON_DIR=$(git rev-parse --git-common-dir 2>/dev/null || echo "")
            if [ -n "$GIT_COMMON_DIR" ] && [ -d "$GIT_COMMON_DIR" ]; then
                # Mount the main git directory at the same path
                DOCKER_VOLUMES+=(-v "$GIT_COMMON_DIR:$GIT_COMMON_DIR")
            fi
        fi

        docker run --rm \
            "${DOCKER_VOLUMES[@]}" \
            -w /workspace \
            nixos/nix:latest \
            nix "$@" --extra-experimental-features "nix-command flakes"
    else
        nix "$@"
    fi
}

log_info "Updating vendorHash in default.nix..."
echo ""

# Get current vendorHash
CURRENT_HASH=$(grep 'vendorHash = ' default.nix | sed 's/.*"\(.*\)".*/\1/')
log_info "Current vendorHash: $CURRENT_HASH"

# Step 1: Set to a known-bad hash to trigger an error
log_info "Setting temporary bad hash to trigger error..."
FAKE_HASH="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

# Keep operator backups and concurrent runs' logs separate.
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/beads-nix-vendorhash.XXXXXX")
BACKUP="$WORK_DIR/default.nix"
BUILD_LOG="$WORK_DIR/build.log"
VERIFY_LOG="$WORK_DIR/verify.log"
VERIFIED=false
cp -p default.nix "$BACKUP"

cleanup() {
    status=$?
    trap - EXIT
    if [ "$VERIFIED" = false ]; then
        if mv -f "$BACKUP" default.nix; then
            log_info "Restored original default.nix"
        else
            log_error "Could not restore default.nix; backup retained at $BACKUP"
            status=1
        fi
    fi
    if [ "$status" -eq 0 ] && [ "$VERIFIED" = true ]; then
        rm -rf "$WORK_DIR"
    else
        log_info "Logs and any recovery artifacts retained at $WORK_DIR"
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Detect sed version (GNU vs BSD)
if sed --version 2>/dev/null | grep -q "GNU sed"; then
    SED_INPLACE=(sed -i)
else
    SED_INPLACE=(sed -i '')
fi

# Update to fake hash
"${SED_INPLACE[@]}" "s|vendorHash = \".*\";|vendorHash = \"$FAKE_HASH\";|" default.nix

# Step 2: Run nix build and capture the error
log_info "Building to get actual hash (this will fail intentionally)..."
# Preserve the real build status; missing hash output is not a success signal.
BUILD_EXIT=0
run_nix build > "$BUILD_LOG" 2>&1 || BUILD_EXIT=$?

ACTUAL_HASH=$(grep -o 'got:[[:space:]]*sha256-[A-Za-z0-9+/=]*' "$BUILD_LOG" | head -1 | sed 's/got:[[:space:]]*//')
if [ -z "$ACTUAL_HASH" ]; then
    if [ "$BUILD_EXIT" -ne 0 ]; then
        if grep -q "hash mismatch" "$BUILD_LOG"; then
            log_error "Build reported a hash mismatch (exit $BUILD_EXIT) but no extractable got: hash"
        else
            log_error "Build failed for a reason other than a hash mismatch (exit $BUILD_EXIT)"
        fi
        tail -30 "$BUILD_LOG"
        exit 1
    fi
    # Even a successful fake-hash build must verify the restored real file.
    cp -p "$BACKUP" default.nix
    ACTUAL_HASH="$CURRENT_HASH"
else
    log_success "Extracted actual hash: $ACTUAL_HASH"
    "${SED_INPLACE[@]}" "s|vendorHash = \".*\";|vendorHash = \"$ACTUAL_HASH\";|" default.nix
fi

log_info "Verifying update with a clean build..."
# Scope the contributor's pipefail guard to this verification pipeline.
set -o pipefail
if run_nix build 2>&1 | tee "$VERIFY_LOG"; then
    set +o pipefail
    VERIFIED=true
    log_success "Build successful! vendorHash verified."
    echo "  Old hash: $CURRENT_HASH"
    echo "  New hash: $ACTUAL_HASH"
else
    set +o pipefail
    log_error "Build failed after updating or restoring the hash"
    log_error "Verification log: $VERIFY_LOG"
    exit 1
fi
