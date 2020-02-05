package cmca

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	destConfigMap                   = "kube-controller-manager"
	kubeControllerManagerDeployment = "kube-controller-manager"
)

// ControllerManagerCAUpdater is a controller that updates the kube controller manager's service-ca
// and annotates the kube controller manager's deployment with a hash to force a restart in case the
// contents of the service ca change.
type ControllerManagerCAUpdater struct {
	// Client is a client of the management cluster
	client.Client

	// Log is the logger for this controller
	Log logr.Logger

	// InitialCA is the initial CA for the controller manager
	InitialCA string

	// Namespace is the namespace where the control plane of the cluster
	// lives on the management server
	Namespace string
}

// Reconcile periodically watches for changes in the CA configmaps and updates
// the kube-controller-manager-ca configmap in the management cluster with their
// content.
func (r *ControllerManagerCAUpdater) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	controllerLog := r.Log.WithValues("configmap", req.NamespacedName.String())
	ctx := context.Background()

	// Ignore any namespace that is not the Namespace.
	if req.Namespace != r.Namespace {
		return ctrl.Result{}, nil
	}

	// Ignore all configmaps except the one that contains the controller manager additional CAs
	if req.Name != ControllerManagerAdditionalCAConfigMap {
		return ctrl.Result{}, nil
	}

	controllerLog.Info("Begin reconciling")

	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, req.NamespacedName, cm)
	if err != nil && !errors.IsNotFound(err) {
		r.Log.Error(err, "Failed to fetch configmap")
		return ctrl.Result{}, err
	}

	routerCA := cm.Data["router-ca"]
	serviceCA := cm.Data["service-ca"]

	ca := &bytes.Buffer{}
	if _, err = fmt.Fprintf(ca, "%s", r.InitialCA); err != nil {
		return ctrl.Result{}, err
	}
	if len(routerCA) > 0 {
		if _, err = fmt.Fprintf(ca, "%s", routerCA); err != nil {
			return ctrl.Result{}, err
		}
	}
	if len(serviceCA) > 0 {
		if _, err = fmt.Fprintf(ca, "%s", serviceCA); err != nil {
			return ctrl.Result{}, err
		}
	}

	hash := calculateHash(ca.Bytes())
	r.Log.Info("Calculated controller manager hash", "hash", hash)

	destinationCM := &corev1.ConfigMap{}
	if err = r.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: destConfigMap}, destinationCM); err != nil {
		return ctrl.Result{}, err
	}
	destinationCM.Data["service-ca.crt"] = ca.String()
	r.Log.Info("Updating controller manager configmap")
	if err = r.Update(ctx, destinationCM); err != nil {
		return ctrl.Result{}, err
	}

	cmDeployment := &appsv1.Deployment{}
	if err = r.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: kubeControllerManagerDeployment}, cmDeployment); err != nil {
		return ctrl.Result{}, err
	}
	r.Log.Info("Updating controller manager deployment checksum annotation")
	cmDeployment.Spec.Template.ObjectMeta.Annotations["ca-checksum"] = hash
	if err = r.Update(ctx, cmDeployment); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, err
}

func calculateHash(b []byte) string {
	return fmt.Sprintf("%x", md5.Sum(b))
}
