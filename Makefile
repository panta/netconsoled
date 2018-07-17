GO_EXECUTABLE ?= go
REPOSITORY?=panta/netconsoled
TAG?=latest

DOCKER = docker
DOCKER_COMPOSE = docker-compose

VERSION ?= $(shell git describe --tags)
GIT_VERSION ?= $(shell git --no-pager describe --tags --always --dirty)
GIT_DATE ?= $(shell git --no-pager show --date=short --format="%ad" --name-only | head -n 1 | awk '{print $1;}')
PROJECT_TAG ?= $(shell git describe --abbrev=0 --tags)
BUILD_DATE ?= $(shell date "+%Y%m%d-%H%M")
BUILD_HOST ?= $(shell hostname)

OK_COLOR=\033[32;01m
NO_COLOR=\033[0m

.PHONY: all
all: build push

.PHONY: build-go
build-go:
	${GO_EXECUTABLE} build -o netconsoled -ldflags "-X main.version=${VERSION}" ./cmd/netconsoled

.PHONY: bootstrap-dist
bootstrap-dist:
	${GO_EXECUTABLE} get -u github.com/Masterminds/gox
	${GO_EXECUTABLE} get -u github.com/gobuffalo/packr/...

.PHONY: build-all
build-all:
	# -os="linux darwin windows freebsd openbsd netbsd"
	# -arch="amd64 386 armv5 armv6 armv7 arm64 s390x"
	gox -verbose \
		-ldflags "-X main.version=${VERSION}" \
		-os="linux darwin windows" \
		-arch="amd64 386" \
		-osarch="!darwin/386 !darwin/arm64" \
		-output="dist/{{.OS}}-{{.Arch}}/{{.Dir}}" .

.PHONY: build-win
build-win:
	# -os="linux darwin windows freebsd openbsd netbsd"
	# -arch="amd64 386 armv5 armv6 armv7 arm64 s390x"
	gox -verbose \
		-ldflags "-X main.version=${VERSION}" \
		-os="windows" \
		-arch="amd64 386" \
		-osarch="!darwin/arm64" \
		-output="dist/{{.OS}}-{{.Arch}}/{{.Dir}}" .

.PHONY: build
build:
	@echo "$(OK_COLOR)==>$(NO_COLOR) Building $(REPOSITORY):$(TAG)"
	@docker build --rm -f Dockerfile.netconsoled -t $(REPOSITORY):$(TAG) .

$(REPOSITORY)_$(TAG).tar: build
	@echo "$(OK_COLOR)==>$(NO_COLOR) Saving $(REPOSITORY):$(TAG) > $@"
	@docker save $(REPOSITORY):$(TAG) > $@

.PHONY: push
push: build
	@echo "$(OK_COLOR)==>$(NO_COLOR) Pushing $(REPOSITORY):$(TAG)"
	@docker push $(REPOSITORY):$(TAG)

.PHONY: up
up:
	@$(DOCKER_COMPOSE) -f docker-compose.yml build
	@$(DOCKER_COMPOSE) -f docker-compose.yml up

.PHONY: down
down:
	@$(DOCKER_COMPOSE) -f docker-compose.yml stop
	@$(DOCKER_COMPOSE) -f docker-compose.yml rm -f
