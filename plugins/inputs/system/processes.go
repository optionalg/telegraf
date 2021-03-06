// +build !windows

package system

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
)

type Processes struct {
	execPS       func() ([]byte, error)
	readProcFile func(statFile string) ([]byte, error)

	forcePS   bool
	forceProc bool
}

func (p *Processes) Description() string {
	return "Get the number of processes and group them by status"
}

func (p *Processes) SampleConfig() string { return "" }

func (p *Processes) Gather(acc telegraf.Accumulator) error {
	// Get an empty map of metric fields
	fields := getEmptyFields()

	// Decide if we will use 'ps' to get stats (use procfs otherwise)
	usePS := true
	if runtime.GOOS == "linux" {
		usePS = false
	}
	if p.forcePS {
		usePS = true
	} else if p.forceProc {
		usePS = false
	}

	// Gather stats from 'ps' or procfs
	if usePS {
		if err := p.gatherFromPS(fields); err != nil {
			return err
		}
	} else {
		if err := p.gatherFromProc(fields); err != nil {
			return err
		}
	}

	acc.AddFields("processes", fields, nil)
	return nil
}

// Gets empty fields of metrics based on the OS
func getEmptyFields() map[string]interface{} {
	fields := map[string]interface{}{
		"blocked":  int64(0),
		"zombies":  int64(0),
		"stopped":  int64(0),
		"running":  int64(0),
		"sleeping": int64(0),
		"total":    int64(0),
	}
	switch runtime.GOOS {
	case "freebsd":
		fields["idle"] = int64(0)
		fields["wait"] = int64(0)
	case "darwin":
		fields["idle"] = int64(0)
	case "openbsd":
		fields["idle"] = int64(0)
	case "linux":
		fields["paging"] = int64(0)
		fields["total_threads"] = int64(0)
	}
	return fields
}

// exec `ps` to get all process states
func (p *Processes) gatherFromPS(fields map[string]interface{}) error {
	out, err := p.execPS()
	if err != nil {
		return err
	}

	for i, status := range bytes.Fields(out) {
		if i == 0 && string(status) == "STAT" {
			// This is a header, skip it
			continue
		}
		switch status[0] {
		case 'W':
			fields["wait"] = fields["wait"].(int64) + int64(1)
		case 'U', 'D', 'L':
			// Also known as uninterruptible sleep or disk sleep
			fields["blocked"] = fields["blocked"].(int64) + int64(1)
		case 'Z':
			fields["zombies"] = fields["zombies"].(int64) + int64(1)
		case 'T':
			fields["stopped"] = fields["stopped"].(int64) + int64(1)
		case 'R':
			fields["running"] = fields["running"].(int64) + int64(1)
		case 'S':
			fields["sleeping"] = fields["sleeping"].(int64) + int64(1)
		case 'I':
			fields["idle"] = fields["idle"].(int64) + int64(1)
		default:
			log.Printf("processes: Unknown state [ %s ] from ps",
				string(status[0]))
		}
		fields["total"] = fields["total"].(int64) + int64(1)
	}
	return nil
}

// get process states from /proc/(pid)/stat files
func (p *Processes) gatherFromProc(fields map[string]interface{}) error {
	files, err := ioutil.ReadDir("/proc")
	if err != nil {
		return err
	}

	for _, file := range files {
		if !file.IsDir() {
			continue
		}

		statFile := path.Join("/proc", file.Name(), "stat")
		data, err := p.readProcFile(statFile)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}

		stats := bytes.Fields(data)
		if len(stats) < 3 {
			return fmt.Errorf("Something is terribly wrong with %s", statFile)
		}
		switch stats[2][0] {
		case 'R':
			fields["running"] = fields["running"].(int64) + int64(1)
		case 'S':
			fields["sleeping"] = fields["sleeping"].(int64) + int64(1)
		case 'D':
			fields["blocked"] = fields["blocked"].(int64) + int64(1)
		case 'Z':
			fields["zombies"] = fields["zombies"].(int64) + int64(1)
		case 'T', 't':
			fields["stopped"] = fields["stopped"].(int64) + int64(1)
		case 'W':
			fields["paging"] = fields["paging"].(int64) + int64(1)
		default:
			log.Printf("processes: Unknown state [ %s ] in file %s",
				string(stats[2][0]), statFile)
		}
		fields["total"] = fields["total"].(int64) + int64(1)

		threads, err := strconv.Atoi(string(stats[19]))
		if err != nil {
			log.Printf("processes: Error parsing thread count: %s", err)
			continue
		}
		fields["total_threads"] = fields["total_threads"].(int64) + int64(threads)
	}
	return nil
}

func readProcFile(statFile string) ([]byte, error) {
	if _, err := os.Stat(statFile); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	data, err := ioutil.ReadFile(statFile)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func execPS() ([]byte, error) {
	bin, err := exec.LookPath("ps")
	if err != nil {
		return nil, err
	}

	out, err := exec.Command(bin, "axo", "state").Output()
	if err != nil {
		return nil, err
	}

	return out, err
}

func init() {
	inputs.Add("processes", func() telegraf.Input {
		return &Processes{
			execPS:       execPS,
			readProcFile: readProcFile,
		}
	})
}
