# Get an array of CMD's we want to build. Looks for files in
# `cmd/*/main.go` and only grab the directory name with `patsubst`.
APPS := $(patsubst cmd/%/main.go,%,$(wildcard cmd/*/main.go))

# Base info for build, set values if not already set
GO         ?= go
GOHOSTOS   ?= $(shell $(GO) env GOHOSTOS)
GOHOSTARCH ?= $(shell $(GO) env GOHOSTARCH)
GOVERSION  ?= $(shell $(GO) env GOVERSION)

# Export ENV for `go install` and `go test`
# Override directory to save created binary into `bin/`
export BASEWD := $(PWD)
export GOBIN  := $(BASEWD)/bin

.PHONY: build
build:
	@$(GO) mod tidy
	@for app in $(APPS) ; do \
		cd $(BASEWD)/cmd/$$app/ && \
		$(GO) install -v -ldflags "-X main.BuildName=$$app \
			-X main.BuildUser=$(USER)@$(shell hostname) \
			-X main.BuildDate=$(shell date +%FT%T%Z) \
			-X main.BuildBranch=$(shell git rev-parse --abbrev-ref HEAD) \
			-X main.BuildCommit=$(shell git rev-parse HEAD) \
			-X main.BuildGo=$(GOVERSION) \
			-X main.BuildPlatform=$(GOHOSTOS)/$(GOHOSTARCH) \
		";\
	done

.PHONY: test
test:
	@for app in $(APPS) ; do \
		cd $(BASEWD)/cmd/$$app/ && \
		$(GO) test -v ;\
	done

.PHONY: clean
clean:
	@for app in $(APPS) ; do \
		rm -f $(BASEWD)/bin/$$app ;\
	done
