#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
make_bin=$(command -v make)
mkdir -p "$tmp_dir/path" "$tmp_dir/empty" "$tmp_dir/opt/go/bin" "$tmp_dir/opt/go/src/errors"
printf '#!/bin/sh\nexit 0\n' > "$tmp_dir/path/go"
cp "$tmp_dir/path/go" "$tmp_dir/opt/go/bin/go"
chmod +x "$tmp_dir/path/go" "$tmp_dir/opt/go/bin/go"
: > "$tmp_dir/opt/go/src/errors/errors.go"

selected_go() {
    PATH="$1" "$make_bin" --no-print-directory -s -f "$repo_dir/build/make/makefile" \
        OPT_GO_ROOT="$2" --eval='print-go:;@echo "$(SYSTEM_GO)"' print-go
}

[ "$(selected_go "$tmp_dir/path" "$tmp_dir/opt/go")" = "$tmp_dir/path/go" ]
[ "$(selected_go "$tmp_dir/empty" "$tmp_dir/opt/go")" = "$tmp_dir/opt/go/bin/go" ]
[ -z "$(selected_go "$tmp_dir/empty" "$tmp_dir/missing")" ]
mkdir -p "$tmp_dir/incomplete/bin"
cp "$tmp_dir/path/go" "$tmp_dir/incomplete/bin/go"
chmod +x "$tmp_dir/incomplete/bin/go"
[ -z "$(selected_go "$tmp_dir/empty" "$tmp_dir/incomplete")" ]

# Exercise every device entry point, including HF's required Go version.
cat > "$tmp_dir/path/go" <<'EOF'
#!/bin/sh
case "$1" in
    version) echo 'go version go1.23.12 linux/amd64' ;;
    build) echo build >> "$GO_DISCOVERY_LOG"; exit 7 ;;
    *) exit 1 ;;
esac
EOF
for device in CCU HF MultibandRadio MultibandHandheld SmallRadio; do
    log="$tmp_dir/$device.log"
    output="$tmp_dir/$device.output"
    if GO_DISCOVERY_LOG="$log" "$make_bin" --no-print-directory \
        -f "$repo_dir/build/make/makefile_$device" SYSTEM_GO="$tmp_dir/path/go" \
        DIST_DIR="$tmp_dir/release" all > "$output" 2>&1; then
        echo "FAIL: $device accepted a failed system Go build" >&2
        exit 1
    fi
    [ "$(wc -l < "$log")" -eq 1 ]
    grep -q 'Error 7' "$output"
    if grep -Eq 'bundled|retrying|sha256sum' "$output"; then
        echo "FAIL: $device switched toolchains after failure" >&2
        exit 1
    fi
done
printf '%s\n' 'GNSSAgent Go discovery passed.'
