package kubelet_serving_ca

import (
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift-toolkit/pkg/cmd/cpoperator"
)

func Setup(cfg *cpoperator.ControlPlaneOperatorConfig) error {

	clusterVersions := cfg.TargetConfigInformers().Config().V1().ClusterVersions()

	reconciler := &KubeletServingCASyncer{
		InitialCA:    cfg.InitialCA(),
		TargetClient: cfg.TargetKubeClient(),
		Log:          cfg.Logger().WithName("KubeletServingCA"),
	}
	c, err := controller.New("kubelet-serving-ca", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: clusterVersions.Informer()}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}
