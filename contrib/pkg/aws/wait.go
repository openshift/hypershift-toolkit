package aws

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	kubeclient "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	clientwatch "k8s.io/client-go/tools/watch"

	configapi "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

const (
	apiEndpointTimeout           = 10 * time.Minute
	nodesReadyTimeout            = 10 * time.Minute
	bootstrapPodCompleteTimeout  = 5 * time.Minute
	clusterOperatorsReadyTimeout = 15 * time.Minute
)

func waitForAPIEndpoint(pkiDir, apiDNSName string) error {
	caCertBytes, err := ioutil.ReadFile(filepath.Join(pkiDir, "root-ca.crt"))
	if err != nil {
		return fmt.Errorf("cannot read CA file: %v", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCertBytes)

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: caCertPool,
			},
		},
		Timeout: 3 * time.Second,
	}

	url := fmt.Sprintf("https://%s:6443/healthz", apiDNSName)

	err = wait.PollImmediate(10*time.Second, apiEndpointTimeout, func() (bool, error) {
		resp, err := client.Get(url)
		if err != nil {
			return false, nil
		}
		return resp.StatusCode == http.StatusOK, nil
	})
	return err
}

func waitForNodesReady(client kubeclient.Interface, expectedCount int) error {
	ctx, cancel := context.WithTimeout(context.Background(), nodesReadyTimeout)
	defer cancel()
	listWatcher := cache.NewListWatchFromClient(client.CoreV1().RESTClient(), "nodes", "", fields.Everything())

	allNodesReady := func(event watch.Event) (bool, error) {
		list, err := listWatcher.List(metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("an error occurred listing nodes: %v", err)
		}
		nodeList, ok := list.(*corev1.NodeList)
		if !ok {
			return false, fmt.Errorf("unexpected object from list function: %t", list)
		}
		if len(nodeList.Items) < expectedCount {
			return false, nil
		}

		for _, node := range nodeList.Items {
			ready := false
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady {
					if cond.Status == corev1.ConditionTrue {
						ready = true
						break
					} else {
						return false, nil
					}
				}
			}
			if !ready {
				return false, nil
			}
		}
		return true, nil
	}
	_, err := clientwatch.UntilWithSync(ctx, listWatcher, &corev1.Node{}, nil, allNodesReady)
	return err
}

func waitForBootstrapPod(client kubeclient.Interface, namespace string) error {
	ctx, cancel := context.WithTimeout(context.Background(), bootstrapPodCompleteTimeout)
	defer cancel()
	listWatcher := cache.NewListWatchFromClient(client.CoreV1().RESTClient(), "pods", "", fields.OneTermEqualSelector("metadata.name", "manifests-bootstrapper"))
	podIsComplete := func(event watch.Event) (bool, error) {
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			return false, fmt.Errorf("unexpected object type")
		}
		if pod.Name != "manifests-bootstrapper" {
			return false, fmt.Errorf("unexpected pod name: %s", pod.Name)
		}
		return pod.Status.Phase == corev1.PodSucceeded, nil
	}
	_, err := clientwatch.UntilWithSync(ctx, listWatcher, &corev1.Pod{}, nil, podIsComplete)
	return err
}

func waitForClusterOperators(cfg *rest.Config) error {
	client, err := configclient.NewForConfig(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), clusterOperatorsReadyTimeout)
	defer cancel()
	listWatcher := cache.NewListWatchFromClient(client.RESTClient(), "clusteroperators", "", fields.Everything())

	clusterOperatorsAreAvailable := func(event watch.Event) (bool, error) {
		list, err := listWatcher.List(metav1.ListOptions{})
		if err != nil {
			return false, fmt.Errorf("an error occurred listing cluster operators: %v", err)
		}
		operatorList, ok := list.(*configapi.ClusterOperatorList)
		if !ok {
			return false, fmt.Errorf("unexpected object from list function: %t", list)
		}

		for _, co := range operatorList.Items {
			available := false
			for _, condition := range co.Status.Conditions {
				if condition.Type == configapi.OperatorAvailable {
					if condition.Status == configapi.ConditionTrue {
						available = true
						break
					} else {
						return false, nil
					}
				}
			}
			if !available {
				return false, nil
			}
		}
		return true, nil
	}

	_, err = clientwatch.UntilWithSync(ctx, listWatcher, &configapi.ClusterOperator{}, nil, clusterOperatorsAreAvailable)
	return err
}
