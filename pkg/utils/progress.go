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
	"os"

	"github.com/mattn/go-isatty"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

type Progress struct {
	*mpb.Progress
	Quiet     bool
	showSpeed bool
	bars      []*mpb.Bar
}

type Bar struct {
	*mpb.Bar
	total int64
}

func (b *Bar) IncrTotal(n int64) { // not thread safe
	b.total += n
	b.Bar.SetTotal(b.total, false)
}

func (b *Bar) SetTotal(total int64) { // not thread safe
	b.total = total
	b.Bar.SetTotal(total, false)
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

func NewProgress(quiet, showSpeed bool) *Progress {
	var p *Progress
	if quiet || os.Getenv("DISPLAY_PROGRESSBAR") == "false" || !isatty.IsTerminal(os.Stdout.Fd()) {
		p = &Progress{mpb.New(mpb.WithWidth(64), mpb.WithOutput(nil)), true, showSpeed, nil}
	} else {
		p = &Progress{mpb.New(mpb.WithWidth(64)), false, showSpeed, nil}
		SetOutput(p)
	}
	return p
}

func (p *Progress) AddCountBar(name string, total int64) *Bar {
	b := p.Progress.AddBar(0, // disable triggerComplete
		mpb.PrependDecorators(
			decor.Name(name+" count: ", decor.WCSyncWidth),
			decor.CountersNoUnit("%d / %d"),
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
		decor.Name(name+" count: ", decor.WCSyncWidth),
		decor.Merge(decor.CurrentNoUnit("%d", decor.WCSyncSpaceR), decor.WCSyncSpaceR),
	}
	if p.showSpeed {
		decors = append(decors, decor.AverageSpeed(0, "  %.2f/s", decor.WCSyncSpaceR))
	}
	b := p.Progress.Add(0, newSpinner(),
		mpb.PrependDecorators(decors...),
		mpb.BarFillerClearOnComplete(),
	)
	p.bars = append(p.bars, b)
	return &Bar{Bar: b}
}

func (p *Progress) AddByteSpinner(name string) *Bar {
	decors := []decor.Decorator{
		decor.Name(name+" bytes: ", decor.WCSyncWidth),
		decor.CurrentKibiByte("% .2f", decor.WCSyncSpaceR),
		decor.CurrentNoUnit("(%d Bytes)", decor.WCSyncSpaceR),
	}
	if p.showSpeed { // FIXME: maybe use EWMA speed
		decors = append(decors, decor.AverageSpeed(decor.UnitKiB, "  % .2f", decor.WCSyncSpaceR))
	}
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
	progress := NewProgress(true, false)
	bar := progress.AddCountBar("Mock", 0)
	return progress, bar
}
