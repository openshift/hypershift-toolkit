package pki

import (
	"fmt"
	"net"

	"github.com/openshift/hypershift-toolkit/pkg/api"
)

func GeneratePKI(params *api.ClusterParams, outputDir string) error {
	cas := []caSpec{
		ca("root-ca", "root-ca", "openshift"),
		ca("cluster-signer", "cluster-signer", "openshift"),
		ca("openvpn-ca", "openvpn-ca", "openshift"),
	}

	externalAPIServerAddress := fmt.Sprintf("https://%s:%d", params.ExternalAPIDNSName, params.ExternalAPIPort)
	kubeconfigs := []kubeconfigSpec{
		kubeconfig("admin", externalAPIServerAddress, "root-ca", "system:admin", "system:masters"),
		kubeconfig("internal-admin", "https://kube-apiserver:6443", "root-ca", "system:admin", "system:masters"),
		kubeconfig("kubelet-bootstrap", externalAPIServerAddress, "cluster-signer", "system:bootstrapper", "system:bootstrappers"),
	}

	_, serviceIPNet, err := net.ParseCIDR(params.ServiceCIDR)
	if err != nil {
		return err
	}
	kubeIP := firstIP(serviceIPNet)
	certs := []certSpec{
		// kube-apiserver
		cert("kube-apiserver-server", "root-ca", "kubernetes", "kubernetes",
			[]string{
				"kubernetes",
				"kubernetes.default.svc",
				"kubernetes.default.svc.cluster.local",
				"kube-apiserver",
				fmt.Sprintf("kube-apiserver.%s.svc", params.Namespace),
				fmt.Sprintf("kube-apiserver.%s.svc.cluster.local", params.Namespace),
			},
			[]string{
				kubeIP.String(),
				params.ExternalAPIIPAddress,
			}),
		cert("kube-apiserver-kubelet", "root-ca", "system:kube-apiserver", "kubernetes", nil, nil),
		cert("kube-apiserver-aggregator-proxy-client", "root-ca", "system:openshift-aggregator", "kubernetes", nil, nil),

		// etcd
		cert("etcd-client", "root-ca", "etcd-client", "kubernetes", nil, nil),
		cert("etcd-server", "root-ca", "etcd-server", "kubernetes",
			[]string{
				fmt.Sprintf("*.etcd.%s.svc", params.Namespace),
				fmt.Sprintf("etcd-client.%s.svc", params.Namespace),
				"etcd",
				"etcd-client",
				"localhost",
			}, nil),
		cert("etcd-server", "root-ca", "etcd-server", "kubernetes",
			[]string{
				fmt.Sprintf("*.etcd.%s.svc", params.Namespace),
				fmt.Sprintf("*.etcd.%s.svc.cluster.local", params.Namespace),
			}, nil),

		// openshift-apiserver
		cert("openshift-apiserver-server", "root-ca", "openshift-apiserver", "openshift",
			[]string{
				"openshift-apiserver",
				fmt.Sprintf("openshift-apiserver.%s.svc", params.Namespace),
				fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", params.Namespace),
				"openshift-apiserver.default.svc",
				"openshift-apiserver.default.svc.cluster.local",
			}, nil),

		// openshift-controller-manager
		cert("openshift-controller-manager-server", "root-ca", "openshift-controller-manager", "openshift",
			[]string{
				"openshift-controller-manager",
				fmt.Sprintf("openshift-controller-manager.%s.svc", params.Namespace),
				fmt.Sprintf("openshift-controller-manager.%s.svc.cluster.local", params.Namespace),
			}, nil),

		// openvpn
		cert("openvpn-server", "openvpn-ca", "server", "kubernetes",
			[]string{
				"openvpn-server",
				fmt.Sprintf("openvpn-server.%s.svc", params.Namespace),
				fmt.Sprintf("%s:%d", params.ExternalOpenVPNDNSName, params.ExternalOpenVPNPort),
			}, nil),
		cert("openvpn-kube-apiserver-client", "openvpn-ca", "kube-apiserver", "kubernetes", nil, nil),
		cert("openvpn-worker-client", "openvpn-ca", "kube-apiserver", "kubernetes", nil, nil),
	}
	caMap, err := generateCAs(cas)
	if err != nil {
		return err
	}
	kubeconfigMap, err := generateKubeconfigs(kubeconfigs, caMap)
	if err != nil {
		return err
	}
	certMap, err := generateCerts(certs, caMap)
	if err != nil {
		return err
	}

	if err := writeCAs(caMap, outputDir); err != nil {
		return err
	}
	if err := writeKubeconfigs(kubeconfigMap, outputDir); err != nil {
		return err
	}
	if err := writeCerts(certMap, outputDir); err != nil {
		return err
	}

	// Miscellaneous PKI artifacts
	if err := writeCombinedCA([]string{"root-ca", "cluster-signer"}, caMap, outputDir, "combined-ca"); err != nil {
		return err
	}
	if err := writeRSAKey(outputDir, "service-account-key"); err != nil {
		return err
	}
	if err := writeDHParams(outputDir, "openvpn-dh"); err != nil {
		return err
	}
	return nil
}

