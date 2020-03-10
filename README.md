# Hypershift Toolkit

## Overview
The hypershift toolkit is a set of tools and files that enables running OpenShift 4.x in a hyperscale manner with many control planes hosted on a central management cluster. This tool was jointly developed by RedHat and IBM. 

## Getting Started

### Install on standalone environment

* Run `make build` to build the binary
* Construct a "cluster.yaml" to define custom parameters for the cluster. Example found here: [cluster.yaml.example](https://github.com/openshift/hypershift-toolkit/blob/master/cluster.yaml.example)
* Construct a "pull-secret.txt" to provide authentication to pull from desired docker registries. Example found here: [pull-secret.txt.example](https://github.com/openshift/hypershift-toolkit/blob/master/pull-secret.txt.example)
* Construct and run the render command, with optional fields below: `./bin/hypershift render`
    - `output-dir`: Specify the directory where manifest files should be output (default ./manifests)
    - `config`: Specify the config file for this cluster (default ./cluster.yaml)
    - `pull-secret`: Specify the pull secret used to pull from desired docker registries (default ./pull-secret.txt)
    - `pki-dir`: Specify the directory where the input PKI files have been placed (default ./pki)
    - `include-secrets`: If true, PKI secrets will be included in rendered manifests (default false)
    - `include-etcd`: If true, Etcd manifests will be included in rendered manifests (default false)
    - `include-autoapprover`: If true, includes a simple autoapprover pod in manifests (default false)
    - `include-vpn`: If true, includes a VPN server, sidecar and client (default false)
    - `include-registry`: If true, includes a default registry config to deploy into the user cluster (default false)
* Apply all the generated resources to the cluster `kubectl apply -f output-dir/`

### Installing on AWS

* Install an Openshift 4.x cluster on AWS using the traditional installer
* Run `make hypershift-aws` on this repository
* Setup your KUBECONFIG to point to the admin kubeconfig of your current AWS cluster
  (ie. `export KUBECONFIG=${INSTALL_DIR}/auth/kubeconfig`)
* Run `./bin/hypershift-aws install NAME` to install a new Hypershift cluster on your
  existing AWS cluster. The `NAME` parameter will be used to create a namespace
  on your existing cluster and place all control plane components in it. Infrastructure
  will be created on AWS to support your new cluster instance, including:
  - Network Load Balancers for API, Router, VPN
  - DNS entries for API, Router, VPN
  - Worker machine instances for your new cluster

### Uninstalling on AWS
* Setup your KUBECONFIG to point to the management cluster
* Run `./bin/hypershift-aws uninstall NAME` where NAME is the name you gave your
  cluster when installing.
