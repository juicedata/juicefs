/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

package utils

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

// Min returns min of 2 int
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Exists checks if the file/folder in given path exists
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}

// SplitDir splits a path with default path list separator or comma.
func SplitDir(d string) []string {
	dd := strings.Split(d, string(os.PathListSeparator))
	if len(dd) == 1 {
		dd = strings.Split(dd[0], ",")
	}
	return dd
}

// NewDynProgressBar init a dynamic progress bar,the title will appears at the head of the progress bar
func NewDynProgressBar(title string, quiet bool) (*mpb.Progress, *mpb.Bar) {
	if os.Getenv("DISPLAY_PROGRESSBAR") == "false" {
		quiet = true
	}

	var progress *mpb.Progress
	if !quiet && isatty.IsTerminal(os.Stdout.Fd()) {
		progress = mpb.New(mpb.WithWidth(64))
	} else {
		progress = mpb.New(mpb.WithWidth(64), mpb.WithOutput(nil))
	}
	bar := progress.AddBar(0,
		mpb.PrependDecorators(
			decor.Name(title, decor.WCSyncWidth),
			decor.CountersNoUnit("%d / %d"),
		),
		mpb.AppendDecorators(
			decor.OnComplete(decor.Percentage(decor.WC{W: 5}), "done"),
			decor.OnComplete(
				decor.AverageETA(decor.ET_STYLE_GO, decor.WC{W: 6}), "",
			),
		),
	)
	return progress, bar
}

// NewProgressCounter init a progress counter
func NewProgressCounter(title string) (*mpb.Progress, *mpb.Bar) {
	quiet := os.Getenv("DISPLAY_PROGRESSBAR") == "false"
	var progress *mpb.Progress
	if !quiet && isatty.IsTerminal(os.Stdout.Fd()) {
		progress = mpb.New(mpb.WithWidth(64))
	} else {
		progress = mpb.New(mpb.WithWidth(64), mpb.WithOutput(nil))
	}
	bar := progress.Add(0,
		NewSpinner(),
		mpb.PrependDecorators(
			decor.Name(title, decor.WCSyncWidth),
			decor.CurrentNoUnit("%d", decor.WCSyncWidthR),
		),
		mpb.BarFillerClearOnComplete(),
	)
	return progress, bar
}

func NewSpinner() mpb.BarFiller {
	spinnerStyle := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for i, s := range spinnerStyle {
		spinnerStyle[i] = "\033[1;32m" + s + "\033[0m"
	}
	return mpb.NewBarFiller(mpb.SpinnerStyle(spinnerStyle...))
}

// GetLocalIp get the local ip used to access remote address.
func GetLocalIp(address string) (string, error) {
	conn, err := net.Dial("udp", address)
	if err != nil {
		return "", err
	}
	ip, _, err := net.SplitHostPort(conn.LocalAddr().String())
	if err != nil {
		return "", err
	}
	return ip, nil
}

func WithTimeout(f func() error, timeout time.Duration) error {
	var done = make(chan int, 1)
	var t = time.NewTimer(timeout)
	var err error
	go func() {
		err = f()
		done <- 1
	}()
	select {
	case <-done:
		t.Stop()
	case <-t.C:
		err = fmt.Errorf("timeout after %s", timeout)
	}
	return err
}
