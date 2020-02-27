package cmca

import (
	"context"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	RouterCAConfigMap  = "router-ca"
	ServiceCAConfigMap = "service-ca"
)

// ManagedCAObserver watches 2 CA configmaps in the target cluster:
// - openshift-managed-config/router-ca
// - openshift-managed-config/service-ca
// It populates a configmap on the management cluster with their content.
// A separate controller uses that content to adjust the configmap for
// the Kube controller manager CA.
type ManagedCAObserver struct {

	// Client is a client that allows access to the management cluster
	client.Client

	// TargetCMLister is a lister for configmaps in the target cluster
	TargetCMLister corev1listers.ConfigMapLister

	// Namespace is the namespace where the control plane of the cluster
	// lives on the management server
	Namespace string

	// Log is the logger for this controller
	Log logr.Logger
}

// Reconcile periodically watches for changes in the CA configmaps and updates
// the kube-controller-manager-ca configmap in the management cluster with their
// content.
func (r *ManagedCAObserver) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	controllerLog := r.Log.WithValues("configmap", req.NamespacedName)
	ctx := context.Background()

	if req.Namespace != ManagedConfigNamespace {
		return ctrl.Result{}, nil
	}

	var err error
	switch req.Name {
	case RouterCAConfigMap:
		err = r.syncConfigMap(ctx, controllerLog, req.NamespacedName, "ca-bundle.crt", "router-ca")
	case ServiceCAConfigMap:
		err = r.syncConfigMap(ctx, controllerLog, req.NamespacedName, "ca-bundle.crt", "service-ca")
	}

	return ctrl.Result{}, err
}

func (r *ManagedCAObserver) syncConfigMap(ctx context.Context, logger logr.Logger, configMapName types.NamespacedName, sourceKey, destKey string) error {
	logger.Info("Syncing configmap")
	caData := ""
	sourceCM, err := r.TargetCMLister.ConfigMaps(configMapName.Namespace).Get(configMapName.Name)
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Error fetching configmap")
			return err
		}
	} else {
		if !sourceCM.DeletionTimestamp.IsZero() {
			caData = ""
		} else {
			caData = sourceCM.Data[sourceKey]
		}
	}
	targetCM := &corev1.ConfigMap{}
	createCM := false
	targetCMName := types.NamespacedName{Namespace: r.Namespace, Name: ControllerManagerAdditionalCAConfigMap}
	err = r.Get(ctx, targetCMName, targetCM)
	if err != nil {
		if !errors.IsNotFound(err) {
			logger.Error(err, "Error fetching target configmap")
			return err
		}
		targetCM.Namespace = r.Namespace
		targetCM.Name = ControllerManagerAdditionalCAConfigMap
		targetCM.Data = map[string]string{}
		createCM = true
	}
	targetCM.Data[destKey] = caData
	if createCM {
		logger.Info("creating configmap in management cluster", "targetconfigmap", targetCMName.String())
		return r.Create(ctx, targetCM)
	}
	logger.Info("updating configmap in management cluster", "targetconfigmap", targetCMName.String())
	return r.Update(ctx, targetCM)
}
