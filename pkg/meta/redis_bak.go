//go:build !noredis
// +build !noredis

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

package meta

func (m *redisMeta) buildDumpedSeg(typ int, opt *DumpOption, txn *eTxn) iDumpedSeg {
	return nil
}

func (m *redisMeta) buildLoadedSeg(typ int, opt *LoadOption) iLoadedSeg {
	return nil
}

func (m *redisMeta) execETxn(ctx Context, txn *eTxn, f func(Context, *eTxn) error) error {
	txn.opt.notUsed = true
	return f(ctx, txn)
}

func (m *redisMeta) prepareLoad(ctx Context, opt *LoadOption) error {
	return nil
}
