package cmca

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/openshift/hypershift-toolkit/pkg/cmd/cpoperator"
	"github.com/openshift/hypershift-toolkit/pkg/controllers"
)

const (
	ManagedConfigNamespace                 = "openshift-config-managed"
	ControllerManagerAdditionalCAConfigMap = "controller-manager-additional-ca"
)

func Setup(cfg *cpoperator.ControlPlaneOperatorConfig) error {
	if err := setupConfigMapObserver(cfg); err != nil {
		return err
	}

	if err := setupControllerManagerCAUpdater(cfg); err != nil {
		return err
	}

	return nil
}

func setupConfigMapObserver(cfg *cpoperator.ControlPlaneOperatorConfig) error {
	informerFactory := informers.NewSharedInformerFactoryWithOptions(cfg.TargetKubeClient(), controllers.DefaultResync, informers.WithNamespace(ManagedConfigNamespace))
	cfg.Manager().Add(manager.RunnableFunc(func(stopCh <-chan struct{}) error {
		informerFactory.Start(stopCh)
		return nil
	}))
	configMaps := informerFactory.Core().V1().ConfigMaps()
	reconciler := &ManagedCAObserver{
		Client:         cfg.Manager().GetClient(),
		TargetCMLister: configMaps.Lister(),
		Namespace:      cfg.Namespace(),
		Log:            cfg.Logger().WithName("ManagedCAObserver"),
	}
	c, err := controller.New("ca-configmap-observer", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Informer{Informer: configMaps.Informer()}, controllers.NamedResourceHandler(RouterCAConfigMap, ServiceCAConfigMap)); err != nil {
		return err
	}
	return nil
}

func setupControllerManagerCAUpdater(cfg *cpoperator.ControlPlaneOperatorConfig) error {
	reconciler := &ControllerManagerCAUpdater{
		Client:    cfg.Manager().GetClient(),
		Namespace: cfg.Namespace(),
		Log:       cfg.Logger().WithName("ControllerManagerCAUpdater"),
	}
	c, err := controller.New("controller-manager-ca-updater", cfg.Manager(), controller.Options{Reconciler: reconciler})
	if err != nil {
		return err
	}
	if err := c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, controllers.NamedResourceHandler(ControllerManagerAdditionalCAConfigMap)); err != nil {
		return err
	}
	return nil
}
