package pki

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/openshift/hypershift-toolkit/pkg/config"
	"github.com/openshift/hypershift-toolkit/pkg/pki"
)

func NewPKICommand() *cobra.Command {
	var outputDir, configFile string
	cmd := &cobra.Command{
		Use:   "pki",
		Short: "Generates PKI artifacts given an output directory",
		Run: func(cmd *cobra.Command, args []string) {
			if err := ensureDir(outputDir); err != nil {
				fmt.Fprintf(os.Stderr, "Cannot create output directory: %v\n", err)
				os.Exit(1)
			}

			params, err := config.ReadFrom(configFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Cannot read config file: %v\n", err)
				os.Exit(1)
			}

			if err := pki.GeneratePKI(params, outputDir); err != nil {
				fmt.Fprintf(os.Stderr, "Error generating PKI: %s\n", err)
				os.Exit(1)
			}
		},
	}
	cmd.Flags().StringVar(&outputDir, "output-dir", defaultOutputDir(), "Specify the directory where PKI artifacts should be output")
	cmd.Flags().StringVar(&configFile, "config", defaultConfigFile(), "Specify the config file for this cluster")
	return cmd
}

func defaultOutputDir() string {
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

func ensureDir(dirName string) error {
	return os.MkdirAll(dirName, 0755)
}
