BUILD_DIR := build

GO_PACKAGE := github.com/toitlang/tpkg

all: toitpkg
.PHONY: all

$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

.PHONY: go_dependencies
go_dependencies:
	go get -u github.com/jstroem/tedi/cmd/tedi

GO_SOURCES := $(shell find . -name '*.go' -not -name '*_mock.go' -not -path './tests/*')

GO_BUILD_FLAGS ?=
ifeq ("$(GO_BUILD_FLAGS)", "")
$(eval GO_BUILD_FLAGS=CGO_ENABLED=1 GODEBUG=netdns=go)
else
$(eval GO_BUILD_FLAGS=$(GO_BUILD_FLAGS) CGO_ENABLED=1 GODEBUG=netdns=go)
endif

$(BUILD_DIR)/toitpkg: $(GO_SOURCES)
	$(GO_BUILD_FLAGS) go build -ldflags "$(GO_LINK_FLAGS)" -tags 'netgo osusergo' -o $(BUILD_DIR)/toitpkg ./cmd/toitpkg

.PHONY: toitpkg
toitpkg: $(BUILD_DIR)/toitpkg

TEST_FLAGS ?=
.PHONY: test
test: toitpkg $(GO_MOCKS)
	tedi test -v -cover $(TEST_FLAGS) $(foreach dir,$(filter-out third_party/, $(sort $(dir $(wildcard */)))),./$(dir)...)

.PHONY: clean
clean:
	rm -rf ./$(BUILD_DIR)
