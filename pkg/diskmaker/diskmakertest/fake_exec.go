package diskmakertest

import (
	"fmt"

	utilexec "k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"
)

const fakeExecScriptRepeats = 16

// BlkidAlwaysFakeExec returns a FakeExec where every invocation behaves like blkid returning
// output (and blkidErr) regardless of arguments. Matches sibling-fallback style stubs.
func BlkidAlwaysFakeExec(output string, blkidErr error) *testingexec.FakeExec {
	action := func(cmd string, args ...string) utilexec.Cmd {
		return &testingexec.FakeCmd{
			CombinedOutputScript: []testingexec.FakeAction{
				func() ([]byte, []byte, error) {
					return []byte(output), nil, blkidErr
				},
			},
		}
	}
	return repeatedFakeExec(action)
}

// BlkidForDevicePathFakeExec returns a FakeExec where blkid emits output only when the last
// argument equals devicePath (same semantics as the former lv fakeBlkidExecutor).
func BlkidForDevicePathFakeExec(devicePath, outputWhenMatch string) *testingexec.FakeExec {
	action := func(cmd string, args ...string) utilexec.Cmd {
		out := ""
		if cmd == "blkid" && len(args) > 0 && args[len(args)-1] == devicePath {
			out = outputWhenMatch
		}
		return &testingexec.FakeCmd{
			CombinedOutputScript: []testingexec.FakeAction{
				func() ([]byte, []byte, error) {
					return []byte(out), nil, nil
				},
			},
		}
	}
	return repeatedFakeExec(action)
}

// FindAndBlkidFakeExec returns a FakeExec that handles find and blkid like the former
// lvset fakeCommandExecutor (other commands error).
func FindAndBlkidFakeExec(findOutput, blkidOutput string, blkidErr error) *testingexec.FakeExec {
	action := func(cmd string, args ...string) utilexec.Cmd {
		switch cmd {
		case "find":
			return &testingexec.FakeCmd{
				CombinedOutputScript: []testingexec.FakeAction{
					func() ([]byte, []byte, error) {
						return []byte(findOutput), nil, nil
					},
				},
			}
		case "blkid":
			return &testingexec.FakeCmd{
				CombinedOutputScript: []testingexec.FakeAction{
					func() ([]byte, []byte, error) {
						return []byte(blkidOutput), nil, blkidErr
					},
				},
			}
		default:
			return &testingexec.FakeCmd{
				CombinedOutputScript: []testingexec.FakeAction{
					func() ([]byte, []byte, error) {
						return nil, nil, fmt.Errorf("unexpected command %s", cmd)
					},
				},
			}
		}
	}
	return repeatedFakeExec(action)
}

func repeatedFakeExec(action testingexec.FakeCommandAction) *testingexec.FakeExec {
	commandScript := make([]testingexec.FakeCommandAction, 0, fakeExecScriptRepeats)
	for i := 0; i < fakeExecScriptRepeats; i++ {
		commandScript = append(commandScript, action)
	}
	return &testingexec.FakeExec{
		CommandScript: commandScript,
	}
}
