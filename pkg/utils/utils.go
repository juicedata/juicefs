/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package utils

import (
	"io"
	"os"
	"strings"

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
	return err == nil
}

// CopyFile copies file in src path to dst path
func CopyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return out.Close()
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
		),
	)
	return progress, bar
}

// NewProgressCounter init a progress counter
func NewProgressCounter(title string) (*mpb.Progress, *mpb.Bar) {
	var progress *mpb.Progress
	if isatty.IsTerminal(os.Stdout.Fd()) {
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
