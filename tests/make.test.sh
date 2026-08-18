#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
project_dir="$tmp_dir/project"
make_dir="$project_dir/build/scripts/make"
outside_dir="$tmp_dir/outside"

mkdir -p "$make_dir" "$outside_dir"
for file in Makefile Makefile_CCU Makefile_HF Makefile_MultibandRadio Makefile_MultibandHandheld; do
    cp "$repo_dir/build/scripts/make/$file" "$make_dir/$file"
done

fail() {
    printf '%s\n' "FAIL: $*" >&2
    exit 1
}

require_contains() {
    case "$1" in
        *"$2"*) ;;
        *) fail "expected $3 to contain: $2" ;;
    esac
}

dry_run() {
    (
        cd "$outside_dir"
        make -n -f "$make_dir/Makefile" "$1"
    )
}

assert_target() {
    target=$1 go_root=$2 version=$3 environment=$4 output_name=$5 link_target=$6
    output=$(dry_run "$target")
    require_contains "$output" "$environment" "$target dry run"
    require_contains "$output" "$go_root" "$target dry run"
    require_contains "$output" "go version $version linux/amd64" "$target dry run"
    require_contains "$output" "$output_name" "$target dry run"
    require_contains "$output" "gnssagent/internal/buildinfo.Target=$link_target" "$target dry run"
}

assert_target ccu '.go1.26.4-linux-amd64' 'go1.26.4' 'GOOS=linux GOARCH=amd64' 'GNSSAgent-CCU' ccu
assert_target multiband-radio '.go1.26.4-linux-amd64' 'go1.26.4' 'GOOS=linux GOARCH=arm64 GOARM64=v8.0' 'GNSSAgent-MultibandRadio' multiband-radio
assert_target multiband-handheld '.go1.26.4-linux-amd64' 'go1.26.4' 'GOOS=linux GOARCH=arm GOARM=7' 'GNSSAgent-MultibandHandheld' multiband-handheld
assert_target hf '.go1.23.12-linux-amd64' 'go1.23.12' 'GOOS=linux GOARCH=arm GOARM=7' 'GNSSAgent-HF' hf

for wrapper_target in ccu hf multiband-radio multiband-handheld; do
    case "$wrapper_target" in
        ccu) wrapper=Makefile_CCU ;;
        hf) wrapper=Makefile_HF ;;
        multiband-radio) wrapper=Makefile_MultibandRadio ;;
        multiband-handheld) wrapper=Makefile_MultibandHandheld ;;
    esac
    output=$(cd "$outside_dir" && make -n -f "$make_dir/$wrapper")
    require_contains "$output" " $wrapper_target" "$wrapper wrapper"
    require_contains "$output" "gnssagent/internal/buildinfo.Target=$wrapper_target" "$wrapper wrapper"
done

make_archive() {
    archive=$1
    version=$2
    stage="$tmp_dir/stage-$version"
    mkdir -p "$stage/go/bin" "$stage/go/src/errors" "$stage/go/src/runtime"
    printf '%s\n' '#!/bin/sh' \
        'case "$1" in' \
        "version) printf '%s\\n' 'go version $version linux/amd64' ;;" \
        'test) sleep "${FAKE_TEST_SLEEP:-0}"; [ "${FAKE_TEST_FAIL:-0}" = 0 ] || exit 1 ;;' \
        'build) printf build\\n >> "${FAKE_EVENT_LOG:?}" ;;' \
        '*) exit 0 ;;' \
        'esac' > "$stage/go/bin/go"
    chmod +x "$stage/go/bin/go"
    : > "$stage/go/src/errors/errors.go"
    : > "$stage/go/src/runtime/recovered.go"
    tar -czf "$archive" -C "$stage" go
}

compiler_dir="$project_dir/build/compiler"
mkdir -p "$compiler_dir"
archive126="$compiler_dir/fake-go126.tar.gz"
archive123="$compiler_dir/fake-go123.tar.gz"
make_archive "$archive126" go1.26.4
make_archive "$archive123" go1.23.12
sha126=$(sha256sum "$archive126" | awk '{print $1}')
sha123=$(sha256sum "$archive123" | awk '{print $1}')

run_make() {
    root126=$1 root123=$2
    shift 2
    make -f "$make_dir/Makefile" \
        GO126_ARCHIVE="$archive126" GO126_SHA256="$sha126" GO126_ROOT="$root126" \
        GO123_ARCHIVE="$archive123" GO123_SHA256="$sha123" GO123_ROOT="$root123" "$@"
}

