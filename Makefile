BUILD_DIR := build

GO_PACKAGE := github.com/toitlang/tpkg

TOITC_PATH ?= toit.compile
TOITLSP_PATH ?= toit.lsp
TOITVM_PATH ?= toit.run
TOITPKG_PATH ?= $(CURDIR)/$(BUILD_DIR)/toitpkg

all: toitpkg
.PHONY: all

ifeq ($(OS),Windows_NT)
        EXE_SUFFIX=.exe
else
        EXE_SUFFIX=
endif

.PHONY: go_dependencies
go_dependencies:
	go install github.com/jstroem/tedi/cmd/tedi

GO_BUILD_FLAGS ?=
ifeq ("$(GO_BUILD_FLAGS)", "")
$(eval GODEBUG=netdns=go)
else
$(eval GO_BUILD_FLAGS=$(GO_BUILD_FLAGS) GODEBUG=netdns=go)
endif

.PHONY: $(BUILD_DIR)/toitpkg
$(BUILD_DIR)/toitpkg:
	# Use `cmake` to have a platform independent way of setting env variables.
	cmake -E env $(GO_BUILD_FLAGS) go build -ldflags "$(GO_LINK_FLAGS)" -tags 'netgo osusergo' -o $(BUILD_DIR)/toitpkg$(EXE_SUFFIX) ./cmd/toitpkg

.PHONY: toitpkg
toitpkg: $(BUILD_DIR)/toitpkg

TEST_FLAGS ?=
.PHONY: test
test: toitpkg $(GO_MOCKS)
	cmake -E env \
	    TOITC_PATH=$(TOITC_PATH) \
	    TOITLSP_PATH=$(TOITLSP_PATH) \
	    TOITVM_PATH=$(TOITVM_PATH)  \
	    TOITPKG_PATH=$(TOITPKG_PATH) \
	    tedi test -v -cover $(TEST_FLAGS) ./...

.PHONY: update-gold
update-gold: export UPDATE_PKG_GOLD = true
update-gold:
	$(MAKE) test

.PHONY: clean
clean:
	rm -rf ./$(BUILD_DIR)
