BIN = bin

# The environment's GOBIN, captured with ":=" before the export below redirects
# GOBIN at ./bin. That redirect exists so the lint tools stay out of the user's
# path, and "make install" must not inherit it.
ENV_GOBIN := $(GOBIN)
GIT_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)

# Where "make install" puts gopatch, and what version it reports. goreleaser
# injects the tag it is building; a local install describes the checkout, so an
# installed dev build does not claim to be "unknown".
INSTALL_BIN ?= $(if $(ENV_GOBIN),$(ENV_GOBIN),$(shell go env GOPATH)/bin)
VERSION ?= $(GIT_VERSION)
HACK_MODULES := $(patsubst %/go.mod,%,$(wildcard hack/*/go.mod))

export GO111MODULE=on
export GOBIN ?= $(shell pwd)/$(BIN)

GO_FILES = $(shell find . \
	   -path '*/.*' -prune -o \
	   '(' -type f -a -name '*.go' ')' -print)

GOLINT = $(BIN)/golint
STATICCHECK = $(BIN)/staticcheck
EXTRACT_CHANGELOG = $(BIN)/extract-changelog
TOOLS = $(GOLINT) $(STATICCHECK) $(EXTRACT_CHANGELOG)

.PHONY: all
all: build lint test

.PHONY: build
build:
	go build ./...

.PHONY: binary build-hack test-hack lint-hack
binary:
	go build -trimpath -ldflags '-s -w -X main._version=$(VERSION)' -o $(BIN)/gopatch .

build-hack: $(addprefix build-,$(HACK_MODULES))
test-hack: $(addprefix test-,$(HACK_MODULES))
lint-hack: $(addprefix lint-,$(HACK_MODULES))

.PHONY: $(addprefix build-,$(HACK_MODULES)) $(addprefix test-,$(HACK_MODULES)) $(addprefix lint-,$(HACK_MODULES))
$(addprefix build-,$(HACK_MODULES)):
	GOWORK=off go -C $(@:build-%=%) build -o $(abspath $(BIN))/ ./...

$(addprefix test-,$(HACK_MODULES)):
	GOWORK=off go -C $(@:test-%=%) test -race ./...

$(addprefix lint-,$(HACK_MODULES)): $(GOLINT) $(STATICCHECK)
	$(MAKE) -C $(@:lint-%=%) -f $(abspath Makefile) GOWORK=off GOLINT=$(abspath $(GOLINT)) STATICCHECK=$(abspath $(STATICCHECK)) golint staticcheck

.PHONY: install
install:
	GOBIN=$(INSTALL_BIN) go install -ldflags '-X main._version=$(VERSION)' .
	@echo "installed $(INSTALL_BIN)/gopatch ($(VERSION))"

.PHONY: test
test:
	go test -v -race ./...

.PHONY: cover
cover:
	go test -race -coverprofile=cover.out -coverpkg=./... ./...
	go tool cover -html=cover.out -o cover.html

.PHONY: lint
lint: gofmt golint staticcheck

.PHONY: gofmt
gofmt:
	$(eval FMT_LOG := $(shell mktemp -t gofmt.XXXXX))
	@gofmt -e -s -l $(GO_FILES) > $(FMT_LOG) || true
	@[ ! -s "$(FMT_LOG)" ] || \
		(echo "gofmt failed. Please reformat the following files:" | \
		cat - $(FMT_LOG) && false)

.PHONY: golint
golint: $(GOLINT)
	$(GOLINT) ./...

.PHONY: staticcheck
staticcheck: $(STATICCHECK)
	$(STATICCHECK) ./...

.PHONY: tools
tools: $(GOLINT) $(STATICCHECK)

$(GOLINT): tools/go.mod
	cd tools && go install golang.org/x/lint/golint

$(STATICCHECK): tools/go.mod
	cd tools && go install honnef.co/go/tools/cmd/staticcheck

$(EXTRACT_CHANGELOG): tools/cmd/extract-changelog/main.go
	cd tools && go install github.com/uber-go/gopatch/tools/cmd/extract-changelog
