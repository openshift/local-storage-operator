package test

import (
	"path/filepath"
)

const (
	// Separator to statically create directories.
	filePathSep = string(filepath.Separator)

	// dirs
	CmdDir         = "cmd"
	ManagerDir     = CmdDir + filePathSep + "manager"
	PkgDir         = "pkg"
	ApisDir        = PkgDir + filePathSep + "apis"
	ControllerDir  = PkgDir + filePathSep + "controller"
	BuildDir       = "build"
	BuildTestDir   = BuildDir + filePathSep + "test-framework"
	BuildBinDir    = BuildDir + filePathSep + "_output" + filePathSep + "bin"
	BuildScriptDir = BuildDir + filePathSep + "bin"
	DeployDir      = "deploy"
	CRDsDir        = DeployDir + filePathSep + "crds"
)
