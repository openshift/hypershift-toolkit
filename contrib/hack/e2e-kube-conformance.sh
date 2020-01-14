#!/bin/bash
set -e

export CLUSTER_NAME=""
export ARTIFACT_DIR="${ARTIFACT_DIR:-/tmp/artifacts}"
cd $GOPATH/src/github.com/openshift/hypershift-toolkit
export HYPERSHIFT_DIR="$(pwd)"
export MANAGEMENT_KUBECONFIG="${KUBECONFIG}"

# Build the hypershift-aws installer
make hypershift-aws

function teardown() {
  if [[ -n "${CLUSTER_NAME}" ]]; then
    export KUBECONFIG="${MANAGEMENT_KUBECONFIG}"
    echo "Deleting hypershift cluster ${CLUSTER_NAME}"
    "${HYPERSHIFT_DIR}/bin/hypershift-aws" uninstall "${CLUSTER_NAME}"
  fi
}

trap 'teardown' EXIT

# Create a random cluster name (hs=hypershift)
export CLUSTER_NAME="hs-$(uuidgen | tr '[:upper:]' '[:lower:]' | cut -c1-5)"

# Run the hypershift AWS installer
./bin/hypershift-aws install "${CLUSTER_NAME}"

# Download recent oc client (needed by script running k8s conformance)
cd /tmp
mkdir -p client-tar
pushd client-tar
curl -O https://mirror.openshift.com/pub/openshift-v4/clients/ocp/4.2.13/openshift-client-linux-4.2.13.tar.gz
popd
mkdir -p client-bin
pushd client-bin
tar -xzf ../client-tar/openshift-client-linux-4.2.13.tar.gz
popd
export PATH="${PATH}:/tmp/client-bin"

# Extract secret to talk to hypershift cluster
oc get secret -n "${CLUSTER_NAME}" admin-kubeconfig -o jsonpath='{ .data.kubeconfig }' | base64 --decode > /tmp/cluster.kubeconfig
export KUBECONFIG=/tmp/cluster.kubeconfig

# Extract origin source and copy over our version of the k8s conformance test script
mkdir -p $GOPATH/src/github.com/openshift/origin
git clone https://github.com/openshift/origin.git $GOPATH/src/github.com/openshift/origin
cd $GOPATH/src/github.com/openshift/origin
cp "${HYPERSHIFT_DIR}/contrib/hack/conformance-k8s-hypershift.sh" ./test/extended

# Run the test script
./test/extended/conformance-k8s-hypershift.sh
