package test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"context"

	"github.com/mitchellh/go-homedir"
	"github.com/rogpeppe/go-internal/modfile"
	log "github.com/sirupsen/logrus"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	GoModEnv  = "GO111MODULE"
	goModFile = "go.mod"
	GoPathEnv = "GOPATH"
	SrcDir    = "src"
)

var validVendorCmds = map[string]struct{}{
	"build":   {},
	"clean":   {},
	"get":     {},
	"install": {},
	"list":    {},
	"run":     {},
	"test":    {},
}

func getHomeDir() (string, error) {
	hd, err := homedir.Dir()
	if err != nil {
		return "", err
	}
	return homedir.Expand(hd)
}

func ExecCmd(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	log.Debugf("Running %#v", cmd.Args)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to exec %#v: %v", cmd.Args, err)
	}
	return nil
}

func MustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: (%v)", err)
	}
	return wd
}

// GoCmdOptions is the base option set for "go" subcommands.
type GoCmdOptions struct {
	// BinName is the name of the compiled binary, passed to -o.
	BinName string
	// Args are args passed to "go {cmd}", aside from "-o {bin_name}" and
	// test binary args.
	// These apply to build, clean, get, install, list, run, and test.
	Args []string
	// PackagePath is the path to the main (go build) or test (go test) packages.
	PackagePath string
	// Env is a list of environment variables to pass to the cmd;
	// exec.Command.Env is set to this value.
	Env []string
	// Dir is the dir to run "go {cmd}" in; exec.Command.Dir is set to this value.
	Dir string
}

// GoBuild runs "go build" configured with opts.
func GoBuild(opts GoCmdOptions) error {
	return GoCmd("build", opts)
}

// GoCmd runs "go {cmd}".
func GoCmd(cmd string, opts GoCmdOptions) error {
	bargs, err := opts.getGeneralArgsWithCmd(cmd)
	if err != nil {
		return err
	}
	c := exec.Command("go", bargs...)
	opts.setCmdFields(c)
	return ExecCmd(c)
}

// TODO(hasbro17): If this function is called in the subdir of
// a module project it will fail to parse go.mod and return
// the correct import path.
// This needs to be fixed to return the pkg import path for any subdir
// in order for `generate csv` to correctly form pkg imports
// for API pkg paths that are not relative to the root dir.
// This might not be fixable since there is no good way to
// get the project root from inside the subdir of a module project.
//
// GetGoPkg returns the current directory's import path by parsing it from
// wd if this project's repository path is rooted under $GOPATH/src, or
// from go.mod the project uses Go modules to manage dependencies.
// If the project has a go.mod then wd must be the project root.
//
// Example: "github.com/example-inc/app-operator"
func GetGoPkg() string {
	// Default to reading from go.mod, as it should usually have the (correct)
	// package path, and no further processing need be done on it if so.
	if _, err := os.Stat(goModFile); err != nil && !os.IsNotExist(err) {
		log.Fatalf("Failed to read go.mod: %v", err)
	} else if err == nil {
		b, err := os.ReadFile(goModFile)
		if err != nil {
			log.Fatalf("Read go.mod: %v", err)
		}
		mf, err := modfile.Parse(goModFile, b, nil)
		if err != nil {
			log.Fatalf("Parse go.mod: %v", err)
		}
		if mf.Module != nil && mf.Module.Mod.Path != "" {
			return mf.Module.Mod.Path
		}
	}

	// Then try parsing package path from $GOPATH (set env or default).
	goPath, ok := os.LookupEnv(GoPathEnv)
	if !ok || goPath == "" {
		hd, err := getHomeDir()
		if err != nil {
			log.Fatal(err)
		}
		goPath = filepath.Join(hd, "go", "src")
	} else {
		// MustSetWdGopath is necessary here because the user has set GOPATH,
		// which could be a path list.
		goPath = MustSetWdGopath(goPath)
	}
	if !strings.HasPrefix(MustGetwd(), goPath) {
		log.Fatal("Could not determine project repository path: $GOPATH not set, wd in default $HOME/go/src," +
			" or wd does not contain a go.mod")
	}
	return parseGoPkg(goPath)
}

// MustSetWdGopath sets GOPATH to the first element of the path list in
// currentGopath that prefixes the wd, then returns the set path.
// If GOPATH cannot be set, MustSetWdGopath exits.
func MustSetWdGopath(currentGopath string) string {
	var (
		newGopath   string
		cwdInGopath bool
		wd          = MustGetwd()
	)
	for _, newGopath = range filepath.SplitList(currentGopath) {
		if strings.HasPrefix(filepath.Dir(wd), newGopath) {
			cwdInGopath = true
			break
		}
	}
	if !cwdInGopath {
		log.Fatalf("Project not in $GOPATH")
	}
	if err := os.Setenv(GoPathEnv, newGopath); err != nil {
		log.Fatal(err)
	}
	return newGopath
}

func parseGoPkg(gopath string) string {
	goSrc := filepath.Join(gopath, SrcDir)
	wd := MustGetwd()
	pathedPkg := strings.Replace(wd, goSrc, "", 1)
	// Make sure package only contains the "/" separator and no others, and
	// trim any leading/trailing "/".
	return strings.Trim(filepath.ToSlash(pathedPkg), "/")
}

