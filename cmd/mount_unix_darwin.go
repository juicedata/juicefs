//go:build darwin

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"errors"
	"time"

	"github.com/juicedata/juicefs/pkg/fuse"
	"github.com/juicedata/juicefs/pkg/fusemac"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"
)

func mountServe(v *vfs.VFS, options string, enableXattr, enableIoctl bool, c *cli.Context) error {
	backend, err := parseBackend(options)
	if err != nil {
		return err
	}
	if backend == "" {
		// Default: macFUSE kext variant via go-fuse.
		return fuse.Serve(v, options, enableXattr, enableIoctl)
	}
	if err := fusemac.CheckBackend(backend, options); err != nil {
		return err
	}
	err = fusemac.Serve(v, options, enableXattr, enableIoctl, c)
	if errors.Is(err, fusemac.ErrGracefulUnmount) {
		return nil
	}
	return err
}

func fuseShutdown(useFusemac bool) bool {
	if !useFusemac {
		return fuse.Shutdown()
	}
	return fusemac.UnmountActive()
}

func unmountActive(useFusemac bool) {
	if !useFusemac {
		return
	}
	done := make(chan bool, 1)
	go func() { done <- fusemac.UnmountActive() }()
	select {
	case ok := <-done:
		logger.Infof("clean unmount finished: %v", ok)
	case <-time.After(10 * time.Second):
		logger.Warnf("clean unmount did not finish in 10s, falling back to forced umount")
	}
	fusemac.MarkSignalShutdown()
}