if run_make "$tmp_dir/bad-sha" "$tmp_dir/unused123" GO126_SHA256=bad go126 >/dev/null 2>&1; then
    fail 'checksum mismatch was accepted'
fi

bad_version_archive="$compiler_dir/fake-bad-version.tar.gz"
make_archive "$bad_version_archive" go9.9.9
bad_version_sha=$(sha256sum "$bad_version_archive" | awk '{print $1}')
if make -f "$make_dir/Makefile" GO126_ARCHIVE="$bad_version_archive" GO126_SHA256="$bad_version_sha" GO126_ROOT="$tmp_dir/bad-version" go126 >/dev/null 2>&1; then
    fail 'wrong Go version was accepted'
fi

partial_root="$tmp_dir/partial"
mkdir -p "$partial_root/bin" "$partial_root/src/errors"
cp "$tmp_dir/stage-go1.26.4/go/bin/go" "$partial_root/bin/go"
cp "$tmp_dir/stage-go1.26.4/go/src/errors/errors.go" "$partial_root/src/errors/errors.go"
run_make "$partial_root" "$tmp_dir/unused123" go126
[ -f "$partial_root/src/runtime/recovered.go" ] || fail 'partial cache was not re-extracted'
[ -f "$partial_root/.complete" ] || fail 'toolchain completion stamp was not written'

stale_lock_root="$tmp_dir/stale-lock"
: > "$stale_lock_root.lock"
if ! timeout 2 make -f "$make_dir/Makefile" GO126_ARCHIVE="$archive126" GO126_SHA256="$sha126" GO126_ROOT="$stale_lock_root" go126 >/dev/null 2>&1; then
    fail 'an inert lock file blocked toolchain preparation'
fi

fake_bin="$tmp_dir/fake-bin"
tar_log="$tmp_dir/tar.log"
mkdir -p "$fake_bin"
printf '%s\n' '#!/bin/sh' 'printf tar\\n >> "${TAR_LOG:?}"' 'sleep 1' 'exec /usr/bin/tar "$@"' > "$fake_bin/tar"
chmod +x "$fake_bin/tar"
concurrent_root="$tmp_dir/concurrent"
PATH="$fake_bin:$PATH" TAR_LOG="$tar_log" run_make "$concurrent_root" "$tmp_dir/unused123-a" go126 &
first_pid=$!
PATH="$fake_bin:$PATH" TAR_LOG="$tar_log" run_make "$concurrent_root" "$tmp_dir/unused123-b" go126 &
second_pid=$!
wait "$first_pid"
wait "$second_pid"
[ "$(wc -l < "$tar_log" | tr -d ' ')" = 1 ] || fail 'concurrent preparation extracted more than once'
[ -f "$concurrent_root/.complete" ] || fail 'concurrent preparation did not complete'

event_log="$tmp_dir/events.log"
if FAKE_EVENT_LOG="$event_log" FAKE_TEST_SLEEP=1 FAKE_TEST_FAIL=1 run_make "$tmp_dir/barrier126" "$tmp_dir/barrier123" -j 2 all >/dev/null 2>&1; then
    fail 'all accepted a failing test'
fi
if [ -f "$event_log" ] && grep -q '^build$' "$event_log"; then
    fail 'all started builds before tests completed'
fi

space_project="$tmp_dir/project with spaces"
space_make_dir="$space_project/build/scripts/make"
mkdir -p "$space_make_dir"
for file in Makefile Makefile_CCU Makefile_HF Makefile_MultibandRadio Makefile_MultibandHandheld; do
    cp "$make_dir/$file" "$space_make_dir/$file"
    if make -n -f "$space_make_dir/$file" >/dev/null 2>"$tmp_dir/space-error"; then
        fail "$file accepted a whitespace-containing path"
    fi
    grep -q 'Makefile paths do not support whitespace' "$tmp_dir/space-error" || fail "$file did not explain whitespace rejection"
    if (cd "$space_project" && make -n -f "build/scripts/make/$file") >/dev/null 2>"$tmp_dir/space-error"; then
        fail "$file accepted a relative Makefile path from a whitespace-containing directory"
    fi
    grep -q 'Makefile paths do not support whitespace' "$tmp_dir/space-error" || fail "$file did not explain relative whitespace rejection"
done

grep -Fq '$$("$(GO126)" version)' "$make_dir/Makefile" || fail 'Go 1.26 version command is not quoted'
grep -Fq '$$("$(GO123)" version)' "$make_dir/Makefile" || fail 'Go 1.23 version command is not quoted'

printf '%s\n' 'GNSSAgent Make behavior passed.'
