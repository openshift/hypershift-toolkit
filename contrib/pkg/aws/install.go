package aws

import (
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/ioutil"
	"math/big"
	"math/rand"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	gocidr "github.com/apparentlymart/go-cidr/cidr"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"

	"github.com/openshift/hypershift-toolkit/pkg/api"
	"github.com/openshift/hypershift-toolkit/pkg/ignition"
	"github.com/openshift/hypershift-toolkit/pkg/pki"
	"github.com/openshift/hypershift-toolkit/pkg/render"
)

const (
	routerNodePortHTTP    = 31080
	routerNodePortHTTPS   = 31443
	externalOauthPort     = 8443
	workerMachineSetCount = 3

	defaultControlPlaneOperatorImage = "registry.svc.ci.openshift.org/hypershift-toolkit/hypershift-4.4:control-plane-operator"
)

var (
	excludeManifests = []string{
		"kube-apiserver-service.yaml",
		"openshift-apiserver-service.yaml",
		"openvpn-server-service.yaml",
		"v4-0-config-system-branding.yaml",
		"oauth-server-service.yaml",
	}
	coreScheme = runtime.NewScheme()
	coreCodecs = serializer.NewCodecFactory(coreScheme)
)

func init() {
	if err := corev1.AddToScheme(coreScheme); err != nil {
		panic(err)
	}
}

