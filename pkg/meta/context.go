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

package meta

import (
	"context"
	"strconv"
)

type Ino uint64

func (i Ino) String() string {
	return strconv.FormatUint(uint64(i), 10)
}

type Context interface {
	context.Context
	Gid() uint32
	Uid() uint32
	Pid() uint32
	Cancel()
	Canceled() bool
}

type emptyContext struct {
	context.Context
}

func (ctx emptyContext) Gid() uint32    { return 0 }
func (ctx emptyContext) Uid() uint32    { return 0 }
func (ctx emptyContext) Pid() uint32    { return 1 }
func (ctx emptyContext) Cancel()        {}
func (ctx emptyContext) Canceled() bool { return false }

var Background Context = emptyContext{context.Background()}
