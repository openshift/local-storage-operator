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

func main() {
	rootCmd.AddCommand(lvDaemonCmd)
	rootCmd.AddCommand(managerCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
