// +build !windows

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
	"fmt"
	"log/syslog"
	"os"

	"github.com/sirupsen/logrus"
	logrus_syslog "github.com/sirupsen/logrus/hooks/syslog"
)

type SyslogHook struct {
	*logrus_syslog.SyslogHook
}

func (hook *SyslogHook) Fire(entry *logrus.Entry) error {
	line, err := entry.String()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to read entry, %v", err)
		return err
	}

	// drop the timestamp
	line = line[27:]

	switch entry.Level {
	case logrus.PanicLevel:
		return hook.Writer.Crit(line)
	case logrus.FatalLevel:
		return hook.Writer.Crit(line)
	case logrus.ErrorLevel:
		return hook.Writer.Err(line)
	case logrus.WarnLevel:
		return hook.Writer.Warning(line)
	case logrus.InfoLevel:
		return hook.Writer.Info(line)
	case logrus.DebugLevel:
		return hook.Writer.Debug(line)
	default:
		return nil
	}
}

func InitLoggers(logToSyslog bool) {
	if logToSyslog {
		hook, err := logrus_syslog.NewSyslogHook("", "", syslog.LOG_DEBUG|syslog.LOG_USER, "")
		if err != nil {
			// println("Unable to connect to local syslog daemon")
			return
		}
		syslogHook = &SyslogHook{hook}

		for _, l := range loggers {
			l.Hooks.Add(syslogHook)
		}
	}
}
