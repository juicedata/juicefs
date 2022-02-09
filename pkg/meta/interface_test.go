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

package meta

import (
	"os"
	"testing"
)

func Test_setPasswordFromEnv(t *testing.T) {
	os.Setenv("META_PASSWORD", "dbPasswd")
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
