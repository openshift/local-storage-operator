package discovery

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"k8s.io/klog"
)

// Monitors udev for block device changes, and collapses these events such that
// only one event is emitted per period in order to deal with flapping.
func udevBlockMonitor(c chan string, period time.Duration) {
	defer close(c)
	var udevFilter []string

	// return any add or remove events, but none that match device mapper
	// events. string matching is case-insensitive
	events := make(chan string)

	// regex to ignore the list of devices where udev events should not
	// trigger continous discovery
	discoverUdev := "(?i)dm-[0-9]+,(?i)rbd[0-9]+,(?i)nbd[0-9]+"

	udevFilter = strings.Split(discoverUdev, ",")
	klog.Infof("using the regular expressions %q", udevFilter)

	go rawUdevBlockMonitor(events,
		[]string{"(?i)add", "(?i)remove"},
		udevFilter)

	for {
		event, ok := <-events
		if !ok {
			return
		}
		timeout := time.NewTimer(period)
		for {
			select {
			case <-timeout.C:
				break
			case _, ok := <-events:
				if !ok {
					return
				}
				continue
			}
			break
		}
		c <- event
	}
}

// Scans `udevadm monitor` output for block sub-system events. Each line of
// output matching a set of substrings is sent to the provided channel. An event
// is returned if it passes any matches tests, and passes all exclusion tests.
func rawUdevBlockMonitor(c chan string, matches, exclusions []string) {
	defer close(c)

	// stdbuf -oL performs line bufferred output
	cmd := exec.Command("stdbuf", "-oL", "udevadm", "monitor", "-u", "-k", "-s", "block")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		klog.Warningf("Cannot open udevadm stdout: %v", err)
		return
	}

	err = cmd.Start()
	if err != nil {
		klog.Warningf("Cannot start udevadm monitoring: %v", err)
		return
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		text := scanner.Text()
		klog.Infof("udevadm monitor: %s", text)
		match, err := matchUdevEvent(text, matches, exclusions)
		if err != nil {
			klog.Warningf("udevadm filtering failed: %v", err)
			return
		}
		if match {
			c <- text
		}
	}

	if err := scanner.Err(); err != nil {
		klog.Warningf("udevadm monitor scanner error: %v", err)
	}

	klog.Info("udevadm monitor finished")
}

func matchUdevEvent(text string, matches, exclusions []string) (bool, error) {
	for _, match := range matches {
		matched, err := regexp.MatchString(match, text)
		if err != nil {
			return false, fmt.Errorf("failed to search string: %v", err)
		}
		if matched {
			hasExclusion := false
			for _, exclusion := range exclusions {
				matched, err = regexp.MatchString(exclusion, text)
				if err != nil {
					return false, fmt.Errorf("failed to search string: %v", err)
				}
				if matched {
					hasExclusion = true
					break
				}
			}
			if !hasExclusion {
				klog.Infof("udevadm monitor: matched event: %s", text)
				return true, nil
			}
		}
	}
	return false, nil
}
