//go:build !windows

// Copyright 2015 - 2017 Ka-Hing Cheung
// Copyright 2021 Yandex LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	daemon "github.com/sevlyar/go-daemon"

	"github.com/yandex-cloud/geesefs/core"
	"github.com/yandex-cloud/geesefs/core/cfg"
)

// Where Go runtime crash output goes once the daemon has closed its stderr.
// Overridable for non-root mounts via GEESEFS_CRASH_LOG.
const defaultCrashLogPath = "/var/log/geesefs-crash.log"

// setupCrashLog routes runtime crash output (panics, fatal errors, deadlock
// dumps) to a shared append-only file. These bypass the logger and write to
// fd 2, which the daemon closes right after mounting, so without this a
// crashed daemon leaves no trace anywhere. The header line ties the pid to
// its mount so a following dump can be attributed.
func setupCrashLog(bucketName string, mountPoint string) {
	path := os.Getenv("GEESEFS_CRASH_LOG")
	if path == "" {
		path = defaultCrashLogPath
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		log.Warnf("Cannot open crash log %v: %v; runtime panics will be lost", path, err)
		return
	}
	fmt.Fprintf(f, "--- %s geesefs pid %d serving %s at %s: crash output armed ---\n",
		time.Now().UTC().Format(time.RFC3339), os.Getpid(), bucketName, mountPoint)
	if err = debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		log.Warnf("Cannot set crash output to %v: %v", path, err)
	}
	// SetCrashOutput keeps its own duplicate of the descriptor.
	f.Close()
}

var signalsToHandle = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGUSR1}

func isSigUsr1(s os.Signal) bool {
	return s == syscall.SIGUSR1
}

func kill(pid int, s os.Signal) (err error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	defer p.Release()

	err = p.Signal(s)
	if err != nil {
		return err
	}
	return
}

const canDaemonize = true

type Daemonizer struct {
	result os.Signal
	wg     sync.WaitGroup
}

func NewDaemonizer() *Daemonizer {
	p := &Daemonizer{}

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGUSR1, syscall.SIGUSR2)

	p.wg.Add(1)
	go func() {
		p.result = <-signalChan
		p.wg.Done()
	}()

	return p
}

func (p *Daemonizer) Daemonize(logFile string) error {
	messageArg0()

	ctx := new(daemon.Context)
	if logFile == "stderr" || logFile == "/dev/stderr" {
		ctx.LogFileName = "/dev/stderr"
	}
	child, err := ctx.Reborn()

	if err != nil {
		panic(fmt.Sprintf("unable to daemonize: %v", err))
	}

	if child != nil {
		// attempt to wait for child to notify parent
		if p.Wait() {
			os.Exit(0)
		} else {
			return syscall.EINVAL
		}
	} else {
		p.Cancel()
		defer ctx.Release()
	}

	return nil
}

func (p *Daemonizer) Cancel() {
	// kill our own waiting goroutine
	kill(os.Getpid(), syscall.SIGUSR1)
	p.wg.Wait()
}

func (p *Daemonizer) Wait() bool {
	p.wg.Wait()
	return p.result == syscall.SIGUSR1
}

func (p *Daemonizer) NotifySuccess(success bool) {
	sig := syscall.SIGUSR1
	if !success {
		sig = syscall.SIGUSR2
	}
	kill(os.Getppid(), sig)
}

// Mount the file system based on the supplied arguments, returning a
// MountedFS that can be joined to wait for unmounting.
func mount(
	ctx context.Context,
	bucketName string,
	flags *cfg.FlagStorage) (fs *core.Goofys, mfs core.MountedFS, err error) {
	if flags.ClusterMode {
		return core.MountCluster(ctx, bucketName, flags)
	} else {
		return core.MountFuse(ctx, bucketName, flags)
	}
}

func messagePath() {
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PATH=") {
			return
		}
	}

	// mount -a seems to run goofys without PATH
	// usually fusermount is in /bin
	os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
}

func messageArg0() {
	var err error
	os.Args[0], err = os.Executable()
	if err != nil {
		panic(fmt.Sprintf("Unable to discover current executable: %v", err))
	}
}

func setuid(uid int) error {
	return syscall.Setuid(uid)
}

func setgid(gid int) error {
	return syscall.Setgid(gid)
}
