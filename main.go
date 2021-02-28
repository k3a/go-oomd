package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

var pageSize = os.Getpagesize()
var pidsArray = make([]int, 0, 1024)
var fields [8]string
var processBlacklist = map[string]struct{}{ //NOTE: \n at the end is required!
	"plasmashell\n": {},
	"Xorg\n":        {},
	"systemd\n":     {},
}

func pids() ([]int, error) {
	f, err := os.Open("/proc")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	pids := pidsArray[:0]
	for {
		names, err := f.Readdirnames(1024)
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		for _, pidStr := range names {
			f, err := os.Open("/proc/" + pidStr + "/comm")
			if err == nil {
				bts, err := ioutil.ReadAll(f)
				f.Close()
				if err == nil {
					if _, blacklisted := processBlacklist[string(bts)]; !blacklisted {
						pid, err := strconv.Atoi(pidStr)
						if err == nil {
							pids = append(pids, pid)
						}
					}
				}
			}
		}
	}

	return pids, nil
}

func getMemStat(pid int) (rss int, err error) {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	s.Split(bufio.ScanWords)

	n := 0
	for s.Scan() {
		if n >= 128 {
			break
		}

		fields[n] = s.Text()
		n++
	}

	if n < 7 {
		return 0, fmt.Errorf("unexpected end of /proc/PID/statm file")
	}

	rssPages, _ := strconv.Atoi(fields[1])

	return rssPages * pageSize, nil
}

// checks error and exists on error
func chkErrExit(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL ERROR: %v\n", err)
		os.Exit(2)
	}
}

// checks if no error (false on error)
func chkErr(err error) bool {
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		return false
	}

	return true
}

func killMostMemoryHungryProcess() {
	pids, err := pids()
	if !chkErr(err) {
		return
	}

	pidWithHighestRSS := 0
	highestRSS := 0
	for _, pid := range pids {
		rss, err := getMemStat(pid)
		if chkErr(err) {
			if rss > highestRSS {
				highestRSS = rss
				pidWithHighestRSS = pid
			}
		}
	}

	if pidWithHighestRSS != 0 {
		fmt.Printf("Killing PID %d with highest RSS %d bytes\n", pidWithHighestRSS, highestRSS)

		err := syscall.Kill(pidWithHighestRSS, syscall.SIGTERM)
		chkErr(err)
	}
}

func main() {
	var statusBuf [128]byte
	reAvg10 := regexp.MustCompile(`some avg10=(\S+) `)

	f, err := os.Open("/proc/pressure/memory")
	chkErrExit(err)
	defer f.Close()

	fmt.Fprintf(os.Stdout, "K3A OOMd Started\n")

	for {
		_, err := f.Seek(0, io.SeekStart)
		chkErrExit(err)

		n, err := f.Read(statusBuf[:])
		if err == nil {
			byts := statusBuf[:n]

			m := reAvg10.FindSubmatch(byts)
			if len(m) >= 2 {
				percent, err := strconv.ParseFloat(string(m[1]), 32)
				chkErrExit(err)

				if percent >= 80 {
					killMostMemoryHungryProcess()
				}
			} else {
				chkErrExit(fmt.Errorf("unexpected format of /proc/pressure/memory"))
			}
		}
		time.Sleep(1 * time.Second)
	}
}
