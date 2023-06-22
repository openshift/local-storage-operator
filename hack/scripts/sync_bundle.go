package main

import (
	"bytes"
	"flag"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	"k8s.io/klog/v2"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
)

const (
	defaultOperatorImage       = "quay.io/openshift/origin-local-storage-operator:latest"
	defaultDiskmakerImage      = "quay.io/openshift/origin-local-storage-diskmaker:latest"
	defaultOperatorDockerfile  = "./Dockerfile"
	defaultDiskmakerDockerfile = "./Dockerfile.diskmaker.rhel7"
	defaultBundleDockerfile    = "./bundle.Dockerfile"
	packageFile                = "./config/manifests/local-storage-operator.package.yaml"
	cliPackagesUrl             = "https://mirror.openshift.com/pub/openshift-v4/x86_64/clients/ocp/latest/"
	defaultFailedCode          = 1
)

// HELPER FUNCTIONS - START

func getClusterServiceVersionFilePath(channel string) string {
	return path.Join("./config/manifests", channel, "local-storage-operator.clusterserviceversion.yaml")
}

func loadYaml(fileName string) map[string]interface{} {
	klog.V(4).Infof("Loading YAML file: %s", fileName)
	yamlFile, err := os.ReadFile(fileName)
	if err != nil {
		klog.Fatalf("Error reading YAML file: %s\n", err)
	}

	var data map[string]interface{}
	err = yaml.Unmarshal(yamlFile, &data)
	if err != nil {
		klog.Errorf("Error parsing YAML file: %s\n", err)
	}

	klog.V(6).Infof("Original file : %s", data)

	return data
}

func saveYaml(data map[string]interface{}, fileName string) {
	klog.V(4).Infof("Saving YAML file: %s", fileName)
	output, err := yaml.Marshal(data)
	if err != nil {
		klog.Fatalf("Error marshalling YAML: %s\n", err)
	}
	os.WriteFile(fileName, output, 0644)
	klog.V(8).Infof("Modified YAML file: %s", data)
}

func executeCommand(dir, command string, args ...string) (stdout string, stderr string, exitCode int) {
	klog.V(4).Infof("Executing command: %s %s", command, strings.Join(args, " "))
	var outbuf, errbuf bytes.Buffer
	cmd := exec.Command(command, args...)

	if dir != "" {
		cmd.Dir = dir
	}

	cmd.Stdout = &outbuf
	cmd.Stderr = &errbuf

	err := cmd.Run()
	stdout = outbuf.String()
	stderr = errbuf.String()

	if err != nil {
		// try to get the exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			ws := exitError.Sys().(syscall.WaitStatus)
			exitCode = ws.ExitStatus()
		} else {
			// This will happen (in OSX) if `name` is not available in $PATH,
			// in this situation, exit code could not be get, and stderr will be
			// empty string very likely, so we use the default fail code, and format err
			// to string and set to stderr
			klog.Errorf("Could not get exit code for failed program: %v, %v", command, args)
			exitCode = defaultFailedCode
			if stderr == "" {
				stderr = err.Error()
			}
		}
	} else {
		// success, exitCode should be 0 if go is ok
		ws := cmd.ProcessState.Sys().(syscall.WaitStatus)
		exitCode = ws.ExitStatus()
	}
	klog.V(4).Infof("Command result:\n\tstdout: %v\n\tstderr: %v\n\texitCode: %v\n", stdout, stderr, exitCode)
	return
}

// Execute a command and panic if rc is not 0
func mustExecuteCommand(dir, command string, args ...string) {
	_, _, rc := executeCommand(dir, command, args...)
	if rc != 0 {
		panic("Command failed")
	}
	return
}

// HELPER FUNCTIONS - END

func buildAndPushImage(imageName, dockerfilePath string) {
	klog.V(4).Infof("Building image: %s", imageName)
	mustExecuteCommand("", "docker", "build", "-t", imageName, "-f", dockerfilePath, ".")
	klog.V(4).Infof("Pushing image: %s", imageName)
	mustExecuteCommand("", "docker", "push", imageName)
}

func createAndPushBundle(bundleImageName, indexImageName string) {
	mustExecuteCommand("./config", "docker", "build", "-t", bundleImageName, "-f", defaultBundleDockerfile, ".")
	mustExecuteCommand("", "docker", "push", bundleImageName)
	mustExecuteCommand("", "opm", "index", "add", "--bundles", bundleImageName, "--tag", indexImageName, "--container-tool", "docker")
	mustExecuteCommand("", "docker", "push", indexImageName)
}

