// Copyright 2015 Ka-Hing Cheung
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

package utils

import (
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

var mu sync.Mutex
var loggers = make(map[string]*logHandle)

var syslogHook logrus.Hook

type logHandle struct {
	logrus.Logger

	name     string
	lvl      *logrus.Level
	colorful bool
}

func (l *logHandle) Format(e *logrus.Entry) ([]byte, error) {
	lvl := e.Level
	if l.lvl != nil {
		lvl = *l.lvl
	}
	lvlStr := strings.ToUpper(lvl.String())
	if l.colorful {
		var color int
		switch lvl {
		case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
			color = 31 // RED
		case logrus.WarnLevel:
			color = 33 // YELLOW
		case logrus.InfoLevel:
			color = 34 // BLUE
		default: // logrus.TraceLevel, logrus.DebugLevel
			color = 35 // MAGENTA
		}
		lvlStr = fmt.Sprintf("\033[1;%dm%s\033[0m", color, lvlStr)
	}
	const timeFormat = "2006/01/02 15:04:05.000000"
	timestamp := e.Time.Format(timeFormat)
	str := fmt.Sprintf("%v %s[%d] <%v>: %v [%s:%d]",
		timestamp,
		l.name,
		os.Getpid(),
		lvlStr,
		strings.TrimRight(e.Message, "\n"),
		path.Base(e.Caller.File),
		e.Caller.Line)

	if len(e.Data) != 0 {
		str += " " + fmt.Sprint(e.Data)
	}
	if !strings.HasSuffix(str, "\n") {
		str += "\n"
	}
	return []byte(str), nil
}

// for aws.Logger
func (l *logHandle) Log(args ...interface{}) {
	l.Debugln(args...)
}

func newLogger(name string) *logHandle {
	l := &logHandle{Logger: *logrus.New(), name: name, colorful: SupportANSIColor(os.Stderr.Fd())}
	l.Formatter = l
	if syslogHook != nil {
		l.Hooks.Add(syslogHook)
	}
	l.SetReportCaller(true)
	return l
}

// GetLogger returns a logger mapped to `name`
func GetLogger(name string) *logHandle {
	mu.Lock()
	defer mu.Unlock()

	if logger, ok := loggers[name]; ok {
		return logger
	}
	logger := newLogger(name)
	loggers[name] = logger
	return logger
}

// SetLogLevel sets Level to all the loggers in the map
func SetLogLevel(lvl logrus.Level) {
	for _, logger := range loggers {
		logger.Level = lvl
	}
}

func DisableLogColor() {
	for _, logger := range loggers {
		logger.colorful = false
	}
}

func SetOutFile(name string) {
	file, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		return
	}
	for _, logger := range loggers {
		logger.SetOutput(file)
		logger.colorful = false
	}
}

func SetOutput(w io.Writer) {
	for _, logger := range loggers {
		logger.SetOutput(w)
	}
}
