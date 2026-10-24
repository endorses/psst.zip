.DEFAULT_GOAL := build

CLIENT_DIR ?= $(CURDIR)
GO ?= go
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
DESTDIR ?=
VERSION ?= $(shell sh "$(CLIENT_DIR)/scripts/source-version.sh" "$(CLIENT_DIR)")
REVISION ?= $(shell git -C "$(CLIENT_DIR)" rev-parse HEAD 2>/dev/null || printf '%s' unknown)

.PHONY: build build-release install install-release uninstall clean help lan-assets

lan-assets:
	@tool=$$(GO="$(GO)" sh "$(CLIENT_DIR)/../tools/release/build.sh"); \
		"$$tool" --root "$(CLIENT_DIR)/.." assets

build build-release: lan-assets
	CGO_ENABLED=0 $(GO) -C "$(CLIENT_DIR)" build -trimpath -buildvcs=false \
		-ldflags "$(if $(filter build-release,$@),-s -w )-X main.version=$(VERSION) -X main.revision=$(REVISION)" \
		-o "$(CLIENT_DIR)/psst" ./cmd/psst

install: build

install-release: build-release

install install-release:
	install -d "$(DESTDIR)$(BINDIR)"
	install -m 0755 "$(CLIENT_DIR)/psst" "$(DESTDIR)$(BINDIR)/psst"

uninstall:
	rm -f "$(DESTDIR)$(BINDIR)/psst"

clean:
	rm -f "$(CLIENT_DIR)/psst"

help:
	@printf '%s\n' 'psst.zip CLI — build and install' '' 'Targets (from the repository root or client/):'
	@printf '  %-22s %s\n' \
		'make [build]' 'Build client/psst with the current Git revision' \
		'make build-release' 'Build a stripped executable (-s -w)' \
		'make lan-assets' 'Prepare matching embedded LAN browser assets (cached)' \
		'make install' 'Build and install psst (default: ~/.local/bin)' \
		'make install-release' 'Build and install the stripped executable' \
		'make uninstall' 'Remove the installed executable' \
		'make clean' 'Remove only the local executable' \
		'make help' 'Show these targets'
	@printf '\n%s\n' 'Overrides:'
	@printf '  %-22s %s\n' \
		'PREFIX=PATH' 'Installation prefix (default: ~/.local)' \
		'BINDIR=PATH' 'Binary directory (default: PREFIX/bin)' \
		'DESTDIR=PATH' 'Optional installation staging root' \
		'GO=COMMAND' 'Go executable (default: go)' \
		'VERSION=TEXT' 'Override the version derived from client/v* tags' \
		'REVISION=TEXT' 'Embedded revision (default: current Git HEAD)'
