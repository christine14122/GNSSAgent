SHELL := /bin/sh

ifneq ($(words $(MAKEFILE_LIST)),1)
$(error Makefile paths do not support whitespace)
endif
MAKEFILE_PATH := $(lastword $(MAKEFILE_LIST))
ifeq ($(filter /%,$(MAKEFILE_PATH)),)
ifneq ($(words $(CURDIR)),1)
$(error Makefile paths do not support whitespace)
endif
endif

MAKE_DIR := $(dir $(abspath $(MAKEFILE_PATH)))
PROJECT_ROOT := $(abspath $(MAKE_DIR)/../../..)
COMPILER_DIR := $(PROJECT_ROOT)/build/compiler
DIST_DIR := $(PROJECT_ROOT)/build/dist/bin
MAIN_PACKAGE := ./cmd/gnssagent
BUILD_FLAGS := -trimpath -buildvcs=false
LINK_FLAGS := -s -w

GO126_ARCHIVE := $(COMPILER_DIR)/go1.26.4.linux-amd64.tar.gz
GO126_SHA256 := 1153d3d50e0ac764b447adfe05c2bcf08e889d42a02e0fe0259bd47f6733ad7f
GO126_ROOT := $(COMPILER_DIR)/.go1.26.4-linux-amd64
GO126 := $(GO126_ROOT)/bin/go
GO126_SENTINEL := $(GO126_ROOT)/src/errors/errors.go
GO126_STAMP := $(GO126_ROOT)/.complete

GO123_ARCHIVE := $(COMPILER_DIR)/go1.23.12.linux-amd64.tar.gz
GO123_SHA256 := d3847fef834e9db11bf64e3fb34db9c04db14e068eeb064f49af747010454f90
GO123_ROOT := $(COMPILER_DIR)/.go1.23.12-linux-amd64
GO123 := $(GO123_ROOT)/bin/go
GO123_SENTINEL := $(GO123_ROOT)/src/errors/errors.go
GO123_STAMP := $(GO123_ROOT)/.complete

.DEFAULT_GOAL := all
.PHONY: all test build ccu hf multiband-radio multiband-handheld go126 go123 clean

all: test

	$(MAKE) --no-print-directory -f "$(MAKE_DIR)/Makefile" build

test: go126

	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" "$(GO126)" test ./...

build: ccu multiband-radio multiband-handheld hf

ccu: go126 | $(DIST_DIR)

	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "$(GO126)" build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=ccu" -o "$(DIST_DIR)/GNSSAgent-CCU" $(MAIN_PACKAGE)

multiband-radio: go126 | $(DIST_DIR)

	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0 "$(GO126)" build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=multiband-radio" -o "$(DIST_DIR)/GNSSAgent-MultibandRadio" $(MAIN_PACKAGE)

multiband-handheld: go126 | $(DIST_DIR)

	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO126_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 "$(GO126)" build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=multiband-handheld" -o "$(DIST_DIR)/GNSSAgent-MultibandHandheld" $(MAIN_PACKAGE)

hf: go123 | $(DIST_DIR)

	cd "$(PROJECT_ROOT)" && GOTOOLCHAIN=local GOROOT="$(GO123_ROOT)" CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 "$(GO123)" build $(BUILD_FLAGS) -ldflags "$(LINK_FLAGS) -X gnssagent/internal/buildinfo.Target=hf" -o "$(DIST_DIR)/GNSSAgent-HF" $(MAIN_PACKAGE)

$(DIST_DIR):

	mkdir -p "$(DIST_DIR)"

go126:

	test -f "$(GO126_ARCHIVE)"

	printf '%s  %s\n' "$(GO126_SHA256)" "$(GO126_ARCHIVE)" | sha256sum -c -

	@command -v flock >/dev/null 2>&1 || { echo "flock is required to prepare Go 1.26.4" >&2; exit 1; }; mkdir -p "$(GO126_ROOT)" || { echo "Unable to create Go 1.26.4 toolchain directory" >&2; exit 1; }; exec 9>"$(GO126_ROOT)/.prepare.lock" || { echo "Unable to open Go 1.26.4 toolchain lock" >&2; exit 1; }; flock 9 || { echo "Unable to acquire Go 1.26.4 toolchain lock" >&2; exit 1; }; if [ ! -f "$(GO126_STAMP)" ] || [ ! -x "$(GO126)" ] || [ ! -f "$(GO126_SENTINEL)" ]; then rm -f "$(GO126_STAMP)"; tar -xzf "$(GO126_ARCHIVE)" -C "$(GO126_ROOT)" --strip-components=1 || exit 1; fi; version="$$("$(GO126)" version)" || { echo "Unable to run Go 1.26.4 executable: $(GO126)" >&2; rm -f "$(GO126_STAMP)"; exit 1; }; if [ "$$version" != "go version go1.26.4 linux/amd64" ]; then echo "Expected go version go1.26.4 linux/amd64, got: $$version ($(GO126))" >&2; rm -f "$(GO126_STAMP)"; exit 1; fi; touch "$(GO126_STAMP)"

go123:

	test -f "$(GO123_ARCHIVE)"

	printf '%s  %s\n' "$(GO123_SHA256)" "$(GO123_ARCHIVE)" | sha256sum -c -

	@command -v flock >/dev/null 2>&1 || { echo "flock is required to prepare Go 1.23.12" >&2; exit 1; }; mkdir -p "$(GO123_ROOT)" || { echo "Unable to create Go 1.23.12 toolchain directory" >&2; exit 1; }; exec 9>"$(GO123_ROOT)/.prepare.lock" || { echo "Unable to open Go 1.23.12 toolchain lock" >&2; exit 1; }; flock 9 || { echo "Unable to acquire Go 1.23.12 toolchain lock" >&2; exit 1; }; if [ ! -f "$(GO123_STAMP)" ] || [ ! -x "$(GO123)" ] || [ ! -f "$(GO123_SENTINEL)" ]; then rm -f "$(GO123_STAMP)"; tar -xzf "$(GO123_ARCHIVE)" -C "$(GO123_ROOT)" --strip-components=1 || exit 1; fi; version="$$("$(GO123)" version)" || { echo "Unable to run Go 1.23.12 executable: $(GO123)" >&2; rm -f "$(GO123_STAMP)"; exit 1; }; if [ "$$version" != "go version go1.23.12 linux/amd64" ]; then echo "Expected go version go1.23.12 linux/amd64, got: $$version ($(GO123))" >&2; rm -f "$(GO123_STAMP)"; exit 1; fi; touch "$(GO123_STAMP)"

clean:

	rm -f "$(DIST_DIR)/GNSSAgent-CCU"

	rm -f "$(DIST_DIR)/GNSSAgent-MultibandRadio"

	rm -f "$(DIST_DIR)/GNSSAgent-MultibandHandheld"

	rm -f "$(DIST_DIR)/GNSSAgent-HF"