func InstallCluster(name, releaseImage, dhParamsFile string, waitForReady bool) error {

	// First, ensure that we can access the host cluster
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("cannot access existing cluster; make sure a connection to host cluster is available: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("cannot obtain dynamic client: %v", err)
	}
	// Extract config information from management cluster
	sshKey, err := getSSHPublicKey(dynamicClient)
	if err != nil {
		return fmt.Errorf("failed to fetch an SSH public key from existing cluster: %v", err)
	}
	log.Debugf("The SSH public key is: %s", string(sshKey))

	client, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to obtain a kubernetes client from existing configuration: %v", err)
	}
	awsKey, awsSecretKey, err := getAWSCredentials(client)
	if err != nil {
		return fmt.Errorf("failed to obtain AWS credentials from host cluster: %v", err)
	}
	log.Debugf("AWS key: %s, secret: %s", awsKey, awsSecretKey)

	if releaseImage == "" {
		releaseImage, err = getReleaseImage(dynamicClient)
		if err != nil {
			return fmt.Errorf("failed to obtain release image from host cluster: %v", err)
		}
	}

	pullSecret, err := getPullSecret(client)
	if err != nil {
		return fmt.Errorf("failed to obtain a pull secret from cluster: %v", err)
	}
	log.Debugf("The pull secret is: %v", pullSecret)

	infraName, region, err := getInfrastructureInfo(dynamicClient)
	if err != nil {
		return fmt.Errorf("failed to obtain infrastructure info for cluster: %v", err)
	}
	log.Debugf("The management cluster infra name is: %s", infraName)
	log.Debugf("The management cluster AWS region is: %s", region)

	serviceCIDR, podCIDR, err := getNetworkInfo(dynamicClient)
	if err != nil {
		return fmt.Errorf("failed to obtain network info for cluster: %v", err)
	}

	dnsZoneID, parentDomain, err := getDNSZoneInfo(dynamicClient)
	if err != nil {
		return fmt.Errorf("failed to obtain public zone information: %v", err)
	}
	log.Debugf("Using public DNS Zone: %s and parent suffix: %s", dnsZoneID, parentDomain)

	machineNames, err := getMachineNames(dynamicClient)
	if err != nil {
		return fmt.Errorf("failed to fetch machine names for cluster: %v", err)
	}

	// Start creating resources on management cluster
	_, err = client.CoreV1().Namespaces().Get(name, metav1.GetOptions{})
	if err == nil {
		return fmt.Errorf("target namespace %s already exists on management cluster", name)
	}
	if !errors.IsNotFound(err) {
		return fmt.Errorf("unexpected error getting namespaces from management cluster: %v", err)
	}
	log.Infof("Creating namespace %s", name)
	ns := &corev1.Namespace{}
	ns.Name = name
	_, err = client.CoreV1().Namespaces().Create(ns)
	if err != nil {
		return fmt.Errorf("failed to create namespace %s: %v", name, err)
	}

	// Ensure that we can run privileged pods
	if err = ensurePrivilegedSCC(dynamicClient, name); err != nil {
		return fmt.Errorf("failed to ensure privileged SCC for the new namespace: %v", err)
	}

	// Create pull secret
	log.Infof("Creating pull secret")
	if err := createPullSecret(client, name, pullSecret); err != nil {
		return fmt.Errorf("failed to create pull secret: %v", err)
	}

	// Create Kube APIServer service
	log.Infof("Creating Kube API service")
	apiNodePort, err := createKubeAPIServerService(client, name)
	if err != nil {
		return fmt.Errorf("failed to create kube apiserver service: %v", err)
	}
	log.Infof("Created Kube API service with NodePort %d", apiNodePort)

	log.Infof("Creating VPN service")
	vpnNodePort, err := createVPNServerService(client, name)
	if err != nil {
		return fmt.Errorf("failed to create vpn server service: %v", err)
	}
	log.Infof("Created VPN service with NodePort %d", vpnNodePort)

	log.Infof("Creating Openshift API service")
	openshiftClusterIP, err := createOpenshiftService(client, name)
	if err != nil {
		return fmt.Errorf("failed to create openshift server service: %v", err)
	}
	log.Infof("Created Openshift API service with cluster IP: %s", openshiftClusterIP)

	oauthNodePort, err := createOauthService(client, name)
	if err != nil {
		return fmt.Errorf("failed to create Oauth server service: %v", err)
	}
	log.Infof("Created Oauth server service with NodePort: %d", oauthNodePort)

	// Fetch AWS cloud data
	aws, err := NewAWSHelper(awsKey, awsSecretKey, region, infraName)
	if err != nil {
		return fmt.Errorf("cannot create an AWS client: %v", err)
	}

	lbInfo, err := aws.LoadBalancerInfo(machineNames)
	if err != nil {
		return fmt.Errorf("cannot get load balancer info: %v", err)
	}
	log.Infof("Using VPC: %s, Zone: %s, Subnet: %s", lbInfo.VPC, lbInfo.Zone, lbInfo.Subnet)

	machineID, machineIP, err := getMachineInfo(dynamicClient, machineNames, fmt.Sprintf("%s-worker-%s", infraName, lbInfo.Zone))
	if err != nil {
		return fmt.Errorf("cannot get machine info: %v", err)
	}
	log.Infof("Using management machine with ID: %s and IP: %s", machineID, machineIP)

	apiLBName := generateLBResourceName(infraName, name, "api")
	apiAllocID, apiPublicIP, err := aws.EnsureEIP(apiLBName)
	if err != nil {
		return fmt.Errorf("cannot allocate API load balancer EIP: %v", err)
	}
	log.Infof("Allocated EIP with ID: %s, and IP: %s", apiAllocID, apiPublicIP)

	apiLBARN, apiLBDNS, err := aws.EnsureNLB(apiLBName, lbInfo.Subnet, apiAllocID)
	if err != nil {
		return fmt.Errorf("cannot create network load balancer: %v", err)
	}
	log.Infof("Created API load balancer with ARN: %s, DNS: %s", apiLBARN, apiLBDNS)

	apiTGARN, err := aws.EnsureTargetGroup(lbInfo.VPC, apiLBName, apiNodePort)
	if err != nil {
		return fmt.Errorf("cannot create API target group: %v", err)
	}
	log.Infof("Created API target group ARN: %s", apiTGARN)

	oauthTGName := generateLBResourceName(infraName, name, "oauth")
	oauthTGARN, err := aws.EnsureTargetGroup(lbInfo.VPC, oauthTGName, oauthNodePort)
	if err != nil {
		return fmt.Errorf("cannot create OAuth target group: %v", err)
	}

	if err = aws.EnsureTarget(apiTGARN, machineIP); err != nil {
		return fmt.Errorf("cannot create API load balancer target: %v", err)
	}
	log.Infof("Created API load balancer target to %s", machineIP)

	if err = aws.EnsureTarget(oauthTGARN, machineIP); err != nil {
		return fmt.Errorf("cannot create OAuth load balancer target: %v", err)
	}
	log.Infof("Created OAuth load balancer target to %s", machineIP)

	err = aws.EnsureListener(apiLBARN, apiTGARN, 6443, false)
	if err != nil {
		return fmt.Errorf("cannot create API listener: %v", err)
	}
	log.Infof("Created API load balancer listener")

	err = aws.EnsureListener(apiLBARN, oauthTGARN, externalOauthPort, false)
	if err != nil {
		return fmt.Errorf("cannot create OAuth listener: %v", err)
	}
	log.Infof("Created OAuth load balancer listener")

	apiDNSName := fmt.Sprintf("api.%s.%s", name, parentDomain)
	err = aws.EnsureCNameRecord(dnsZoneID, apiDNSName, apiLBDNS)
	if err != nil {
		return fmt.Errorf("cannot create API DNS record: %v", err)
	}
	log.Infof("Created DNS record for API name: %s", apiDNSName)

	routerLBName := generateLBResourceName(infraName, name, "apps")
	routerLBARN, routerLBDNS, err := aws.EnsureNLB(routerLBName, lbInfo.Subnet, "")
	if err != nil {
		return fmt.Errorf("cannot create router load balancer: %v", err)
	}
	log.Infof("Created router load balancer with ARN: %s, DNS: %s", routerLBARN, routerLBDNS)

	routerHTTPTGName := generateLBResourceName(infraName, name, "http")
	routerHTTPARN, err := aws.EnsureTargetGroup(lbInfo.VPC, routerHTTPTGName, routerNodePortHTTP)
	if err != nil {
		return fmt.Errorf("cannot create router HTTP target group: %v", err)
	}
	log.Infof("Created router HTTP target group ARN: %s", routerHTTPARN)

	err = aws.EnsureListener(routerLBARN, routerHTTPARN, 80, false)
	if err != nil {
		return fmt.Errorf("cannot create router HTTP listener: %v", err)
	}
	log.Infof("Created router HTTP load balancer listener")

	routerHTTPSTGName := generateLBResourceName(infraName, name, "https")
	routerHTTPSARN, err := aws.EnsureTargetGroup(lbInfo.VPC, routerHTTPSTGName, routerNodePortHTTPS)
	if err != nil {
		return fmt.Errorf("cannot create router HTTPS target group: %v", err)
	}
	log.Infof("Created router HTTPS target group ARN: %s", routerHTTPSARN)

	err = aws.EnsureListener(routerLBARN, routerHTTPSARN, 443, false)
	if err != nil {
		return fmt.Errorf("cannot create router HTTPS listener: %v", err)
	}
	log.Infof("Created router HTTPS load balancer listener")

	routerDNSName := fmt.Sprintf("*.apps.%s.%s", name, parentDomain)
	err = aws.EnsureCNameRecord(dnsZoneID, routerDNSName, routerLBDNS)
	if err != nil {
		return fmt.Errorf("cannot create router DNS record: %v", err)
	}
	log.Infof("Created DNS record for router name: %s", routerDNSName)

	vpnLBName := generateLBResourceName(infraName, name, "vpn")
	vpnLBARN, vpnLBDNS, err := aws.EnsureNLB(vpnLBName, lbInfo.Subnet, "")
	if err != nil {
		return fmt.Errorf("cannot create vpn load balancer: %v", err)
	}
	log.Infof("Created VPN load balancer with ARN: %s and DNS: %s", vpnLBARN, vpnLBDNS)

	vpnTGARN, err := aws.EnsureUDPTargetGroup(lbInfo.VPC, vpnLBName, vpnNodePort, apiNodePort)
	if err != nil {
		return fmt.Errorf("cannot create VPN target group: %v", err)
	}
	log.Infof("Created VPN target group ARN: %s", vpnTGARN)

	if err = aws.EnsureTarget(vpnTGARN, machineID); err != nil {
		return fmt.Errorf("cannot create VPN load balancer target: %v", err)
	}
	log.Infof("Created VPN load balancer target to %s", machineID)

	err = aws.EnsureListener(vpnLBARN, vpnTGARN, 1194, true)
	if err != nil {
		return fmt.Errorf("cannot create VPN listener: %v", err)
	}
	log.Infof("Created VPN load balancer listener")

	vpnDNSName := fmt.Sprintf("vpn.%s.%s", name, parentDomain)
	err = aws.EnsureCNameRecord(dnsZoneID, vpnDNSName, vpnLBDNS)
	if err != nil {
		return fmt.Errorf("cannot create router DNS record: %v", err)
	}
	log.Infof("Created DNS record for VPN: %s", vpnDNSName)

	err = aws.EnsureWorkersAllowNodePortAccess()
	if err != nil {
		return fmt.Errorf("cannot setup security group for worker nodes: %v", err)
	}
	log.Infof("Ensured that node ports on workers are accessible")

	_, serviceCIDRNet, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		return fmt.Errorf("cannot parse service CIDR %s: %v", serviceCIDR, err)
	}

	_, podCIDRNet, err := net.ParseCIDR(podCIDR)
	if err != nil {
		return fmt.Errorf("cannot parse pod CIDR %s: %v", podCIDR, err)
	}

	serviceCIDRPrefixLen, _ := serviceCIDRNet.Mask.Size()
	clusterServiceCIDR, exceedsMax := gocidr.NextSubnet(serviceCIDRNet, serviceCIDRPrefixLen)
	if exceedsMax {
		return fmt.Errorf("cluster service CIDR exceeds max address space")
	}

	podCIDRPrefixLen, _ := podCIDRNet.Mask.Size()
	clusterPodCIDR, exceedsMax := gocidr.NextSubnet(podCIDRNet, podCIDRPrefixLen)
	if exceedsMax {
		return fmt.Errorf("cluster pod CIDR exceeds max address space")
	}

	params := api.NewClusterParams()
	params.Namespace = name
	params.ExternalAPIDNSName = apiDNSName
	params.ExternalAPIPort = 6443
	params.ExternalAPIIPAddress = apiPublicIP
	params.ExternalOpenVPNDNSName = vpnDNSName
	params.ExternalOpenVPNPort = 1194
	params.ExternalOauthPort = externalOauthPort
	params.APINodePort = uint(apiNodePort)
	params.ServiceCIDR = clusterServiceCIDR.String()
	params.PodCIDR = clusterPodCIDR.String()
	params.ReleaseImage = releaseImage
	params.IngressSubdomain = fmt.Sprintf("apps.%s.%s", name, parentDomain)
	params.OpenShiftAPIClusterIP = openshiftClusterIP
	params.OpenVPNNodePort = fmt.Sprintf("%d", vpnNodePort)
	params.BaseDomain = fmt.Sprintf("%s.%s", name, parentDomain)
	params.CloudProvider = "AWS"
	params.InternalAPIPort = 6443
	params.EtcdClientName = "etcd-client"
	params.NetworkType = "OpenShiftSDN"
	params.ImageRegistryHTTPSecret = generateImageRegistrySecret()
	params.RouterNodePortHTTP = fmt.Sprintf("%d", routerNodePortHTTP)
	params.RouterNodePortHTTPS = fmt.Sprintf("%d", routerNodePortHTTPS)
	params.RouterServiceType = "NodePort"
	params.Replicas = "1"
	params.ControlPlaneOperatorControllers = []string{
		"controller-manager-ca",
		"auto-approver",
		"kubeadmin-password",
		"cluster-operator",
		"cluster-version",
		"kubelet-serving-ca",
		"openshift-apiserver",
		"openshift-controller-manager",
	}
	cpOperatorImage := os.Getenv("CONTROL_PLANE_OPERATOR_IMAGE_OVERRIDE")
	if cpOperatorImage == "" {
		params.ControlPlaneOperatorImage = defaultControlPlaneOperatorImage
	} else {
		params.ControlPlaneOperatorImage = cpOperatorImage
	}

	workingDir, err := ioutil.TempDir("", "")
	if err != nil {
		return err
	}
	log.Infof("The working directory is %s", workingDir)
	pkiDir := filepath.Join(workingDir, "pki")
	if err = os.Mkdir(pkiDir, 0755); err != nil {
		return fmt.Errorf("cannot create temporary PKI directory: %v", err)
	}
	log.Info("Generating PKI")
	if len(dhParamsFile) > 0 {
		if err = copyFile(dhParamsFile, filepath.Join(pkiDir, "openvpn-dh.pem")); err != nil {
			return fmt.Errorf("cannot copy dh parameters file %s: %v", dhParamsFile, err)
		}
	}
	if err := pki.GeneratePKI(params, pkiDir); err != nil {
		return fmt.Errorf("failed to generate PKI assets: %v", err)
	}
	manifestsDir := filepath.Join(workingDir, "manifests")
	if err = os.Mkdir(manifestsDir, 0755); err != nil {
		return fmt.Errorf("cannot create temporary manifests directory: %v", err)
	}
	pullSecretFile := filepath.Join(workingDir, "pull-secret")
	if err = ioutil.WriteFile(pullSecretFile, []byte(pullSecret), 0644); err != nil {
		return fmt.Errorf("failed to create temporary pull secret file: %v", err)
	}
	log.Info("Generating ignition for workers")
	if err = ignition.GenerateIgnition(params, sshKey, pullSecretFile, pkiDir, workingDir); err != nil {
		return fmt.Errorf("cannot generate ignition file for workers: %v", err)
	}
	// Ensure that S3 bucket with ignition file in it exists
	bucketName := generateBucketName(infraName, name, "ign")
	log.Infof("Ensuring ignition bucket exists")
	if err = aws.EnsureIgnitionBucket(bucketName, filepath.Join(workingDir, "bootstrap.ign")); err != nil {
		return fmt.Errorf("failed to ensure ignition bucket exists: %v", err)
	}

	log.Info("Rendering Manifests")
	render.RenderPKISecrets(pkiDir, manifestsDir, true, true, true)
	caBytes, err := ioutil.ReadFile(filepath.Join(pkiDir, "combined-ca.crt"))
	if err != nil {
		return fmt.Errorf("failed to render PKI secrets: %v", err)
	}
	params.OpenshiftAPIServerCABundle = base64.StdEncoding.EncodeToString(caBytes)
	if err = render.RenderClusterManifests(params, pullSecretFile, manifestsDir, true, true, true, true); err != nil {
		return fmt.Errorf("failed to render manifests for cluster: %v", err)
	}

	// Create a nodeport service for the router
	if err = generateRouterService(filepath.Join(manifestsDir, "router-service.json")); err != nil {
		return fmt.Errorf("failed to generate router service: %v", err)
	}

	// Create a machineset for the new cluster's worker nodes
	if err = generateWorkerMachineset(dynamicClient, infraName, lbInfo.Zone, name, routerLBName, filepath.Join(manifestsDir, "machineset.json")); err != nil {
		return fmt.Errorf("failed to generate worker machineset: %v", err)
	}
	if err = generateUserDataSecret(name, bucketName, filepath.Join(manifestsDir, "machine-user-data.json")); err != nil {
		return fmt.Errorf("failed to generate user data secret: %v", err)
	}
	kubeadminPassword, err := generateKubeadminPassword()
	if err != nil {
		return fmt.Errorf("failed to generate kubeadmin password: %v", err)
	}
	if err = generateKubeadminPasswordTargetSecret(kubeadminPassword, filepath.Join(manifestsDir, "kubeadmin-secret.json")); err != nil {
		return fmt.Errorf("failed to create kubeadmin secret manifest for target cluster: %v", err)
	}
	if err = generateKubeadminPasswordSecret(kubeadminPassword, filepath.Join(manifestsDir, "kubeadmin-host-secret.json")); err != nil {
		return fmt.Errorf("failed to create kubeadmin secret manifest for management cluster: %v", err)
	}
	if err = generateKubeconfigSecret(filepath.Join(pkiDir, "admin.kubeconfig"), filepath.Join(manifestsDir, "kubeconfig-secret.json")); err != nil {
		return fmt.Errorf("failed to create kubeconfig secret manifest for management cluster: %v", err)
	}
	if err = generateTargetPullSecret([]byte(pullSecret), filepath.Join(manifestsDir, "user-pull-secret.json")); err != nil {
		return fmt.Errorf("failed to create pull secret manifest for target cluster: %v", err)
	}

	// Create the system branding manifest (cannot be applied because it's too large)
	if err = createBrandingSecret(client, name, filepath.Join(manifestsDir, "v4-0-config-system-branding.yaml")); err != nil {
		return fmt.Errorf("failed to create oauth branding secret: %v", err)
	}

	excludedDir, err := ioutil.TempDir("", "")
	if err != nil {
		return fmt.Errorf("failed to create a temporary directory for excluded manifests")
	}
	log.Infof("Excluded manifests directory: %s", excludedDir)
	if err = applyManifests(cfg, name, manifestsDir, excludeManifests, excludedDir); err != nil {
		return fmt.Errorf("failed to apply manifests: %v", err)
	}
	log.Infof("Cluster resources applied")

	if waitForReady {
		log.Infof("Waiting up to 10 minutes for API endpoint to be available.")
		if err = waitForAPIEndpoint(pkiDir, apiDNSName); err != nil {
			return fmt.Errorf("failed to access API endpoint: %v", err)
		}
		log.Infof("API is available at %s", fmt.Sprintf("https://%s:6443", apiDNSName))

		log.Infof("Waiting up to 5 minutes for bootstrap pod to complete.")
		if err = waitForBootstrapPod(client, name); err != nil {
			return fmt.Errorf("failed to wait for bootstrap pod to complete: %v", err)
		}
		log.Infof("Bootstrap pod has completed.")

		targetClusterCfg, err := getTargetClusterConfig(pkiDir)
		if err != nil {
			return fmt.Errorf("cannot create target cluster client config: %v", err)
		}
		targetClient, err := kubeclient.NewForConfig(targetClusterCfg)
		if err != nil {
			return fmt.Errorf("cannot create target cluster client: %v", err)
		}

		log.Infof("Waiting up to 10 minutes for nodes to be ready.")
		if err = waitForNodesReady(targetClient, workerMachineSetCount); err != nil {
			return fmt.Errorf("failed to wait for nodes ready: %v", err)
		}
		log.Infof("Nodes (%d) are ready", workerMachineSetCount)

		log.Infof("Waiting up to 15 minutes for cluster operators to be ready.")
		if err = waitForClusterOperators(targetClusterCfg); err != nil {
			return fmt.Errorf("failed to wait for cluster operators: %v", err)
		}
	}

	log.Infof("Cluster API URL: %s", fmt.Sprintf("https://%s:6443", apiDNSName))
	log.Infof("Kubeconfig is available in secret %q in the %s namespace", "admin-kubeconfig", name)
	log.Infof("Console URL:  %s", fmt.Sprintf("https://console-openshift-console.%s", params.IngressSubdomain))
	log.Infof("kubeadmin password is available in secret %q in the %s namespace", "kubeadmin-password", name)
	return nil
}

