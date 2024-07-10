# Run me to verify that all tests pass and all binaries are buildable before pushing!
# If you do not, then Travis will be sad.

BUILD_TYPE   ?= build
DOCKER_TAG   ?= dev
EDGE         ?= edge


# Everything; this is the default behavior
LDFLAGS := -X main.Version=$(VERSION)
all: build test
build: format shieldd shield shield-agent shield-schema shield-crypt shield-report plugins

# go fmt ftw
format:
	go list ./... | grep -v vendor | xargs go fmt

# Running Tests
test: go-tests api-tests plugin-tests
plugin-tests: plugins
	go build ./plugin/mock
	./t/plugins
	@rm -f mock
go-tests: shield
	go list ./... | grep -v vendor/ | PATH=$$PWD:$$PWD/bin:$$PWD/test/bin:$$PATH xargs go test -race
api-tests: shieldd shield-schema shield-crypt shield-agent shield-report
	./t/api

# Running Tests for race conditions
race:
	ginkgo -race *

# Building Shield
shield: shieldd shield-agent shield-schema shield-crypt shield-report

shield-crypt:
	go $(BUILD_TYPE) -mod vendor ./cmd/shield-crypt
shieldd:
	go $(BUILD_TYPE) -mod vendor ./cmd/shieldd
shield-agent:
	go $(BUILD_TYPE) -mod vendor ./cmd/shield-agent
shield-schema:
	go $(BUILD_TYPE) -mod vendor ./cmd/shield-schema
shield-report:
	go $(BUILD_TYPE) -mod vendor ./cmd/shield-report

shield: cmd/shield/help.go
	go $(BUILD_TYPE) -mod vendor ./cmd/shield
help.all: cmd/shield/main.go
	grep case $< | grep '{''{{' | cut -d\" -f 2 | sort | xargs -n1 -I@ ./shield @ -h > $@

# Building Plugins
plugin: plugins
plugins:
	go $(BUILD_TYPE) -mod vendor ./plugin/dummy
	@for plugin in $$(cat plugins); do \
		echo building plugin $$plugin...; \
		go $(BUILD_TYPE) -mod vendor ./plugin/$$plugin; \
	done


demo: clean shield plugins
	./demo/build
	(cd demo && docker-compose up)

docs: docs/dev/API.md
	./bin/mkdocs --version latest --docroot /docs --output tmp/docs --style basic
	gow -r tmp/docs

docs/dev/API.md: docs/dev/API.yml
	perl ./docs/regen.pl <$+ >$@~
	mv $@~ $@

clean:
	rm -f shield shieldd shield-agent shield-schema shield-crypt shield-report
	rm -f $$(cat plugins) dummy


