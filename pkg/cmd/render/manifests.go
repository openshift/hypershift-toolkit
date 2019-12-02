package render

import (
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift-toolkit/pkg/cmd/util"
	"github.com/openshift/hypershift-toolkit/pkg/config"
	"github.com/openshift/hypershift-toolkit/pkg/render"
)

type RenderManifestsOptions struct {
	OutputDir      string
	ConfigFile     string
	PullSecretFile string
	PKIDir         string

	IncludeSecrets      bool
	IncludeEtcd         bool
	IncludeAutoApprover bool
	IncludeVPN          bool
}

func NewRenderManifestsCommand() *cobra.Command {
	opt := &RenderManifestsOptions{}
	cmd := &cobra.Command{
		Use: "render",
		Run: func(cmd *cobra.Command, args []string) {
			if err := opt.Run(); err != nil {
				log.WithError(err).Fatal("Error occurred rendering manifests")
			}
		},
	}
	cmd.Flags().StringVar(&opt.OutputDir, "output-dir", defaultManifestsDir(), "Specify the directory where manifest files should be output")
	cmd.Flags().StringVar(&opt.ConfigFile, "config", defaultConfigFile(), "Specify the config file for this cluster")
	cmd.Flags().StringVar(&opt.PullSecretFile, "pull-secret", defaultPullSecretFile(), "Specify the config file for this cluster")
	cmd.Flags().StringVar(&opt.PKIDir, "pki-dir", defaultPKIDir(), "Specify the directory where the input PKI files have been placed")
	cmd.Flags().BoolVar(&opt.IncludeSecrets, "include-secrets", false, "If true, PKI secrets will be included in rendered manifests")
	cmd.Flags().BoolVar(&opt.IncludeEtcd, "include-etcd", false, "If true, Etcd manifests will be included in rendered manifests")
	cmd.Flags().BoolVar(&opt.IncludeAutoApprover, "include-autoapprover", false, "If true, includes a simple autoapprover pod in manifests")
	cmd.Flags().BoolVar(&opt.IncludeVPN, "include-vpn", false, "If true, includes a VPN server, sidecar and client")
	return cmd
}

func (o *RenderManifestsOptions) Run() error {
	util.EnsureDir(o.OutputDir)
	params, err := config.ReadFrom(o.ConfigFile)
	if err != nil {
		log.WithError(err).Fatalf("Error occurred reading configuration")
	}
	externalOauth := params.ExternalOauthPort != 0
	err = render.RenderClusterManifests(params, o.PullSecretFile, o.OutputDir, o.IncludeEtcd, o.IncludeAutoApprover, o.IncludeVPN, externalOauth)
	if err != nil {
		return err
	}
	if o.IncludeSecrets {
		render.RenderPKISecrets(o.PKIDir, o.OutputDir, o.IncludeEtcd, o.IncludeVPN, externalOauth)
	}
	return nil
}

func defaultManifestsDir() string {
	return filepath.Join(util.WorkingDir(), "manifests")
}

func defaultConfigFile() string {
	return filepath.Join(util.WorkingDir(), "cluster.yaml")
}

func defaultPullSecretFile() string {
	return filepath.Join(util.WorkingDir(), "pull-secret.txt")
}

func defaultPKIDir() string {
	return filepath.Join(util.WorkingDir(), "pki")
}