func applyManifests(cfg *rest.Config, namespace, directory string, exclude []string, excludedDir string) error {
	for _, f := range exclude {
		name := filepath.Join(directory, f)
		targetName := filepath.Join(excludedDir, f)
		if err := os.Rename(name, targetName); err != nil {
			return fmt.Errorf("cannot move %s: %v", name, err)
		}
	}
	backoff := wait.Backoff{
		Steps:    3,
		Duration: 10 * time.Second,
		Factor:   1.0,
		Jitter:   0.1,
	}
	attempt := 0
	err := retry.OnError(backoff, func(err error) bool { return true }, func() error {
		attempt++
		log.Infof("Applying Manifests. Attempt %d/3", attempt)
		applier := NewApplier(cfg, namespace)
		return applier.ApplyFile(directory)
	})
	if err != nil {
		return fmt.Errorf("Failed to apply manifests: %v", err)
	}
	return nil
}

func createBrandingSecret(client kubeclient.Interface, namespace, fileName string) error {
	objBytes, err := ioutil.ReadFile(fileName)
	if err != nil {
		return err
	}
	requiredObj, err := runtime.Decode(coreCodecs.UniversalDecoder(corev1.SchemeGroupVersion), objBytes)
	if err != nil {
		return err
	}
	secret, ok := requiredObj.(*corev1.Secret)
	if !ok {
		return fmt.Errorf("object in %s is not a secret", fileName)
	}
	_, err = client.CoreV1().Secrets(namespace).Create(secret)
	return err
}

