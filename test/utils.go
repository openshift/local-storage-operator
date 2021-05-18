package test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	log "github.com/sirupsen/logrus"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

const GoModEnv = "GO111MODULE"

var validVendorCmds = map[string]struct{}{
	"build":   struct{}{},
	"clean":   struct{}{},
	"get":     struct{}{},
	"install": struct{}{},
	"list":    struct{}{},
	"run":     struct{}{},
	"test":    struct{}{},
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
//	You can activate module support in one of two ways:
//	- Invoke the go command in a directory with a valid go.mod file in the
//      current directory or any parent of it and the environment variable
//      GO111MODULE unset (or explicitly set to auto).
//	- Invoke the go command with GO111MODULE=on environment variable set.
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

func NewYAMLScanner(b []byte) *Scanner {
	r := bufio.NewReader(bytes.NewBuffer(b))
	return &Scanner{reader: k8syaml.NewYAMLReader(r)}
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
