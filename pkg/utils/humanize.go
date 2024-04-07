/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"errors"
	"strconv"

	"github.com/urfave/cli/v2"
)

func ParseBytes(ctx *cli.Context, key string, unit byte) uint64 {
	str := ctx.String(key)
	if len(str) == 0 {
		return 0
	}

	val, err := parsBytesStr(str, unit)
	if err != nil {
		logger.Fatalf("Invalid value \"%s\" for option \"--%s\"", str, key)
	}

	return val
}

func parsBytesStr(str string, unit byte) (uint64, error) {
	s := str
	if c := s[len(s)-1]; c < '0' || c > '9' {
		unit = c
		s = s[:len(s)-1]
	}
	val, err := strconv.ParseFloat(s, 64)
	if err == nil {
		var shift int
		switch unit {
		case 'k', 'K':
			shift = 10
		case 'm', 'M':
			shift = 20
		case 'g', 'G':
			shift = 30
		case 't', 'T':
			shift = 40
		case 'p', 'P':
			shift = 50
		case 'e', 'E':
			shift = 60
		default:
			err = errors.New("invalid unit")
		}
		val *= float64(uint64(1) << shift)
	}
	if err != nil {
		return 0, err
	}
	return uint64(val), nil
}

func ParseBytesStr(str string, unit byte) uint64 {
	u, err := parsBytesStr(str, unit)
	if err != nil {
		logger.Fatalf("Invalid value \"%s\"", str)
	}
	return u
}

func ParseMbps(ctx *cli.Context, key string) int64 {
	str := ctx.String(key)
	if len(str) == 0 {
		return 0
	}

	val, err := parseMbpsStr(str)
	if err != nil {
		logger.Fatalf("Invalid value \"%s\" for option \"--%s\"", str, key)
	}

	return val
}

func parseMbpsStr(str string) (int64, error) {
	s := str
	var unit byte = 'M'
	if c := s[len(s)-1]; c < '0' || c > '9' {
		unit = c
		s = s[:len(s)-1]
	}
	val, err := strconv.ParseFloat(s, 64)
	if err == nil {
		switch unit {
		case 'm', 'M':
		case 'g', 'G':
			val *= 1e3
		case 't', 'T':
			val *= 1e6
		case 'p', 'P':
			val *= 1e9
		default:
			err = errors.New("invalid unit")
		}
	}
	return int64(val), err
}

func ParseMbpsStr(str string) int64 {
	val, err := parseMbpsStr(str)
	if err != nil {
		logger.Fatalf("Invalid value \"%s\"", str)
	}
	return val
}

func Mbps(val int64) string {
	v := float64(val)
	if v < 1e3 {
		return strconv.FormatFloat(v, 'f', 1, 64) + " Mbps"
	} else if v < 1e6 {
		return strconv.FormatFloat(v/1e3, 'f', 1, 64) + " Gbps"
	} else if v < 1e9 {
		return strconv.FormatFloat(v/1e6, 'f', 1, 64) + " Tbps"
	}
	return strconv.FormatFloat(v/1e9, 'f', 1, 64) + " Pbps"
}
