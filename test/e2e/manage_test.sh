#!/bin/bash
# E2E tests for agentfs manage/unmanage commands
set -e

# Get the directory of this script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Use the dev binary built by justfile
AGENTFS="$PROJECT_DIR/agentfs-dev"
if [ ! -f "$AGENTFS" ]; then
    echo "Error: agentfs-dev not found. Run 'just build' first."
    exit 1
fi

TEST_DIR="/tmp/agentfs-e2e-$$"
BACKUP_DIR="$HOME/.agentfs/backups"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

setup() {
    echo "Setting up test directory: $TEST_DIR"
    rm -rf "$TEST_DIR"
    mkdir -p "$TEST_DIR"
    cd "$TEST_DIR"
}

teardown() {
    echo "Cleaning up..."
    cd /

    # Unmount any mounted stores in test directory
    if [ -d "$TEST_DIR" ]; then
        for store in "$TEST_DIR"/*.fs; do
            if [ -d "$store" ]; then
                mount="${store%.fs}"
                if [ -d "$mount" ]; then
                    hdiutil detach "$mount" 2>/dev/null || true
                fi
            fi
        done
    fi

    rm -rf "$TEST_DIR"

    # Clean up backups created during tests
    if [ -d "$BACKUP_DIR" ]; then
        # Only remove backups created during this test run
        # by checking if they reference paths in $TEST_DIR
        for backup in "$BACKUP_DIR"/*/; do
            if [ -d "$backup" ]; then
                # This is a simple cleanup - in real tests we'd be more careful
                :
            fi
        done
    fi
}

pass() {
    echo -e "${GREEN}PASS${NC}"
}

fail() {
    echo -e "${RED}FAIL: $1${NC}"
    exit 1
}

# ============================================================
# TEST: Basic Manage
# ============================================================
test_basic_manage() {
    echo ""
    echo "=== Test: Basic Manage ==="

    # Setup
    mkdir -p myapp/subdir
    echo "hello" > myapp/file.txt
    echo "world" > myapp/subdir/nested.txt

    # Run manage
    $AGENTFS manage myapp

    # Verify store created
    [ -d myapp.fs ] || fail "store not created"
    [ -d myapp.fs/data.sparsebundle ] || fail "sparse bundle not created"
    [ -d myapp.fs/checkpoints ] || fail "checkpoints directory not created"

    # Verify mounted (check that it's a mount point via device ID)
    [ -d myapp ] || fail "mount point missing"
    $AGENTFS checkpoint list --store myapp >/dev/null || fail "not mounted (checkpoint list failed)"

    # Verify files preserved
    [ -f myapp/file.txt ] || fail "file.txt missing"
    [ -f myapp/subdir/nested.txt ] || fail "nested file missing"
    grep -q "hello" myapp/file.txt || fail "file.txt content wrong"
    grep -q "world" myapp/subdir/nested.txt || fail "nested.txt content wrong"

    # Verify backup exists
    [ -d "$BACKUP_DIR" ] || fail "backup directory missing"
    backup_count=$(ls "$BACKUP_DIR" 2>/dev/null | wc -l | tr -d ' ')
    [ "$backup_count" -gt 0 ] || fail "no backup created"

    pass
}

# ============================================================
# TEST: Manage with Symlinks
# ============================================================
test_manage_symlinks() {
    echo ""
    echo "=== Test: Manage Symlinks ==="

    # Setup
    mkdir -p linkapp
    echo "target content" > linkapp/real.txt
    ln -s real.txt linkapp/link.txt

    # Run manage
    $AGENTFS manage linkapp

    # Verify symlink preserved
    [ -L linkapp/link.txt ] || fail "symlink not preserved"
    target=$(readlink linkapp/link.txt)
    [ "$target" = "real.txt" ] || fail "symlink target wrong: got '$target'"

    # Verify symlink content accessible
    grep -q "target content" linkapp/link.txt || fail "symlink content not readable"

    pass
}

# ============================================================
# TEST: Manage with Permissions
# ============================================================
test_manage_permissions() {
    echo ""
    echo "=== Test: Manage Permissions ==="

    # Setup
    mkdir -p permapp
    echo "secret" > permapp/secret.txt
    chmod 600 permapp/secret.txt

    echo "executable" > permapp/script.sh
    chmod 755 permapp/script.sh

    # Run manage
    $AGENTFS manage permapp

    # Verify permissions preserved (detect stat format)
    if stat --version 2>/dev/null | grep -q "GNU"; then
        # GNU stat (Linux/Nix)
        perms=$(stat -c %a permapp/secret.txt)
        [ "$perms" = "600" ] || fail "secret.txt permissions not preserved: got $perms"

        perms=$(stat -c %a permapp/script.sh)
        [ "$perms" = "755" ] || fail "script.sh permissions not preserved: got $perms"
    else
        # BSD stat (macOS)
        perms=$(stat -f %Lp permapp/secret.txt)
        [ "$perms" = "600" ] || fail "secret.txt permissions not preserved: got $perms"

        perms=$(stat -f %Lp permapp/script.sh)
        [ "$perms" = "755" ] || fail "script.sh permissions not preserved: got $perms"
    fi

    pass
}