// isOpmInstalled that checks if opm command line tool is installed on the system
func isOpmInstalled() bool {
	out, _, rc := executeCommand("", "opm", "version")
	klog.V(4).Infof("opm version: %s", out)
	return rc == 0
}

func getChannel(packageFile string) string {
	data := loadYaml(packageFile)
	klog.V(4).Infof("Looking for channel in YAML file: %s", packageFile)
	channel, ok := data["channels"].([]interface{})[0].(map[string]interface{})["name"]
	if !ok {
		klog.Fatalf("Error getting channel from YAML file: %s\n", packageFile)
	}
	klog.V(4).Infof("Found channel: %s", channel.(string))

	return channel.(string)
}

func updateCsv(csvFile, diskmakerImage, operatorImage string) map[string]interface{} {
	data := loadYaml(csvFile)
	var updated bool
	klog.V(4).Infof("Updating CSV file: %s", csvFile)
	updated = true

	if diskmakerImage != defaultDiskmakerImage && diskmakerImage != "" {
		klog.V(4).Infof("Updating diskmaker image to: %s", diskmakerImage)
		envs := data["spec"].(map[string]any)["install"].(map[string]any)["spec"].(map[string]any)["deployments"].([]any)[0].(map[string]any)["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["env"].([]any)
		for i, env := range envs {
			if env.(map[string]any)["name"] == "DISKMAKER_IMAGE" {
				envs[i] = map[string]any{
					"name":  "DISKMAKER_IMAGE",
					"value": diskmakerImage,
				}
			}
		}
		updated = true
	}

	if operatorImage != defaultOperatorImage && operatorImage != "" {
		klog.V(4).Infof("Updating operator image to: %s", operatorImage)
		data["spec"].(map[string]any)["install"].(map[string]any)["spec"].(map[string]any)["deployments"].([]any)[0].(map[string]any)["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["image"] = operatorImage
		updated = true
	}

	if updated {
		saveYaml(data, csvFile)
	}

	return data
}

func main() {
	klog.InitFlags(nil)
	defer klog.Flush()

	// Bundle and index image names must be paired in order to build a bundle.
	pflag.String("bundle_image", "", "set image name for bundle")
	pflag.String("index_image", "", "set image name for index")

	pflag.String("diskmaker_image", "", "set image name for diskmaker")
	pflag.String("operator_image", "", "set image name for operator")

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	viper.BindPFlags(pflag.CommandLine)
	viper.SetEnvPrefix("HACK")
	viper.AutomaticEnv()

	// Print useful information about the environment if debugging.
	klog.V(4).Infof("Viper configuration: %v", viper.AllSettings())
	klog.V(4).Infof("Environment variables are set to: %v", os.Environ())

	// Handle flags for bundle and index images.
	bundleImage := viper.GetString("bundle_image")
	indexImage := viper.GetString("index_image")
	if bundleImage != "" && indexImage == "" {
		klog.Fatalf("Bundle image name is set, but index image name is not - can not build bundle without index")
	}
	if indexImage != "" && bundleImage == "" {
		klog.Fatalf("Index image name is set, but bundle image name is not - can not build bundle without bundle")
	}

	if bundleImage != "" && indexImage != "" {
		//Check if opm is installed - it is required for generating bundle image.
		if !isOpmInstalled() {
			klog.Fatalf("opm command not found, please install opm\nYou can download it here: %s", cliPackagesUrl)
		}
		createAndPushBundle(bundleImage, indexImage)
	}

	diskmakerImage := viper.GetString("diskmaker_image")
	operatorImage := viper.GetString("operator_image")
	csvFilePath := getClusterServiceVersionFilePath(getChannel(packageFile))

	if diskmakerImage != defaultDiskmakerImage && diskmakerImage != "" {
		buildAndPushImage(diskmakerImage, defaultDiskmakerDockerfile)
		klog.V(4).Infof("Diskmaker flag is set, value: %s", diskmakerImage)
	}

	if operatorImage != defaultOperatorImage && operatorImage != "" {
		buildAndPushImage(operatorImage, defaultOperatorDockerfile)
		klog.V(4).Infof("Operator flag is set, value: %s", operatorImage)
	}

	updateCsv(csvFilePath, diskmakerImage, operatorImage)

}
