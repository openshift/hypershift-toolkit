package main

import (
	"github.com/spf13/cobra"

	"github.com/openshift/hypershift-toolkit/pkg/cmd"
)

func main() {
	rootCmd := newHypershiftCommand()
	rootCmd.AddCommand(cmd.NewPKICommand())
	rootCmd.Execute()
}

func newHypershiftCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "hypershift",
		Short: "Hypershift is a toolkit that enables running OpenShift 4.x in a hyperscale manner with many control planes hosted on a central management cluster.",
	}
}
