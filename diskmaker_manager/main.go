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
	Short: "Used to start the controller-runtime manager that owns the LocalVolume controller",
	RunE:  startManager,
}
var discoveryDaemonCmd = &cobra.Command{
	Use:   "discover",
	Short: "Used to start device discovery for the LocalVolumeDiscovery CR",
	RunE:  startDeviceDiscovery,
}

func main() {
	rootCmd.AddCommand(lvDaemonCmd)
	rootCmd.AddCommand(managerCmd)
	rootCmd.AddCommand(discoveryDaemonCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