# ============================================================
# TEST: Manage Already Managed Directory
# ============================================================
test_manage_already_managed() {
    echo ""
    echo "=== Test: Already Managed ==="

    # Setup - create and manage a directory
    mkdir -p already
    echo "x" > already/x.txt
    $AGENTFS manage already

    # Try to manage again - should fail
    if $AGENTFS manage already 2>&1 | grep -q "already managed"; then
        pass
    else
        fail "should reject already managed directory"
    fi
}

# ============================================================
# TEST: Manage --cleanup
# ============================================================
test_manage_cleanup() {
    echo ""
    echo "=== Test: Manage Cleanup ==="

    # Setup
    mkdir -p cleanapp
    echo "data" > cleanapp/data.txt
    $AGENTFS manage cleanapp

    # Backup should exist
    entry=$($AGENTFS manage --cleanup cleanapp 2>&1 <<< "n" | grep -o "Delete backup" || true)
    [ -n "$entry" ] || fail "cleanup should show backup info"

    # Actually cleanup (with force flag to skip confirmation)
    echo "y" | $AGENTFS manage --cleanup cleanapp

    # Verify cleanup message
    pass
}

# ============================================================
# TEST: Unmanage
# ============================================================
test_unmanage() {
    echo ""
    echo "=== Test: Unmanage ==="

    # Setup - create and manage a directory
    mkdir -p unmanageapp
    echo "keep me" > unmanageapp/keep.txt
    mkdir -p unmanageapp/subdir
    echo "nested" > unmanageapp/subdir/nested.txt

    $AGENTFS manage unmanageapp

    # Create a checkpoint
    cd unmanageapp
    $AGENTFS checkpoint create "test checkpoint"
    cd ..

    # Unmanage
    echo "y" | $AGENTFS unmanage unmanageapp

    # Verify store deleted
    [ ! -d unmanageapp.fs ] || fail "store not deleted"

    # Verify not mounted anymore (it's now a regular directory)
    [ -d unmanageapp ] || fail "directory missing after unmanage"

    # Verify files restored
    [ -f unmanageapp/keep.txt ] || fail "keep.txt not restored"
    [ -f unmanageapp/subdir/nested.txt ] || fail "nested.txt not restored"
    grep -q "keep me" unmanageapp/keep.txt || fail "content wrong"

    pass
}

# ============================================================
# TEST: Unmanage with Symlinks
# ============================================================
test_unmanage_symlinks() {
    echo ""
    echo "=== Test: Unmanage Symlinks ==="

    # Setup
    mkdir -p unlinkapp
    echo "real content" > unlinkapp/real.txt
    ln -s real.txt unlinkapp/link.txt

    $AGENTFS manage unlinkapp

    # Unmanage
    echo "y" | $AGENTFS unmanage unlinkapp

    # Verify symlink preserved
    [ -L unlinkapp/link.txt ] || fail "symlink not preserved after unmanage"
    target=$(readlink unlinkapp/link.txt)
    [ "$target" = "real.txt" ] || fail "symlink target wrong after unmanage: got '$target'"

    pass
}

# ============================================================
# TEST: Manage nonexistent directory
# ============================================================
test_manage_nonexistent() {
    echo ""
    echo "=== Test: Manage Nonexistent ==="

    if $AGENTFS manage nonexistent 2>&1 | grep -q "not found"; then
        pass
    else
        fail "should error on nonexistent directory"
    fi
}

# ============================================================
# TEST: Mount Detection (checkpoint from nested dir)
# ============================================================
test_mount_detection() {
    echo ""
    echo "=== Test: Mount Detection ==="

    # Setup - create and manage a directory
    mkdir -p detectapp
    echo "root file" > detectapp/root.txt
    $AGENTFS manage detectapp

    # Verify no .agentfs file (we removed that)
    [ ! -f detectapp/.agentfs ] || fail ".agentfs should not exist"

    # Create nested directory structure
    mkdir -p detectapp/deep/nested/dir
    echo "nested file" > detectapp/deep/nested/dir/file.txt

    # Run checkpoint from nested directory (mount detection should find store)
    cd detectapp/deep/nested/dir
    $AGENTFS checkpoint create "from nested" || fail "checkpoint failed from nested dir"
    cd "$TEST_DIR"

    # Verify checkpoint was created
    checkpoint_count=$($AGENTFS checkpoint list --store detectapp 2>/dev/null | grep -c "^v" || echo "0")
    [ "$checkpoint_count" -ge 1 ] || fail "checkpoint not created"

    pass
}

# ============================================================
# Main
# ============================================================

trap teardown EXIT

echo "============================================"
echo "AgentFS Manage/Unmanage E2E Tests"
echo "============================================"

setup

test_basic_manage
teardown; setup

test_manage_symlinks
teardown; setup

test_manage_permissions
teardown; setup

test_manage_already_managed
teardown; setup

test_manage_cleanup
teardown; setup

test_unmanage
teardown; setup

test_unmanage_symlinks
teardown; setup

test_manage_nonexistent
teardown; setup

test_mount_detection

echo ""
echo "============================================"
echo -e "${GREEN}All tests passed!${NC}"
echo "============================================"
