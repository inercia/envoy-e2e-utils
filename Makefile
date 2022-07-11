PLATFORMS   := linux/amd64 windows/amd64 darwin/amd64

VERSION      = $(shell git describe HEAD --tags --abbrev=0)
GIT_COMMIT   = $(shell git rev-parse HEAD)
LD_FLAGS     = -ldflags="-X 'github.com/inercia/kubetnl/pkg/version.gitCommit=$(GIT_COMMIT)'"

SRCS         = $(shell find . -name '*.go' ! -path './tests/*')
ALL_SRCS     = $(shell find . -name '*.go')
SHS          = $(shell find . -name '*.sh')

GO          ?= go
GOOS        ?= $(shell go env GOOS)
GOARCH      ?= $(shell go env GOARCH)
GOEXE       ?= kubetnl
GOFLAGS     ?=

SHFMT_ARGS   = -s -ln bash

############################################################################################

.DEFAULT_GOAL:=help

.PHONY: help
help: ## Show this help screen
	@echo 'Usage: make <OPTIONS> ... <TARGETS>'
	@echo ''
	@echo 'Available targets are:'
	@echo ''
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

#########################################
##@ Code fomatting
#########################################

format-go: ## Format the Go source code
	@echo ">>> Formatting the Go source code..."
	GOFLAGS="$(GOFLAGS)" $(GO) fmt `$(GO) list ./...`

format-sh:  ## Format the Shell source code
	@echo ">>> Formatting the Shell source code..."
	echo "$(SHS)" | xargs shfmt $(SHFMT_ARGS) -w 

format-all: format-go format-sh
format: format-all ## Format all the code
fmt: format

#########################################
##@ Tests
#########################################

PHONY: tests
tests: $(ALL_SRCS) ## Run all the tests
	go test ./tests -test.v
