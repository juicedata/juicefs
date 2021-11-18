/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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
	"io/ioutil"
	"os"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestLogger(t *testing.T) {
	_ = GetLogger("test")
	f, err := os.CreateTemp("", "test_logger")
	if err != nil {
		t.Fatalf("temp file: %s", err)
	}
	defer f.Close()
	SetOutFile("") // invalid
	SetOutFile(f.Name())
	InitLoggers(true)

	SetLogLevel(logrus.TraceLevel)
	SetLogLevel(logrus.DebugLevel)
	SetLogLevel(logrus.InfoLevel)
	SetLogLevel(logrus.ErrorLevel)
	SetLogLevel(logrus.FatalLevel)
	SetLogLevel(logrus.WarnLevel)
	logger := GetLogger("test")
	logger.Info("info level")
	logger.Debug("debug level")
	logger.Warnf("warn level")
	logger.Error("error level")

	d, _ := ioutil.ReadFile(f.Name())
	s := string(d)
	if strings.Contains(s, "info level") || strings.Contains(s, "debug level") {
		t.Fatalf("info/debug should not be logged: %s", s)
	} else if !strings.Contains(s, "warn level") || !strings.Contains(s, "error level") {
		t.Fatalf("warn/error should be logged: %s", s)
	}
}