func createKubeAPIServerService(client kubeclient.Interface, namespace string) (int, error) {
	svc := &corev1.Service{}
	svc.Name = "kube-apiserver"
	svc.Spec.Selector = map[string]string{"app": "kube-apiserver"}
	svc.Spec.Type = corev1.ServiceTypeNodePort
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Port:       6443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(6443),
		},
	}
	svc, err := client.CoreV1().Services(namespace).Create(svc)
	if err != nil {
		return 0, err
	}
	return int(svc.Spec.Ports[0].NodePort), nil
}

func createVPNServerService(client kubeclient.Interface, namespace string) (int, error) {
	svc := &corev1.Service{}
	svc.Name = "openvpn-server"
	svc.Spec.Selector = map[string]string{"app": "openvpn-server"}
	svc.Spec.Type = corev1.ServiceTypeNodePort
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Port:       1194,
			Protocol:   corev1.ProtocolUDP,
			TargetPort: intstr.FromInt(1194),
		},
	}
	svc, err := client.CoreV1().Services(namespace).Create(svc)
	if err != nil {
		return 0, err
	}
	return int(svc.Spec.Ports[0].NodePort), nil
}

func createOpenshiftService(client kubeclient.Interface, namespace string) (string, error) {
	svc := &corev1.Service{}
	svc.Name = "openshift-apiserver"
	svc.Spec.Selector = map[string]string{"app": "openshift-apiserver"}
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(8443),
		},
	}
	svc, err := client.CoreV1().Services(namespace).Create(svc)
	if err != nil {
		return "", err
	}
	return svc.Spec.ClusterIP, nil
}

