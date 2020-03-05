package clusteroperator

import (
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	configclient "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"

	"github.com/openshift/hypershift-toolkit/pkg/cmd/cpoperator"
	"github.com/openshift/hypershift-toolkit/pkg/controllers"
)

func Setup(cfg *cpoperator.ControlPlaneOperatorConfig) error {
	openshiftClient, err := configclient.NewForConfig(cfg.TargetConfig())
	if err != nil {
		return err
	}
	informerFactory := configinformers.NewSharedInformerFactory(openshiftClient, controllers.DefaultResync)
	cfg.Manager().Add(manager.RunnableFunc(func(stopCh <-chan struct{}) error {
		informerFactory.Start(stopCh)
		return nil
	}))
	clusterOperators := informerFactory.Config().V1().ClusterOperators()
	reconciler := &ControlPlaneClusterOperatorSyncer{
		Versions: cfg.Versions(),
		Client:   openshiftClient,
		Lister:   clusterOperators.Lister(),
		Log:      cfg.Logger().WithName("ControlPlaneClusterOperatorSyncer"),
	}
	c, err := controller.New("cluster-operator-syncer", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: clusterOperators.Informer()}, &handler.EnqueueRequestForObject{}); err != nil {
		return err
	}
	return nil
}
