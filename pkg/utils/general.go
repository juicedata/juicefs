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
	"math/rand"
	"time"
)

func SleepWithJitter(d time.Duration) {
	time.Sleep(JitterIt(d))
}

func JitterIt[T float64 | time.Duration](d T) T {
	j := int64(d / 20) // +- 5%
	return d + T(rand.Int63n(2*j+1)-j)
}
