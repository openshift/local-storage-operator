package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "diskmaker",
	Short: "Used to start the diskmaker daemon for the local-storage-operator",
}
var managerCmd = &cobra.Command{
	Use:   "lv-manager",
	Short: "Used to start the controller-runtime manager that owns the LocalVolumeSet controller",
	RunE:  startManager,
}
var lvDaemonCmd = &cobra.Command{
	Use:   "lv-controller",
	Short: "Used to start an instance of the controller for a specific LocalVolume CR",
	RunE:  startDiskMaker,
}

func main() {
	rootCmd.AddCommand(lvDaemonCmd)
	rootCmd.AddCommand(managerCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
