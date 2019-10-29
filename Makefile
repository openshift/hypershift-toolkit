BINDIR = bin
SRC_DIRS = cmd pkg


.PHONY: default
default: build

.PHONY: build
build:  bindata
	go build -o bin/hypershift github.com/openshift/hypershift-toolkit/cmd/hypershift

.PHONY: bindata
bindata:
	hack/update-generated-bindata.sh

.PHONY: verify-bindata
verify-bindata:
	hack/verify-generated-bindata.sh
