package pki

import (
	"fmt"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"net"

	"github.com/openshift/hypershift-toolkit/pkg/api"
)

func GeneratePKI(params *api.ClusterParams, outputDir string) error {
	log.Info("Generating PKI artifacts")

	cas := []caSpec{
		ca("root-ca", "root-ca", "openshift"),
		ca("cluster-signer", "cluster-signer", "openshift"),
		ca("openvpn-ca", "openvpn-ca", "openshift"),
	}

	externalAPIServerAddress := fmt.Sprintf("https://%s:%d", params.ExternalAPIDNSName, params.ExternalAPIPort)
	internalAPIServerAddress := fmt.Sprintf("https://kube-apiserver:%d", params.InternalAPIPort)
	kubeconfigs := []kubeconfigSpec{
		kubeconfig("admin", externalAPIServerAddress, "root-ca", "system:admin", "system:masters"),
		kubeconfig("internal-admin", internalAPIServerAddress, "root-ca", "system:admin", "system:masters"),
		kubeconfig("kubelet-bootstrap", externalAPIServerAddress, "cluster-signer", "system:bootstrapper", "system:bootstrappers"),
	}

	_, serviceIPNet, err := net.ParseCIDR(params.ServiceCIDR)
	if err != nil {
		return errors.Wrapf(err, "failed to parse service CIDR: %q", params.ServiceCIDR)
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
		cert("etcd-peer", "root-ca", "etcd-peer", "kubernetes",
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
		// oauth server
		cert("oauth-openshift", "root-ca", params.ExternalAPIDNSName, "kubernetes",
			[]string{}, nil),
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
	if err := writeRSAKey(outputDir, "service-account"); err != nil {
		return err
	}
	if err := writeDHParams(outputDir, "openvpn-dh"); err != nil {
		return err
	}
	return nil
}
