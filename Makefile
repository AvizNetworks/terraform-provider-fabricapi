TEST?=$$(go list ./... | grep -v 'vendor')

HOSTNAME?=registry.terraform.io
NAMESPACE?=local
NAME?=fabricapi

VERSION?=1.0.0
BINARY?=terraform-provider-$(NAME)_v$(VERSION)

GOOS?=$(shell command -v go >/dev/null 2>&1 && go env GOOS)
GOARCH?=$(shell command -v go >/dev/null 2>&1 && go env GOARCH)
OS_ARCH?=$(GOOS)_$(GOARCH)

WORKDIRECTORY?=examples

.PHONY: default build install uninstall uninstall-all init plan apply destroy test

default: install

build:
	@command -v go >/dev/null 2>&1 || (echo "Error: go is required but was not found in PATH." >&2; exit 1)
	go build -o $(BINARY) .

install: build
	@test -n "$(GOOS)" -a -n "$(GOARCH)" || (echo "Error: unable to determine GOOS/GOARCH (is Go installed and in PATH?)." >&2; exit 1)
	mkdir -p ~/.terraform.d/plugins/$(HOSTNAME)/$(NAMESPACE)/$(NAME)/$(VERSION)/$(OS_ARCH)
	mv $(BINARY) ~/.terraform.d/plugins/$(HOSTNAME)/$(NAMESPACE)/$(NAME)/$(VERSION)/$(OS_ARCH)/

uninstall:
	rm -rf $(WORKDIRECTORY)/.terraform*

uninstall-all:
	rm -rf $(WORKDIRECTORY)/.terraform*
	rm -rf $(WORKDIRECTORY)/*.tfstate*

init: install
	cd $(WORKDIRECTORY) && terraform init

plan: init
	cd $(WORKDIRECTORY) && terraform plan

apply: init
	cd $(WORKDIRECTORY) && terraform apply

destroy:
	cd $(WORKDIRECTORY) && terraform destroy -auto-approve

test:
	go test -i $(TEST) || exit 1
	echo $(TEST) | xargs -t -n4 go test $(TESTARGS) -timeout=30s -parallel=4
