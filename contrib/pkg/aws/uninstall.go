package aws

import (
	"fmt"

	log "github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	kubeclient "k8s.io/client-go/kubernetes"
)

func UninstallCluster(name string) error {
	// First, ensure that we can access the host cluster
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("cannot access existing cluster; make sure a connection to host cluster is available: %v", err)
	}

	dynamicClient, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("cannot obtain dynamic client: %v", err)
	}

	infraName, region, err := getInfrastructureInfo(dynamicClient)
	if err != nil {
		return fmt.Errorf("failed to obtain infrastructure info for cluster: %v", err)
	}
	log.Debugf("The management cluster infra name is: %s", infraName)
	log.Debugf("The management cluster AWS region is: %s", region)

	dnsZoneID, parentDomain, err := getDNSZoneInfo(dynamicClient)
	if err != nil {
		return fmt.Errorf("failed to obtain public zone information: %v", err)
	}
	log.Debugf("Using public DNS Zone: %s and parent suffix: %s", dnsZoneID, parentDomain)

	client, err := kubeclient.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to obtain a kubernetes client from existing configuration: %v", err)
	}
	awsKey, awsSecretKey, err := getAWSCredentials(client)
	if err != nil {
		return fmt.Errorf("failed to obtain AWS credentials from host cluster: %v", err)
	}
	// Fetch AWS cloud data
	aws, err := NewAWSHelper(awsKey, awsSecretKey, region, infraName)
	if err != nil {
		return fmt.Errorf("cannot create an AWS client: %v", err)
	}

	log.Infof("Removing API DNS record")
	apiDNSName := fmt.Sprintf("api.%s.%s.", name, parentDomain)
	if err = aws.RemoveCNameRecord(dnsZoneID, apiDNSName); err != nil {
		return fmt.Errorf("cannot delete API DNS resource record: %v", err)
	}

	log.Infof("Removing API load balancer")
	apiLBName := generateLBResourceName(infraName, name, "api")
	if err = aws.RemoveNLB(apiLBName); err != nil {
		return fmt.Errorf("cannot delete API load balancer: %v", err)
	}

	log.Infof("Removing API target group")
	if err = aws.RemoveTargetGroup(apiLBName); err != nil {
		return fmt.Errorf("cannot delete API target group: %v", err)
	}

	log.Infof("Removing OAuth target group")
	oauthTGName := generateLBResourceName(infraName, name, "oauth")
	if err = aws.RemoveTargetGroup(oauthTGName); err != nil {
		return fmt.Errorf("cannot delete OAuth target group: %v", err)
	}

	log.Infof("Removing API elastic IP")
	if err = aws.RemoveEIP(apiLBName); err != nil {
		return fmt.Errorf("cannot delete EIP for API load balancer: %v", err)
	}

	log.Infof("Removing VPN DNS record")
	vpnDNSName := fmt.Sprintf("vpn.%s.%s.", name, parentDomain)
	if err = aws.RemoveCNameRecord(dnsZoneID, vpnDNSName); err != nil {
		return fmt.Errorf("cannot delete VPN DNS resource record: %v", err)
	}

	log.Infof("Removing VPN load balancer")
	vpnLBName := generateLBResourceName(infraName, name, "vpn")
	if err = aws.RemoveNLB(vpnLBName); err != nil {
		return fmt.Errorf("cannot delete VPN load balancer: %v", err)
	}

	log.Infof("Removing VPN target group")
	if err = aws.RemoveTargetGroup(vpnLBName); err != nil {
		return fmt.Errorf("cannot delete VPN target group: %v", err)
	}

	log.Infof("Removing router DNS record")
	routerDNSName := fmt.Sprintf("\\052.apps.%s.%s.", name, parentDomain)
	if err = aws.RemoveCNameRecord(dnsZoneID, routerDNSName); err != nil {
		return fmt.Errorf("cannot delete router DNS resource record: %v", err)
	}

	log.Infof("Removing router load balancer")
	routerLBName := generateLBResourceName(infraName, name, "apps")
	if err = aws.RemoveNLB(routerLBName); err != nil {
		return fmt.Errorf("cannot delete router load balancer: %v", err)
	}

	log.Infof("Removing router HTTP target group")
	httpTGName := generateLBResourceName(infraName, name, "http")
	if err = aws.RemoveTargetGroup(httpTGName); err != nil {
		return fmt.Errorf("cannot delete router HTTP target group: %v", err)
	}

	log.Infof("Removing router HTTPS target group")
	httpsTGName := generateLBResourceName(infraName, name, "https")
	if err = aws.RemoveTargetGroup(httpsTGName); err != nil {
		return fmt.Errorf("cannot delete router HTTPS target group: %v", err)
	}

	log.Infof("Removing worker machineset")
	if err = removeWorkerMachineset(dynamicClient, infraName, name); err != nil {
		return fmt.Errorf("failed to remove worker machineset: %v", err)
	}

	log.Infof("Removing bootstrap ignition bucket")
	bucketName := generateBucketName(infraName, name, "ign")
	if err = aws.RemoveIgnitionBucket(bucketName); err != nil {
		return fmt.Errorf("cannot delete ignition bucket: %v", err)
	}

	log.Info("Removing cluster namespace")
	if err = client.CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{}); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete namespace %s: %v", name, err)
		}
	}

	return nil
}

func removeWorkerMachineset(client dynamic.Interface, infraName, namespace string) error {
	name := generateMachineSetName(infraName, namespace, "worker")
	machineGV, err := schema.ParseGroupVersion("machine.openshift.io/v1beta1")
	if err != nil {
		return err
	}
	machineSetGVR := machineGV.WithResource("machinesets")
	err = client.Resource(machineSetGVR).Namespace("openshift-machine-api").Delete(name, &metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}
