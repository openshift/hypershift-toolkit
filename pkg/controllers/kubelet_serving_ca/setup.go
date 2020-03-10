package kubelet_serving_ca

import (
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift-toolkit/pkg/cmd/cpoperator"
	"github.com/openshift/hypershift-toolkit/pkg/controllers"
)

const (
	ControlPlaneOperatorConfig = "control-plane-operator"
)

func Setup(cfg *cpoperator.ControlPlaneOperatorConfig) error {

	reconciler := &KubeletServingCASyncer{
		Client:       cfg.Manager().GetClient(),
		Namespace:    cfg.Namespace(),
		TargetClient: cfg.TargetKubeClient(),
		Log:          cfg.Logger().WithName("KubeletServingCA"),
	}
	c, err := controller.New("kubelet-serving-ca", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, controllers.NamedResourceHandler(ControlPlaneOperatorConfig)); err != nil {
		return err
	}
	return nil
}
