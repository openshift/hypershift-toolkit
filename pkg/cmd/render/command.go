package render

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

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
				fmt.Fprintf(os.Stderr, "Error: %v", err)
				os.Exit(1)
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
	if err := ensureDir(o.OutputDir); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot create output directory: %v\n", err)
		os.Exit(1)
	}
	params, err := config.ReadFrom(o.ConfigFile)
	if err != nil {
		return err
	}
	return render.RenderClusterManifests(params, o.PullSecretFile, o.OutputDir, o.PKIDir)
}

func defaultManifestsDir() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Cannot get current working directory: %v", err)
	}
	return filepath.Join(dir, "manifests")
}

func defaultPKIDir() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Cannot get current working directory: %v", err)
	}
	return filepath.Join(dir, "pki")
}

func defaultConfigFile() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Cannot get current working directory: %v", err)
	}
	return filepath.Join(dir, "cluster.yaml")
}

func defaultPullSecretFile() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Cannot get current working directory: %v", err)
	}
	return filepath.Join(dir, "pull-secret.txt")
}

func ensureDir(dirName string) error {
	return os.MkdirAll(dirName, 0755)
}
