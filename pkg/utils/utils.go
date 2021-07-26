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
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
	"io"
	"os"
	"strings"
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

//NewDynProgressBar init a dynamic progress bar,the title will appears at the head of the progress bar
func NewDynProgressBar(title string, w io.Writer) (*mpb.Progress, *mpb.Bar) {
	progress := mpb.New(mpb.WithWidth(64), mpb.WithOutput(w))
	bar := progress.Add(0,
		mpb.NewBarFiller(mpb.BarStyle().Lbound("╢").Filler("▌").Tip("▌").Padding("░").Rbound("╟")),
		mpb.PrependDecorators(
			// display our title with one space on the right
			decor.Name(title, decor.WC{W: len(title) + 1, C: decor.DidentRight}),
		),
		mpb.PrependDecorators(decor.CountersNoUnit("%d / %d")),
		mpb.AppendDecorators(
			decor.OnComplete(decor.Percentage(decor.WC{W: 5}), "done"),
		),
	)
	return progress, bar
}

//NewProgressCounter init a progress counter
func NewProgressCounter(title string, w io.Writer) (*mpb.Progress, *mpb.Bar) {
	process := mpb.New(mpb.WithWidth(64), mpb.WithOutput(w))
	bar := process.AddSpinner(0,
		mpb.PrependDecorators(
			decor.Name(title, decor.WC{W: len(title) + 1, C: decor.DidentRight}),
			decor.CurrentNoUnit("%d"),
		),
		mpb.BarFillerClearOnComplete(),
	)
	return process, bar
}
