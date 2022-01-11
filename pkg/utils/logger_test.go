/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