func (opts GoCmdOptions) getGeneralArgsWithCmd(cmd string) ([]string, error) {
	// Go subcommands with more than one child command must be passed as
	// multiple arguments instead of a spaced string, ex. "go mod init".
	bargs := []string{}
	for _, c := range strings.Split(cmd, " ") {
		if ct := strings.TrimSpace(c); ct != "" {
			bargs = append(bargs, ct)
		}
	}
	if len(bargs) == 0 {
		return nil, fmt.Errorf("the go binary cannot be run without subcommands")
	}

	if opts.BinName != "" {
		bargs = append(bargs, "-o", opts.BinName)
	}
	bargs = append(bargs, opts.Args...)

	if goModOn, err := GoModOn(); err != nil {
		return nil, err
	} else if goModOn {
		// Does vendor exist?
		info, err := os.Stat("vendor")
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		// Does the first "go" subcommand accept -mod=vendor?
		_, ok := validVendorCmds[bargs[0]]
		// TODO: remove needsModVendor when
		// https://github.com/golang/go/issues/32471 is resolved.
		if err == nil && info.IsDir() && ok && needsModVendor() {
			bargs = append(bargs, "-mod=vendor")
		}
	}

	if opts.PackagePath != "" {
		bargs = append(bargs, opts.PackagePath)
	}
	return bargs, nil
}

// needsModVendor resolves https://github.com/golang/go/issues/32471,
// where any flags in GOFLAGS that are also set in the CLI are
// duplicated, causing 'go' invocation errors.
// TODO: remove once the issue is resolved.
func needsModVendor() bool {
	return !strings.Contains(os.Getenv("GOFLAGS"), "-mod=vendor")
}

func (opts GoCmdOptions) setCmdFields(c *exec.Cmd) {
	c.Env = append(c.Env, os.Environ()...)
	if len(opts.Env) != 0 {
		c.Env = append(c.Env, opts.Env...)
	}
	if opts.Dir != "" {
		c.Dir = opts.Dir
	}
}

// From https://github.com/golang/go/wiki/Modules:
//
//		You can activate module support in one of two ways:
//		- Invoke the go command in a directory with a valid go.mod file in the
//	     current directory or any parent of it and the environment variable
//	     GO111MODULE unset (or explicitly set to auto).
//		- Invoke the go command with GO111MODULE=on environment variable set.
//
// GoModOn returns true if Go modules are on in one of the above two ways.
func GoModOn() (bool, error) {
	v, ok := os.LookupEnv(GoModEnv)
	if !ok {
		return true, nil
	}
	switch v {
	case "", "auto", "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("unknown environment setting GO111MODULE=%s", v)
	}
}

// Scanner scans a yaml manifest file for manifest tokens delimited by "---".
// See bufio.Scanner for semantics.
type Scanner struct {
	reader  *k8syaml.YAMLReader
	token   []byte // Last token returned by split.
	err     error  // Sticky error.
	empties int    // Count of successive empty tokens.
	done    bool   // Scan has finished.
}

const maxExecutiveEmpties = 100

func NewYAMLScanner(r io.Reader) *Scanner {
	return &Scanner{reader: k8syaml.NewYAMLReader(bufio.NewReader(r))}
}

func (s *Scanner) Scan() bool {
	if s.done {
		return false
	}

	var (
		tok []byte
		err error
	)

	for {
		tok, err = s.reader.Read()
		if err != nil {
			if err == io.EOF {
				s.done = true
			}
			s.err = err
			return false
		}
		if len(bytes.TrimSpace(tok)) == 0 {
			s.empties++
			if s.empties > maxExecutiveEmpties {
				panic("yaml.Scan: too many empty tokens without progressing")
			}
			continue
		}
		s.empties = 0
		s.token = tok
		return true
	}
}

func (s *Scanner) Bytes() []byte {
	return s.token
}

func (s *Scanner) Err() error {
	if s.err == io.EOF {
		return nil
	}
	return s.err
}

type ApiUtil struct {
	Client client.Client
}

// Create PersistentVolume object
func (a ApiUtil) CreatePV(pv *corev1.PersistentVolume) (*corev1.PersistentVolume, error) {
	return pv, a.Client.Create(context.TODO(), pv)
}

// Delete PersistentVolume object
func (a ApiUtil) DeletePV(pvName string) error {
	pv := &corev1.PersistentVolume{}
	err := a.Client.Get(context.TODO(), types.NamespacedName{Name: pvName}, pv)
	if kerrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = a.Client.Delete(context.TODO(), pv)
	return err
}

// CreateJob Creates a Job execution.
func (a ApiUtil) CreateJob(job *batchv1.Job) error {
	return a.Client.Create(context.TODO(), job)
}

// DeleteJob deletes specified Job by its name and namespace.
func (a ApiUtil) DeleteJob(jobName string, namespace string) error {
	job := &batchv1.Job{}
	err := a.Client.Get(context.TODO(), types.NamespacedName{Name: jobName}, job)
	if kerrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}
	err = a.Client.Delete(context.TODO(), job)
	return err
}