/*
# generate CAs
generate_ca "root-ca"
generate_ca "cluster-signer"

# admin kubeconfig           CA       Name       USER           ORG          HOSTNAME   SERVERADDRESS
generate_client_kubeconfig "root-ca" "admin" "system:admin" "system:masters" "" "${EXTERNAL_API_DNS_NAME}:${EXTERNAL_API_PORT}"

# kubelet bootstrapper kubeconfig
generate_client_kubeconfig "cluster-signer" "kubelet-bootstrap" "system:bootstrapper" "system:bootstrappers" "" "${EXTERNAL_API_DNS_NAME}:${EXTERNAL_API_PORT}"

# service client admin kubeconfig
generate_client_kubeconfig "root-ca" "service-admin" "system:admin" "system:masters" "kube-apiserver"

# kube-controller-manager
generate_client_kubeconfig "root-ca" "kube-controller-manager" "system:admin" "system:masters" "kube-apiserver"
if [ ! -e "service-account-key.pem" ]; then
  openssl genrsa -out service-account-key.pem 2048
  openssl rsa -in service-account-key.pem -pubout > service-account.pem
fi

# kube-scheduler
generate_client_kubeconfig "root-ca" "kube-scheduler" "system:admin" "system:masters"

# kube-apiserver            CA       NAME                    USER           ORG           HOSTS
generate_client_key_cert "root-ca" "kube-apiserver-server" "kubernetes" "kubernetes" "${EXTERNAL_API_DNS_NAME},172.31.0.1,${EXTERNAL_API_IP_ADDRESS},kubernetes,kubernetes.default.svc,kubernetes.default.svc.cluster.local,kube-apiserver,kube-apiserver.${NAMESPACE}.svc,kube-apiserver.${NAMESPACE}.svc.cluster.local"
generate_client_key_cert "root-ca" "kube-apiserver-kubelet" "system:kube-apiserver" "kubernetes"
generate_client_key_cert "root-ca" "kube-apiserver-aggregator-proxy-client" "system:openshift-aggregator" "kubernetes"

# etcd
generate_client_key_cert "root-ca" "etcd-client" "etcd-client" "kubernetes"
generate_client_key_cert "root-ca" "etcd-server" "etcd-server" "kubernetes" "
*.etcd.${NAMESPACE}.svc,
etcd-client.${NAMESPACE}.svc,
etcd,
etcd-client,
localhost"
generate_client_key_cert "root-ca" "etcd-peer" "etcd-peer" "kubernetes" "

*.etcd.${NAMESPACE}.svc,
*.etcd.${NAMESPACE}.svc.cluster.local"

# openshift-apiserver
generate_client_key_cert "root-ca" "openshift-apiserver-server" "openshift-apiserver" "openshift"
"openshift-apiserver,
openshift-apiserver.${NAMESPACE}.svc,
openshift-controller-manager.${NAMESPACE}.svc.cluster.local,
openshift-apiserver.default.svc,
openshift-apiserver.default.svc.cluster.local"

# openshift-controller-manager
generate_client_key_cert "root-ca" "openshift-controller-manager-server" "openshift-controller-manager" "openshift"
"openshift-controller-manager",
"openshift-controller-manager.${NAMESPACE}.svc",
"openshift-controller-manager.${NAMESPACE}.svc.cluster.local",

cat root-ca.pem cluster-signer.pem > combined-ca.pem

rm -f *.csr

# openvpn assets
generate_ca "openvpn-ca"
generate_client_key_cert "openvpn-ca" "openvpn-server" "server" "kubernetes"

"openvpn-server,
openvpn-server.${NAMESPACE}.svc,
${EXTERNAL_API_DNS_NAME}:${OPENVPN_NODEPORT}"

generate_client_key_cert "openvpn-ca" "openvpn-kube-apiserver-client" "kube-apiserver" "kubernetes"
generate_client_key_cert "openvpn-ca" "openvpn-worker-client" "worker" "kubernetes"
if [ ! -e "openvpn-dh.pem" ]; then
  # this might be slow, lots of entropy required
  openssl dhparam -out openvpn-dh.pem 2048
fi
*/
