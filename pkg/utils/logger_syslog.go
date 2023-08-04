//go:build !windows
// +build !windows

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
	"log/syslog"
	"os"
	"sync"

	"github.com/sirupsen/logrus"
	logrus_syslog "github.com/sirupsen/logrus/hooks/syslog"
)

type logLine struct {
	level logrus.Level
	msg   string
}

type SyslogHook struct {
	*logrus_syslog.SyslogHook
	buffer chan logLine
}

func (hook *SyslogHook) flush() {
	for l := range hook.buffer {
		line := l.msg
		var err error
		switch l.level {
		case logrus.PanicLevel:
			err = hook.Writer.Crit(line)
		case logrus.FatalLevel:
			err = hook.Writer.Crit(line)
		case logrus.ErrorLevel:
			err = hook.Writer.Err(line)
		case logrus.WarnLevel:
			err = hook.Writer.Warning(line)
		case logrus.InfoLevel:
			err = hook.Writer.Info(line)
		case logrus.DebugLevel:
			err = hook.Writer.Debug(line)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "write to syslog: %v, level: %s, line: %s", err, l.level, line)
		}
	}
}

func (hook *SyslogHook) Fire(entry *logrus.Entry) error {
	line, err := entry.String()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to read entry, %v", err)
		return err
	}

	select {
	case hook.buffer <- logLine{entry.Level, line[27:]}: // drop the timestamp
		return nil
	default:
		fmt.Fprintf(os.Stderr, "buffer of syslog is full, drop: %s", line)
		return fmt.Errorf("buffer is full")
	}
}

var once sync.Once

func InitLoggers(logToSyslog bool) {
	if logToSyslog {
		once.Do(func() {
			hook, err := logrus_syslog.NewSyslogHook("", "", syslog.LOG_DEBUG|syslog.LOG_USER, "")
			if err != nil {
				// println("Unable to connect to local syslog daemon")
				return
			}
			syslogHook = &SyslogHook{hook, make(chan logLine, 1024)}
			go syslogHook.(*SyslogHook).flush()

			for _, l := range loggers {
				l.AddHook(syslogHook)
			}
		})
	}
}
