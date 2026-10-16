#!/bin/sh
BASE_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P) || exit 1
exec /bin/sh "$BASE_DIR/gnssagentctl.sh" start
