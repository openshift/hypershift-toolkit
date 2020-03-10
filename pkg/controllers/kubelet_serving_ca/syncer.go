package kubelet_serving_ca

import (
	"context"
	"time"

	"github.com/go-logr/logr"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// syncInterval is the amount of time to use between checks
var syncInterval = 5 * time.Minute

type KubeletServingCASyncer struct {
	client.Client
	Namespace    string
	TargetClient kubeclient.Interface
	Log          logr.Logger
}

func (s *KubeletServingCASyncer) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	if req.Namespace != s.Namespace {
		return result(nil)
	}
	if req.Name != ControlPlaneOperatorConfig {
		return result(nil)
	}
	cpConfig := &corev1.ConfigMap{}
	if err := s.Get(ctx, req.NamespacedName, cpConfig); err != nil {
		return ctrl.Result{}, err
	}
	targetConfigMap, err := s.TargetClient.CoreV1().ConfigMaps("openshift-config-managed").Get("kubelet-serving-ca", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return result(err)
	}
	expectedConfigMap := s.expectedConfigMap(cpConfig)
	if err != nil {
		s.Log.Info("target configmap not found, creating it")
		_, err = s.TargetClient.CoreV1().ConfigMaps("openshift-config-managed").Create(expectedConfigMap)
		return result(err)
	}
	if targetConfigMap.Data["ca-bundle.crt"] != expectedConfigMap.Data["ca-bundle.crt"] {
		targetConfigMap.Data["ca-bundle.crt"] = expectedConfigMap.Data["ca-bundle.crt"]
		_, err = s.TargetClient.CoreV1().ConfigMaps("openshift-config-managed").Update(targetConfigMap)
		return result(err)
	}
	return result(nil)
}

func result(err error) (ctrl.Result, error) {
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

func (s *KubeletServingCASyncer) expectedConfigMap(source *corev1.ConfigMap) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{}
	cm.Name = "kubelet-serving-ca"
	cm.Namespace = "openshift-config-managed"
	cm.Data = map[string]string{
		"ca-bundle.crt": source.Data["initial-ca.crt"],
	}
	return cm
}