func createOauthService(client kubeclient.Interface, namespace string) (int, error) {
	svc := &corev1.Service{}
	svc.Name = "oauth-openshift"
	svc.Spec.Selector = map[string]string{"app": "oauth-openshift"}
	svc.Spec.Type = corev1.ServiceTypeNodePort
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "https",
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromInt(6443),
		},
	}
	svc, err := client.CoreV1().Services(namespace).Create(svc)
	if err != nil {
		return 0, err
	}
	return int(svc.Spec.Ports[0].NodePort), nil
}

func createPullSecret(client kubeclient.Interface, namespace, data string) error {
	secret := &corev1.Secret{}
	secret.Name = "pull-secret"
	secret.Data = map[string][]byte{".dockerconfigjson": []byte(data)}
	secret.Type = corev1.SecretTypeDockerConfigJson
	_, err := client.CoreV1().Secrets(namespace).Create(secret)
	if err != nil {
		return err
	}
	retry.RetryOnConflict(retry.DefaultRetry, func() error {
		sa, err := client.CoreV1().ServiceAccounts(namespace).Get("default", metav1.GetOptions{})
		if err != nil {
			return err
		}
		sa.ImagePullSecrets = append(sa.ImagePullSecrets, corev1.LocalObjectReference{Name: "pull-secret"})
		_, err = client.CoreV1().ServiceAccounts(namespace).Update(sa)
		return err
	})
	return nil
}

func generateTargetPullSecret(data []byte, fileName string) error {
	secret := &corev1.Secret{}
	secret.Name = "pull-secret"
	secret.Namespace = "openshift-config"
	secret.Data = map[string][]byte{".dockerconfigjson": data}
	secret.Type = corev1.SecretTypeDockerConfigJson
	secretBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return err
	}
	configMap := &corev1.ConfigMap{}
	configMap.APIVersion = "v1"
	configMap.Kind = "ConfigMap"
	configMap.Name = "user-manifest-pullsecret"
	configMap.Data = map[string]string{"data": string(secretBytes)}
	configMapBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), configMap)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(fileName, configMapBytes, 0644)
}

