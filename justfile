# agentfs development justfile

# Build dev binary (doesn't affect global)
build:
    go build -o agentfs-dev ./cmd/agentfs

# Run dev binary
run *args:
    go run ./cmd/agentfs {{args}}

# Run E2E tests
test: build
    ./test/e2e/manage_test.sh

# Check what version is globally installed
which:
    @echo "Global: $(which agentfs)"
    @readlink -f $(which agentfs) || true
    @echo ""
    @agentfs --help 2>&1 | head -1

# Update flake.lock in ~/config to pick up latest agentfs
# Then run darwin-rebuild to install
upgrade:
    @echo "Updating ~/config/flake.lock..."
    nix flake update agentfs --flake ~/config
    @echo ""
    @echo "Run 'darwin-rebuild switch --flake ~/config' to install"

# Full upgrade: update lock and rebuild
upgrade-now:
    nix flake update agentfs --flake ~/config
    darwin-rebuild switch --flake ~/config

# Show diff between dev and global
diff:
    @echo "=== Dev version ==="
    go run ./cmd/agentfs --help 2>&1 | head -3
    @echo ""
    @echo "=== Global version ==="
    agentfs --help 2>&1 | head -3