# Assemble the CLI help with some assistance from our friend, Perl
HELP := $(shell ls -1 cmd/shield/help/*)
cmd/shield/help.go: $(HELP) cmd/shield/help.pl
	./cmd/shield/help.pl $(HELP) > $@

fixmes: fixme
fixme:
	@grep -rn FIXME * | grep -v vendor/ | grep -v README.md | grep --color FIXME || echo "No FIXMES!  YAY!"

dev:
	./bin/testdev

# Deferred: Naming plugins individually, e.g. make plugin dummy

init:
	go get github.com/kardianos/govendor
	go install github.com/kardianos/govendor

save-deps:
	govendor add +external

ARTIFACTS := artifacts/shield-server-linux-amd64
LDFLAGS := -X main.Version=$(VERSION)
shipit: release
release:
	@echo "Checking that VERSION was defined in the calling environment"
	@test -n "$(VERSION)"

	@echo "OK.  VERSION=$(VERSION)"

	@echo "Compiling SHIELD Linux Server Distribution..."
	export GOOS=linux GOARCH=amd64; \
	for plugin in $$(cat plugins); do \
	              go build -mod vendor -ldflags="$(LDFLAGS)" -o "$(ARTIFACTS)/plugins/$$plugin"      ./plugin/$$plugin; \
	done; \
	              go build -mod vendor -ldflags="$(LDFLAGS)" -o "$(ARTIFACTS)/crypter/shield-crypt"  ./cmd/shield-crypt; \
	              go build -mod vendor -ldflags="$(LDFLAGS)" -o "$(ARTIFACTS)/agent/shield-agent"    ./cmd/shield-agent; \
	              go build -mod vendor -ldflags="$(LDFLAGS)" -o "$(ARTIFACTS)/agent/shield-report"   ./cmd/shield-report; \
	CGO_ENABLED=1 go build -mod vendor -ldflags="$(LDFLAGS)" -o "$(ARTIFACTS)/daemon/shield-schema"  ./cmd/shield-schema; \
	CGO_ENABLED=1 go build -mod vendor -ldflags="$(LDFLAGS)" -o "$(ARTIFACTS)/daemon/shieldd"        ./cmd/shieldd; \

	@echo "Compiling SHIELD CLI For Linux and macOS..."
	GOOS=linux  GOARCH=amd64 go build -mod vendor -ldflags="$(LDFLAGS)" -o artifacts/shield-linux-amd64  ./cmd/shield
	GOOS=darwin GOARCH=amd64 go build -mod vendor -ldflags="$(LDFLAGS)" -o artifacts/shield-darwin-amd64 ./cmd/shield
	GOOS=darwin GOARCH=arm64 go build -mod vendor -ldflags="$(LDFLAGS)" -o artifacts/shield-darwin-arm64 ./cmd/shield
	mkdir -p "$(ARTIFACTS)/cli"
	cp artifacts/shield-linux-amd64 "$(ARTIFACTS)/cli/shield"

	@echo "Assembling Linux Server Distribution..."
	rm -f artifacts/*.tar.gz
	cd artifacts && for x in shield-server-*; do \
	  cp -a ../web/htdocs $$x/webui; \
	  mkdir -p $$x/webui/cli/linux; cp ../artifacts/shield-linux-amd64   $$x/webui/cli/linux/shield; \
	  mkdir -p $$x/webui/cli/darwin/amd64;   cp ../artifacts/shield-darwin-amd64  $$x/webui/cli/darwin/amd64/shield; \
	  mkdir -p $$x/webui/cli/darwin/arm64; cp ../artifacts/shield-darwin-arm64 $$x/webui/cli/darwin/arm64/shield; \
	  cp ../bin/shield-pipe      $$x/daemon; \
	  cp ../bin/shield-recover   $$x/daemon; \
	  cp ../bin/shield-restarter $$x/daemon; \
	  tar -czvf $$x.tar.gz $$x; \
	  rm -r $$x; \
	done

docker: docker-shield docker-webdav docker-demo
docker-shield:
	docker build -t quay.io/shieldproject/shield:$(DOCKER_TAG) . --build-arg VERSION=$(DOCKER_TAG)
docker-webdav:
	docker build -t quay.io/shieldproject/webdav:$(DOCKER_TAG) docker/webdav
docker-demo:
	docker build -t quay.io/shieldproject/demo:$(DOCKER_TAG) docker/demo

docker-edge:
	@echo "Checking that VERSION was defined in the calling environment"
	@test -n "$(VERSION)"
	@echo "OK.  VERSION=$(VERSION)"
	
	docker build -t quay.io/shieldproject/shield:$(EDGE) . --build-arg VERSION=$(VERSION)
	docker push quay.io/shieldproject/shield:$(EDGE)

docker-release:
	@echo "Checking that VERSION was defined in the calling environment"
	@test -n "$(VERSION)"
	@echo "OK.  VERSION=$(VERSION)"
	
	docker build -t quay.io/shieldproject/shield:$(VERSION) . --build-arg VERSION=$(VERSION)
	docker build -t quay.io/shieldproject/webdav:$(VERSION) docker/webdav
	docker build -t quay.io/shieldproject/demo:$(VERSION) docker/demo
	docker run --rm quay.io/shieldproject/shield:$(VERSION) /shield/bin/shieldd --version
	
	for I in quay.io/shieldproject/shield quay.io/shieldproject/webdav quay.io/shieldproject/demo; do \
		docker tag $$I:$(VERSION) $$I:latest; \
		docker push $$I:latest; \
		for V in $(VERSION) $(shell echo "$(VERSION)" | sed -e 's/\.[^.]*$$//') $(shell echo "$(VERSION)" | sed -e 's/\..*$$//'); do \
			docker tag $$I:$(VERSION) $$I:$$V; \
			docker push $$I:$$V; \
		done \
	done


.PHONY: plugins dev shield shieldd shield-schema shield-agent shield-crypt shield-report demo docs