func getPullSecret(client kubeclient.Interface) (string, error) {
	secret, err := client.CoreV1().Secrets("openshift-config").Get("pull-secret", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	pullSecret, ok := secret.Data[".dockerconfigjson"]
	if !ok {
		return "", fmt.Errorf("did not find pull secret data in secret")
	}
	return string(pullSecret), nil
}

func getAWSCredentials(client kubeclient.Interface) (string, string, error) {

	secret, err := client.CoreV1().Secrets("kube-system").Get("aws-creds", metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	key, ok := secret.Data["aws_access_key_id"]
	if !ok {
		return "", "", fmt.Errorf("did not find an AWS access key")
	}
	secretKey, ok := secret.Data["aws_secret_access_key"]
	if !ok {
		return "", "", fmt.Errorf("did not find an AWS secret access key")
	}
	return string(key), string(secretKey), nil
}

func getMachineNames(client dynamic.Interface) ([]string, error) {
	machineGroupVersion, err := schema.ParseGroupVersion("machine.openshift.io/v1beta1")
	if err != nil {
		return nil, err
	}
	machineGroupVersionResource := machineGroupVersion.WithResource("machines")
	list, err := client.Resource(machineGroupVersionResource).Namespace("openshift-machine-api").List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, m := range list.Items {
		names = append(names, m.GetName())
	}
	return names, nil
}

func getMachineInfo(client dynamic.Interface, machineNames []string, prefix string) (string, string, error) {
	name := ""
	for _, machineName := range machineNames {
		if strings.HasPrefix(machineName, prefix) {
			name = machineName
			break
		}
	}
	if name == "" {
		return "", "", fmt.Errorf("did not find machine with prefix %s", prefix)
	}
	machineGroupVersion, err := schema.ParseGroupVersion("machine.openshift.io/v1beta1")
	if err != nil {
		return "", "", err
	}
	machineGroupVersionResource := machineGroupVersion.WithResource("machines")
	machine, err := client.Resource(machineGroupVersionResource).Namespace("openshift-machine-api").Get(name, metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	instanceID, exists, err := unstructured.NestedString(machine.Object, "status", "providerStatus", "instanceId")
	if !exists || err != nil {
		return "", "", fmt.Errorf("did not find instanceId on machine object: %v", err)
	}
	addresses, exists, err := unstructured.NestedSlice(machine.Object, "status", "addresses")
	if !exists || err != nil {
		return "", "", fmt.Errorf("did not find addresses on machine object: %v", err)
	}
	machineIP := ""
	for _, addr := range addresses {
		addrType, _, err := unstructured.NestedString(addr.(map[string]interface{}), "type")
		if err != nil {
			return "", "", fmt.Errorf("cannot get address type: %v", err)
		}
		if addrType != "InternalIP" {
			continue
		}
		machineIP, _, err = unstructured.NestedString(addr.(map[string]interface{}), "address")
		if err != nil {
			return "", "", fmt.Errorf("cannot get machine address: %v", err)
		}
	}
	if machineIP == "" {
		return "", "", fmt.Errorf("could not find machine internal IP")
	}
	return instanceID, machineIP, nil
}

func getSSHPublicKey(client dynamic.Interface) ([]byte, error) {
	machineConfigGroupVersion, err := schema.ParseGroupVersion("machineconfiguration.openshift.io/v1")
	if err != nil {
		return nil, err
	}
	machineConfigGroupVersionResource := machineConfigGroupVersion.WithResource("machineconfigs")
	obj, err := client.Resource(machineConfigGroupVersionResource).Get("99-master-ssh", metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	obj.GetName()
	users, exists, err := unstructured.NestedSlice(obj.Object, "spec", "config", "passwd", "users")
	if !exists || err != nil {
		return nil, fmt.Errorf("could not find users slice in ssh machine config: %v", err)
	}
	keys, exists, err := unstructured.NestedStringSlice(users[0].(map[string]interface{}), "sshAuthorizedKeys")
	if !exists || err != nil {
		return nil, fmt.Errorf("could not find authorized keys for machine config: %v", err)
	}
	return []byte(keys[0]), nil
}

func getInfrastructureInfo(client dynamic.Interface) (string, string, error) {
	infraGroupVersion, err := schema.ParseGroupVersion("config.openshift.io/v1")
	if err != nil {
		return "", "", err
	}
	infraGroupVersionResource := infraGroupVersion.WithResource("infrastructures")
	obj, err := client.Resource(infraGroupVersionResource).Get("cluster", metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	infraName, exists, err := unstructured.NestedString(obj.Object, "status", "infrastructureName")
	if !exists || err != nil {
		return "", "", fmt.Errorf("could not find the infrastructure name in the infrastructure resource: %v", err)
	}
	region, exists, err := unstructured.NestedString(obj.Object, "status", "platformStatus", "aws", "region")
	if !exists || err != nil {
		return "", "", fmt.Errorf("could not find the AWS region in the infrastructure resource: %v", err)
	}

	return infraName, region, nil
}

func getDNSZoneInfo(client dynamic.Interface) (string, string, error) {
	configGroupVersion, err := schema.ParseGroupVersion("config.openshift.io/v1")
	if err != nil {
		return "", "", err
	}
	dnsGroupVersionResource := configGroupVersion.WithResource("dnses")
	obj, err := client.Resource(dnsGroupVersionResource).Get("cluster", metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	publicZoneID, exists, err := unstructured.NestedString(obj.Object, "spec", "publicZone", "id")
	if !exists || err != nil {
		return "", "", fmt.Errorf("could not find the dns public zone id in the dns resource: %v", err)
	}
	domain, exists, err := unstructured.NestedString(obj.Object, "spec", "baseDomain")
	if !exists || err != nil {
		return "", "", fmt.Errorf("could not find the dns base domain in the dns resource: %v", err)
	}
	parts := strings.Split(domain, ".")
	baseDomain := strings.Join(parts[1:], ".")

	return publicZoneID, baseDomain, nil
}

// loadConfig loads a REST Config as per the rules specified in GetConfig
func loadConfig() (*rest.Config, error) {
	if len(os.Getenv("KUBECONFIG")) > 0 {
		return clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	}
	if c, err := rest.InClusterConfig(); err == nil {
		return c, nil
	}
	if usr, err := user.Current(); err == nil {
		if c, err := clientcmd.BuildConfigFromFlags(
			"", filepath.Join(usr.HomeDir, ".kube", "config")); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("could not locate a kubeconfig")
}

func getReleaseImage(client dynamic.Interface) (string, error) {
	configGroupVersion, err := schema.ParseGroupVersion("config.openshift.io/v1")
	if err != nil {
		return "", err
	}
	clusterVersionGVR := configGroupVersion.WithResource("clusterversions")
	obj, err := client.Resource(clusterVersionGVR).Get("version", metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	releaseImage, exists, err := unstructured.NestedString(obj.Object, "status", "desired", "image")
	if !exists || err != nil {
		return "", fmt.Errorf("cannot find release image in cluster version resource")
	}
	return releaseImage, nil
}

func getNetworkInfo(client dynamic.Interface) (string, string, error) {
	configGroupVersion, err := schema.ParseGroupVersion("config.openshift.io/v1")
	if err != nil {
		return "", "", err
	}
	networkGroupVersionResource := configGroupVersion.WithResource("networks")
	obj, err := client.Resource(networkGroupVersionResource).Get("cluster", metav1.GetOptions{})
	if err != nil {
		return "", "", err
	}
	serviceNetworks, exists, err := unstructured.NestedSlice(obj.Object, "status", "serviceNetwork")
	if !exists || err != nil || len(serviceNetworks) == 0 {
		return "", "", fmt.Errorf("could not find service networks in the network status: %v", err)
	}
	serviceCIDR := serviceNetworks[0].(string)

	podNetworks, exists, err := unstructured.NestedSlice(obj.Object, "status", "clusterNetwork")
	if !exists || err != nil || len(podNetworks) == 0 {
		return "", "", fmt.Errorf("could not find cluster networks in the network status: %v", err)
	}
	podCIDR, exists, err := unstructured.NestedString(podNetworks[0].(map[string]interface{}), "cidr")
	if !exists || err != nil {
		return "", "", fmt.Errorf("cannot find cluster network cidr: %v", err)
	}
	return serviceCIDR, podCIDR, nil
}

func generateWorkerMachineset(client dynamic.Interface, infraName, zone, namespace, lbName, fileName string) error {
	machineGV, err := schema.ParseGroupVersion("machine.openshift.io/v1beta1")
	if err != nil {
		return err
	}
	machineSetGVR := machineGV.WithResource("machinesets")
	obj, err := client.Resource(machineSetGVR).Namespace("openshift-machine-api").Get(fmt.Sprintf("%s-worker-%s", infraName, zone), metav1.GetOptions{})
	if err != nil {
		return err
	}

	workerName := generateMachineSetName(infraName, namespace, "worker")
	object := obj.Object

	unstructured.RemoveNestedField(object, "status")
	unstructured.RemoveNestedField(object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(object, "metadata", "generation")
	unstructured.RemoveNestedField(object, "metadata", "resourceVersion")
	unstructured.RemoveNestedField(object, "metadata", "selfLink")
	unstructured.RemoveNestedField(object, "metadata", "uid")
	unstructured.RemoveNestedField(object, "spec", "template", "spec", "metadata")
	unstructured.RemoveNestedField(object, "spec", "template", "spec", "providerSpec", "value", "publicIp")
	unstructured.SetNestedField(object, int64(workerMachineSetCount), "spec", "replicas")
	unstructured.SetNestedField(object, workerName, "metadata", "name")
	unstructured.SetNestedField(object, workerName, "spec", "selector", "matchLabels", "machine.openshift.io/cluster-api-machineset")
	unstructured.SetNestedField(object, workerName, "spec", "template", "metadata", "labels", "machine.openshift.io/cluster-api-machineset")
	unstructured.SetNestedField(object, fmt.Sprintf("%s-user-data", namespace), "spec", "template", "spec", "providerSpec", "value", "userDataSecret", "name")
	loadBalancer := map[string]interface{}{}
	unstructured.SetNestedField(loadBalancer, lbName, "name")
	unstructured.SetNestedField(loadBalancer, "network", "type")
	loadBalancers := []interface{}{loadBalancer}
	unstructured.SetNestedSlice(object, loadBalancers, "spec", "template", "spec", "providerSpec", "value", "loadBalancers")

	machineSetBytes, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(fileName, machineSetBytes, 0644)
}

func generateUserDataSecret(namespace, bucketName, fileName string) error {
	secret := &corev1.Secret{}
	secret.Kind = "Secret"
	secret.APIVersion = "v1"
	secret.Name = fmt.Sprintf("%s-user-data", namespace)
	secret.Namespace = "openshift-machine-api"

	disableTemplatingValue := []byte(base64.StdEncoding.EncodeToString([]byte("true")))
	userDataValue := []byte(fmt.Sprintf(`{"ignition":{"config":{"append":[{"source":"https://%s.s3.amazonaws.com/worker.ign","verification":{}}]},"security":{},"timeouts":{},"version":"2.2.0"},"networkd":{},"passwd":{},"storage":{},"systemd":{}}`, bucketName))

	secret.Data = map[string][]byte{
		"disableTemplating": disableTemplatingValue,
		"userData":          userDataValue,
	}

	secretBytes, err := json.Marshal(secret)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(fileName, secretBytes, 0644)
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func ensurePrivilegedSCC(client dynamic.Interface, namespace string) error {
	securityGV, err := schema.ParseGroupVersion("security.openshift.io/v1")
	if err != nil {
		return err
	}
	sccGVR := securityGV.WithResource("securitycontextconstraints")
	obj, err := client.Resource(sccGVR).Get("privileged", metav1.GetOptions{})
	if err != nil {
		return err
	}
	users, exists, err := unstructured.NestedStringSlice(obj.Object, "users")
	if err != nil {
		return err
	}
	userSet := sets.NewString()
	if exists {
		userSet.Insert(users...)
	}
	svcAccount := fmt.Sprintf("system:serviceaccount:%s:default", namespace)
	if userSet.Has(svcAccount) {
		// No need to update anything, service account already has privileged SCC
		return nil
	}
	userSet.Insert(svcAccount)

	if err = unstructured.SetNestedStringSlice(obj.Object, userSet.List(), "users"); err != nil {
		return err
	}

	_, err = client.Resource(sccGVR).Update(obj, metav1.UpdateOptions{})
	return err
}

func generateKubeadminPasswordTargetSecret(password string, fileName string) error {
	secret := &corev1.Secret{}
	secret.APIVersion = "v1"
	secret.Kind = "Secret"
	secret.Name = "kubeadmin"
	secret.Namespace = "kube-system"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	secret.Data = map[string][]byte{"kubeadmin": passwordHash}

	secretBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return err
	}
	configMap := &corev1.ConfigMap{}
	configMap.APIVersion = "v1"
	configMap.Kind = "ConfigMap"
	configMap.Name = "user-manifest-kubeadmin-password"
	configMap.Data = map[string]string{"data": string(secretBytes)}
	configMapBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), configMap)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(fileName, configMapBytes, 0644)
}

func generateRouterService(fileName string) error {
	svc := &corev1.Service{}
	svc.APIVersion = "v1"
	svc.Kind = "Service"
	svc.Name = "router-default"
	svc.Namespace = "openshift-ingress"
	svc.Labels = map[string]string{
		"app":    "router",
		"router": "router-default",
	}
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:       "http",
			NodePort:   routerNodePortHTTP,
			Port:       80,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromString("http"),
		},
		{
			Name:       "https",
			NodePort:   routerNodePortHTTPS,
			Port:       443,
			Protocol:   corev1.ProtocolTCP,
			TargetPort: intstr.FromString("https"),
		},
	}
	svc.Spec.Selector = map[string]string{
		"ingresscontroller.operator.openshift.io/deployment-ingresscontroller": "default",
	}
	svc.Spec.SessionAffinity = corev1.ServiceAffinityNone
	svc.Spec.Type = corev1.ServiceTypeNodePort

	svcBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), svc)
	if err != nil {
		return err
	}

	configMap := &corev1.ConfigMap{}
	configMap.APIVersion = "v1"
	configMap.Kind = "ConfigMap"
	configMap.Name = "user-manifest-router-service"
	configMap.Data = map[string]string{"data": string(svcBytes)}
	configMapBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), configMap)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(fileName, configMapBytes, 0644)
}

func generateKubeadminPasswordSecret(password string, fileName string) error {
	secret := &corev1.Secret{}
	secret.APIVersion = "v1"
	secret.Kind = "Secret"
	secret.Name = "kubeadmin-password"
	secret.Data = map[string][]byte{"password": []byte(password)}
	secretBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(fileName, secretBytes, 0644)
}

func generateKubeconfigSecret(kubeconfigFile, manifestFilename string) error {
	secret := &corev1.Secret{}
	secret.APIVersion = "v1"
	secret.Kind = "Secret"
	secret.Name = "admin-kubeconfig"
	kubeconfigBytes, err := ioutil.ReadFile(kubeconfigFile)
	if err != nil {
		return err
	}
	secret.Data = map[string][]byte{"kubeconfig": kubeconfigBytes}
	secretBytes, err := runtime.Encode(coreCodecs.LegacyCodec(corev1.SchemeGroupVersion), secret)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(manifestFilename, secretBytes, 0644)
}

func updateOAuthDeployment(client kubeclient.Interface, namespace string) error {
	d, err := client.AppsV1().Deployments(namespace).Get("oauth-openshift", metav1.GetOptions{})
	if err != nil {
		return err
	}
	annotations := d.Spec.Template.ObjectMeta.Annotations
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations["deployment-refresh"] = fmt.Sprintf("%v", time.Now())
	d.Spec.Template.ObjectMeta.Annotations = annotations
	_, err = client.AppsV1().Deployments(namespace).Update(d)
	return err
}

func generateImageRegistrySecret() string {
	num := make([]byte, 64)
	rand.Read(num)
	return hex.EncodeToString(num)
}

func generateKubeadminPassword() (string, error) {
	const (
		lowerLetters = "abcdefghijkmnopqrstuvwxyz"
		upperLetters = "ABCDEFGHIJKLMNPQRSTUVWXYZ"
		digits       = "23456789"
		all          = lowerLetters + upperLetters + digits
		length       = 23
	)
	var password string
	for i := 0; i < length; i++ {
		n, err := crand.Int(crand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", err
		}
		newchar := string(all[n.Int64()])
		if password == "" {
			password = newchar
		}
		if i < length-1 {
			n, err = crand.Int(crand.Reader, big.NewInt(int64(len(password)+1)))
			if err != nil {
				return "", err
			}
			j := n.Int64()
			password = password[0:j] + newchar + password[j:]
		}
	}
	pw := []rune(password)
	for _, replace := range []int{5, 11, 17} {
		pw[replace] = '-'
	}
	return string(pw), nil
}

func getTargetClusterConfig(pkiDir string) (*rest.Config, error) {
	return clientcmd.BuildConfigFromFlags("", filepath.Join(pkiDir, "admin.kubeconfig"))
}

func generateLBResourceName(infraName, clusterName, suffix string) string {
	return getName(fmt.Sprintf("%s-%s", infraName, clusterName), suffix, 32)
}

func generateBucketName(infraName, clusterName, suffix string) string {
	return getName(fmt.Sprintf("%s-%s", infraName, clusterName), suffix, 63)
}

func generateMachineSetName(infraName, clusterName, suffix string) string {
	return getName(fmt.Sprintf("%s-%s", infraName, clusterName), suffix, 43)
}

// getName returns a name given a base ("deployment-5") and a suffix ("deploy")
// It will first attempt to join them with a dash. If the resulting name is longer
// than maxLength: if the suffix is too long, it will truncate the base name and add
// an 8-character hash of the [base]-[suffix] string.  If the suffix is not too long,
// it will truncate the base, add the hash of the base and return [base]-[hash]-[suffix]
func getName(base, suffix string, maxLength int) string {
	if maxLength <= 0 {
		return ""
	}
	name := fmt.Sprintf("%s-%s", base, suffix)
	if len(name) <= maxLength {
		return name
	}

	baseLength := maxLength - 10 /*length of -hash-*/ - len(suffix)

	// if the suffix is too long, ignore it
	if baseLength < 0 {
		prefix := base[0:min(len(base), max(0, maxLength-9))]
		// Calculate hash on initial base-suffix string
		shortName := fmt.Sprintf("%s-%s", prefix, hash(name))
		return shortName[:min(maxLength, len(shortName))]
	}

	prefix := base[0:baseLength]
	// Calculate hash on initial base-suffix string
	return fmt.Sprintf("%s-%s-%s", prefix, hash(base), suffix)
}

// max returns the greater of its 2 inputs
func max(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// min returns the lesser of its 2 inputs
func min(a, b int) int {
	if b < a {
		return b
	}
	return a
}

// hash calculates the hexadecimal representation (8-chars)
// of the hash of the passed in string using the FNV-a algorithm
func hash(s string) string {
	hash := fnv.New32a()
	hash.Write([]byte(s))
	intHash := hash.Sum32()
	result := fmt.Sprintf("%08x", intHash)
	return result
}
