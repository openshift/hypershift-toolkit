package render

import (
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift-toolkit/pkg/cmd/util"
	"github.com/openshift/hypershift-toolkit/pkg/config"
	"github.com/openshift/hypershift-toolkit/pkg/render"
)

type RenderOptions struct {
	PKIDir         string
	OutputDir      string
	ConfigFile     string
	PullSecretFile string
}

func NewRenderCommand() *cobra.Command {
	opt := &RenderOptions{}
	cmd := &cobra.Command{
		Use: "render",
		Run: func(cmd *cobra.Command, args []string) {
			if err := opt.Run(); err != nil {
				log.WithError(err).Fatal("Error occurred rendering manifests")
			}
		},
	}
	cmd.Flags().StringVar(&opt.OutputDir, "output-dir", defaultManifestsDir(), "Specify the directory where PKI artifacts should be output")
	cmd.Flags().StringVar(&opt.PKIDir, "pki-dir", defaultPKIDir(), "Specify the directory where the input PKI files have been placed")
	cmd.Flags().StringVar(&opt.ConfigFile, "config", defaultConfigFile(), "Specify the config file for this cluster")
	cmd.Flags().StringVar(&opt.PullSecretFile, "pull-secret", defaultPullSecretFile(), "Specify the config file for this cluster")
	return cmd
}

func (o *RenderOptions) Run() error {
	util.EnsureDir(o.OutputDir)
	params, err := config.ReadFrom(o.ConfigFile)
	if err != nil {
		log.WithError(err).Fatalf("Error occurred reading configuration")
	}
	return render.RenderClusterManifests(params, o.PullSecretFile, o.OutputDir, o.PKIDir)
}

func defaultManifestsDir() string {
	return filepath.Join(util.WorkingDir(), "manifests")
}

func defaultPKIDir() string {
	return filepath.Join(util.WorkingDir(), "pki")
}

func defaultConfigFile() string {
	return filepath.Join(util.WorkingDir(), "cluster.yaml")
}

func defaultPullSecretFile() string {
	return filepath.Join(util.WorkingDir(), "pull-secret.txt")
}
