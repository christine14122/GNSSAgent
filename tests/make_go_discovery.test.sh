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
printf '%s\n' 'GNSSAgent Go discovery passed.'
