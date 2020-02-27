package main

import (
	"flag"
	"fmt"
	"io/ioutil"

	"github.com/spf13/cobra"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/openshift/hypershift-toolkit/pkg/cmd/cpoperator"
	"github.com/openshift/hypershift-toolkit/pkg/controllers/autoapprover"
	"github.com/openshift/hypershift-toolkit/pkg/controllers/cmca"
	"github.com/openshift/hypershift-toolkit/pkg/controllers/kubeadminpwd"
)

func main() {
	log.SetLogger(zap.New(zap.UseDevMode(true)))
	setupLog := ctrl.Log.WithName("setup")
	if err := newControlPlaneOperatorCommand().Execute(); err != nil {
		setupLog.Error(err, "Operator failed")
	}
}

var controllerFuncs = map[string]cpoperator.ControllerSetupFunc{
	"controller-manager-ca": cmca.Setup,
	"auto-approver":         autoapprover.Setup,
	"kubeadmin-password":    kubeadminpwd.Setup,
}

type ControlPlaneOperator struct {
	// Namespace is the namespace on the management cluster where the control plane components run.
	Namespace string

	// TargetKubeconfig is a kubeconfig to access the target cluster.
	TargetKubeconfig string

	// InitialCAFile is a file containing the initial contents of the Kube controller manager CA.
	InitialCAFile string

	// Controllers is the list of controllers that the operator should start
	Controllers []string

	initialCA []byte
}

func newControlPlaneOperatorCommand() *cobra.Command {
	cpo := newControlPlaneOperator()
	cmd := &cobra.Command{
		Use:   "control-plane-operator",
		Short: "The Hypershift Control Plane Operator contains a set of controllers that manage an OpenShift hosted control plane.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cpo.Validate(); err != nil {
				return err
			}
			if err := cpo.Complete(); err != nil {
				return err
			}
			return cpo.Run()
		},
	}
	flags := cmd.Flags()
	flags.AddGoFlagSet(flag.CommandLine)
	flags.StringVar(&cpo.Namespace, "namespace", cpo.Namespace, "Namespace for control plane components on management cluster")
	flags.StringVar(&cpo.TargetKubeconfig, "target-kubeconfig", cpo.TargetKubeconfig, "Kubeconfig for target cluster")
	flags.StringVar(&cpo.TargetKubeconfig, "initial-ca-file", cpo.TargetKubeconfig, "Path to controller manager initial CA file")
	flags.StringSliceVar(&cpo.Controllers, "controllers", cpo.Controllers, "Controllers to run with this operator")
	return cmd
}

func newControlPlaneOperator() *ControlPlaneOperator {
	return &ControlPlaneOperator{
		Controllers: []string{
			"controller-manager-ca",
		},
	}
}

func (o *ControlPlaneOperator) Validate() error {
	if len(o.Controllers) == 0 {
		return fmt.Errorf("at least one controller is required")
	}
	if len(o.Namespace) == 0 {
		return fmt.Errorf("the namespace for control plane components is required")
	}
	return nil
}

func (o *ControlPlaneOperator) Complete() error {
	var err error
	if len(o.InitialCAFile) > 0 {
		o.initialCA, err = ioutil.ReadFile(o.InitialCAFile)
		if err != nil {
			return err
		}
	}
	return nil
}

func (o *ControlPlaneOperator) Run() error {
	cfg := cpoperator.NewControlPlaneOperatorConfig(
		o.TargetKubeconfig,
		o.Namespace,
		o.initialCA,
		o.Controllers,
		controllerFuncs,
	)
	return cfg.Start()
}
