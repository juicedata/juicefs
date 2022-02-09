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

package meta

import (
	"os"
	"testing"
)

func Test_setPasswordFromEnv(t *testing.T) {
	os.Setenv("META_PASSWORD", "dbPasswd")
	defer os.Unsetenv("META_PASSWORD")
	tests := []struct {
		args string
		want string
	}{
		//redis
		{
			args: "redis://:pwd@localhost:6379/1",
			want: "redis://:pwd@localhost:6379/1",
		},
		{
			args: "redis://localhost:6379/1",
			want: "redis://:dbPasswd@localhost:6379/1",
		},
		{
			args: "redis://root:password@masterName,1.2.3.4,1.2.5.6:26379/2",
			want: "redis://root:password@masterName,1.2.3.4,1.2.5.6:26379/2",
		},
		{
			args: "redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2",
			want: "redis://:password@masterName,1.2.3.4,1.2.5.6:26379/2",
		},
		{
			args: "redis://masterName,1.2.3.4,1.2.5.6:26379/2",
			want: "redis://:dbPasswd@masterName,1.2.3.4,1.2.5.6:26379/2",
		},

		//mysql
		{
			args: "mysql://root:password@(127.0.0.1:3306)/juicefs",
			want: "mysql://root:password@(127.0.0.1:3306)/juicefs",
		},
		{
			args: "mysql://root:@(127.0.0.1:3306)/juicefs",
			want: "mysql://root:dbPasswd@(127.0.0.1:3306)/juicefs",
		},
		{
			args: "mysql://root@(127.0.0.1:3306)/juicefs",
			want: "mysql://root:dbPasswd@(127.0.0.1:3306)/juicefs",
		},

		//postgres
		{
			args: "postgres://root:password@192.168.1.6:5432/juicefs",
			want: "postgres://root:password@192.168.1.6:5432/juicefs",
		},
		{
			args: "postgres://root:@192.168.1.6:5432/juicefs",
			want: "postgres://root:dbPasswd@192.168.1.6:5432/juicefs",
		},
		{
			args: "postgres://root@192.168.1.6:5432/juicefs",
			want: "postgres://root:dbPasswd@192.168.1.6:5432/juicefs",
		},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got := setPasswordFromEnv(tt.args); got != tt.want {
				t.Errorf("setPasswordFromEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}
