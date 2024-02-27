/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"os"
	"sync/atomic"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

type Progress struct {
	*mpb.Progress
	Quiet bool
	bars  []*mpb.Bar
}

type Bar struct {
	total int64
	*mpb.Bar
}

func (b *Bar) IncrTotal(n int64) {
	total := atomic.AddInt64(&b.total, n)
	b.Bar.SetTotal(total, false)
}

func (b *Bar) SetTotal(total int64) {
	atomic.StoreInt64(&b.total, total)
	b.Bar.SetTotal(total, false)
}

func (b *Bar) GetTotal() int64 {
	return atomic.LoadInt64(&b.total)
}

func (b *Bar) Done() {
	b.Bar.SetTotal(0, true)
}

type DoubleSpinner struct {
	count *mpb.Bar
	bytes *mpb.Bar
}

func (s *DoubleSpinner) IncrInt64(size int64) {
	s.count.Increment()
	s.bytes.IncrInt64(size)
}

func (s *DoubleSpinner) Done() {
	s.count.SetTotal(0, true)
	s.bytes.SetTotal(0, true)
}

func (s *DoubleSpinner) Current() (int64, int64) {
	return s.count.Current(), s.bytes.Current()
}

func (s *DoubleSpinner) SetCurrent(count, bytes int64) {
	s.count.SetCurrent(count)
	s.bytes.SetCurrent(bytes)
}

func NewProgress(quiet bool) *Progress {
	var p *Progress
	if quiet || os.Getenv("DISPLAY_PROGRESSBAR") == "false" || !isatty.IsTerminal(os.Stdout.Fd()) {
		p = &Progress{mpb.New(mpb.WithWidth(64), mpb.WithOutput(nil)), true, nil}
	} else {
		p = &Progress{mpb.New(mpb.WithWidth(64)), false, nil}
		SetOutput(p)
	}
	return p
}

func (p *Progress) AddCountBar(name string, total int64) *Bar {
	startTime := time.Now()
	var speedMsg, usedMsg string
	b := p.Progress.AddBar(0, // disable triggerComplete
		mpb.PrependDecorators(
			decor.Name(name+": ", decor.WCSyncWidth),
			decor.CountersNoUnit("%d/%d"),
		),
		mpb.AppendDecorators(
			decor.OnComplete(decor.AverageSpeed(0, " %.1f/s", decor.WCSyncWidthR), ""),
			decor.Any(func(s decor.Statistics) string {
				if s.Completed && speedMsg == "" {
					speed := float64(s.Current) / time.Since(startTime).Seconds()
					speedMsg = fmt.Sprintf(" %.1f/s", speed)
				}
				return speedMsg
			}, decor.WCSyncWidthR),
			decor.OnComplete(decor.Name(" ETA: ", decor.WCSyncWidthR), ""),
			decor.OnComplete(
				decor.AverageETA(decor.ET_STYLE_GO, decor.WCSyncWidthR), "",
			),
			decor.Any(func(s decor.Statistics) string {
				if s.Completed && usedMsg == "" {
					usedMsg = " used: " + (time.Since(startTime)).String()
				}
				return usedMsg
			}, decor.WCSyncWidthR),
		),
	)
	b.SetTotal(total, false)
	p.bars = append(p.bars, b)
	return &Bar{Bar: b, total: total}
}

func newSpinner() mpb.BarFiller {
	spinnerStyle := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for i, s := range spinnerStyle {
		spinnerStyle[i] = "\033[1;32m" + s + "\033[0m"
	}
	return mpb.NewBarFiller(mpb.SpinnerStyle(spinnerStyle...))
}

func (p *Progress) AddCountSpinner(name string) *Bar {
	decors := []decor.Decorator{
		decor.Name(name+": ", decor.WCSyncWidth),
		decor.Merge(decor.CurrentNoUnit("%d", decor.WCSyncSpaceR), decor.WCSyncSpaceR),
	}
	decors = append(decors, decor.AverageSpeed(0, "  %.1f/s", decor.WCSyncSpaceR))
	b := p.Progress.Add(0, newSpinner(),
		mpb.PrependDecorators(decors...),
		mpb.BarFillerClearOnComplete(),
	)
	p.bars = append(p.bars, b)
	return &Bar{Bar: b}
}

func (p *Progress) AddByteSpinner(name string) *Bar {
	decors := []decor.Decorator{
		decor.Name(name+": ", decor.WCSyncWidth),
		decor.CurrentKibiByte("% .1f", decor.WCSyncSpaceR),
		decor.CurrentNoUnit("(%d Bytes)", decor.WCSyncSpaceR),
	}
	// FIXME: maybe use EWMA speed
	decors = append(decors, decor.AverageSpeed(decor.UnitKiB, "  % .1f", decor.WCSyncSpaceR))
	b := p.Progress.Add(0, newSpinner(),
		mpb.PrependDecorators(decors...),
		mpb.BarFillerClearOnComplete(),
	)
	p.bars = append(p.bars, b)
	return &Bar{Bar: b}
}

func (p *Progress) AddIoSpeedBar(name string, total int64) *Bar {
	b := p.Progress.Add(0,
		mpb.NewBarFiller(mpb.BarStyle()),
		mpb.PrependDecorators(
			decor.Name(name+": ", decor.WCSyncWidth),
			decor.CountersKibiByte("% .1f / % .1f"),
		),
		mpb.AppendDecorators(
			decor.OnComplete(decor.Percentage(decor.WC{W: 5}), "done"),
			decor.OnComplete(
				decor.AverageETA(decor.ET_STYLE_GO, decor.WC{W: 6}), "",
			),
		),
	)
	b.SetTotal(total, false)
	p.bars = append(p.bars, b)
	return &Bar{Bar: b}
}

func (p *Progress) AddDoubleSpinner(name string) *DoubleSpinner {
	return &DoubleSpinner{
		p.AddCountSpinner(name).Bar,
		p.AddByteSpinner(name).Bar,
	}
}

func (p *Progress) AddDoubleSpinnerTwo(countName, sizeName string) *DoubleSpinner {
	return &DoubleSpinner{
		p.AddCountSpinner(countName).Bar,
		p.AddByteSpinner(sizeName).Bar,
	}
}

func (p *Progress) Done() {
	for _, b := range p.bars {
		if !b.Completed() {
			b.SetTotal(0, true)
		}
	}
	p.Progress.Wait()
	SetOutput(os.Stderr)
}

func MockProgress() (*Progress, *Bar) {
	progress := NewProgress(true)
	bar := progress.AddCountBar("Mock", 0)
	return progress, bar
}
