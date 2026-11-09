/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package juicefs

import (
	"bytes"
	"io"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// These benchmarks use the same temporary SQLite/file volume as the unit tests.
// Setup and cleanup are excluded from time and allocation measurements.
func BenchmarkClientMetadata(b *testing.B) {
	b.Run("CreateEmpty", func(b *testing.B) {
		c := newTestClient(b)
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			f, err := c.Create("file-" + strconv.Itoa(i))
			if err != nil {
				b.Fatal(err)
			}
			if err := f.Close(); err != nil {
				b.Fatal(err)
			}
			i++
		}
		reportBenchmarkRate(b, "files/s")
	})
	b.Run("Mkdir", func(b *testing.B) {
		c := newTestClient(b)
		names := make([]string, b.N)
		for i := range names {
			names[i] = "dir-" + strconv.Itoa(i)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for _, name := range names {
			if err := c.Mkdir(name, 0755); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		reportBenchmarkRate(b, "op/s")
	})
	b.Run("RemoveFile", func(b *testing.B) {
		for _, tc := range []struct {
			name string
			size int
		}{
			{"Empty", 0},
			{"4KiB", 4 << 10},
		} {
			b.Run(tc.name, func(b *testing.B) {
				c := newTestClient(b)
				data := bytes.Repeat([]byte("juicefs!"), tc.size/8)
				names := make([]string, b.N)
				for i := range names {
					names[i] = "file-" + strconv.Itoa(i)
					// WriteFile closes and flushes each file before timing.
					if err := c.WriteFile(names[i], data, 0644); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for _, name := range names {
					if err := c.Remove(name); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()
				reportBenchmarkRate(b, "files/s")
				for _, name := range names {
					if _, err := c.Stat(name); !os.IsNotExist(err) {
						b.Fatalf("Stat after Remove(%s): got %v, want not exist", name, err)
					}
				}
			})
		}
	})
	b.Run("RemoveDir", func(b *testing.B) {
		c := newTestClient(b)
		// Prepare one directory per removal before starting the timer.
		names := make([]string, b.N)
		for i := range names {
			names[i] = "dir-" + strconv.Itoa(i)
			if err := c.Mkdir(names[i], 0755); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportAllocs()
		b.ResetTimer()
		for _, name := range names {
			if err := c.Remove(name); err != nil {
				b.Fatal(err)
			}
		}
		b.StopTimer()
		reportBenchmarkRate(b, "op/s")
	})
	b.Run("OpenClose", func(b *testing.B) {
		c := newTestClient(b)
		names := prepareMetadataBenchmarkFiles(b, c)
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			f, err := c.Open(names[i%len(names)])
			if err != nil {
				b.Fatal(err)
			}
			if err := f.Close(); err != nil {
				b.Fatal(err)
			}
			i++
		}
		reportBenchmarkRate(b, "files/s")
	})
	b.Run("Stat", func(b *testing.B) {
		c := newTestClient(b)
		names := prepareMetadataBenchmarkFiles(b, c)
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			if _, err := c.Stat(names[i%len(names)]); err != nil {
				b.Fatal(err)
			}
			i++
		}
		reportBenchmarkRate(b, "ops/s")
	})
}

// Prepare distinct files and their names before timing. Each operation visits
// the next file, restarting the traversal after the whole batch is visited.
func prepareMetadataBenchmarkFiles(b *testing.B, c *Client) []string {
	b.Helper()
	names := make([]string, 1024)
	for i := range names {
		names[i] = "file-" + strconv.Itoa(i)
		createFile(b, c, names[i])
	}
	return names
}

// reportBenchmarkRate reports average end-to-end latency, not percentiles.
func reportBenchmarkRate(b *testing.B, unit string) {
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), unit)
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e3, "avg-us/op")
}

func BenchmarkClientSmallFiles(b *testing.B) {
	data := bytes.Repeat([]byte("juicefs!"), 4096/8)
	b.Run("Create4KiB", func(b *testing.B) {
		c := newTestClient(b)
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			// Each iteration creates a new file and includes close/flush.
			if err := c.WriteFile("file-"+strconv.Itoa(i), data, 0644); err != nil {
				b.Fatal(err)
			}
			i++
		}
		reportBenchmarkRate(b, "files/s")
		got, err := c.ReadFile("file-" + strconv.Itoa(i-1))
		if err != nil || !bytes.Equal(got, data) {
			b.Fatalf("read back: err=%v, data equal=%v", err, bytes.Equal(got, data))
		}
	})
	b.Run("Read4KiBWarm", func(b *testing.B) {
		c := newTestClient(b)
		var names [256]string
		for i := range names {
			names[i] = "file-" + strconv.Itoa(i)
			if err := c.WriteFile(names[i], data, 0644); err != nil {
				b.Fatal(err)
			}
			got, err := c.ReadFile(names[i])
			if err != nil || !bytes.Equal(got, data) {
				b.Fatalf("warm read: err=%v, data equal=%v", err, bytes.Equal(got, data))
			}
		}
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			got, err := c.ReadFile(names[i%len(names)])
			if err != nil || len(got) != len(data) {
				b.Fatalf("ReadFile: n=%d, err=%v", len(got), err)
			}
			i++
		}
		reportBenchmarkRate(b, "files/s")
	})
}

const benchmarkLargeSize = 256 << 20

// Disable the SDK chunk cache for large-file tests. Read buffers and the OS
// page cache remain, so these are not cold-storage or disk durability tests.
func newLargeBenchmarkClient(b *testing.B) *Client {
	b.Helper()
	conf := testConfig()
	conf.CacheSize = 0
	conf.Prefetch = 0
	c, err := New("test", newTestVolume(b), conf)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := c.Close(); err != nil {
			b.Error(err)
		}
	})
	return c
}

func benchmarkLargeData() []byte {
	data := make([]byte, 1<<20)
	// A reproducible, nontrivial payload, prepared outside the timed loop.
	_, _ = rand.New(rand.NewSource(1)).Read(data)
	return data
}

func prepareLargeBenchmarkFile(b *testing.B, c *Client, data []byte) {
	b.Helper()
	f, err := c.Create("large")
	if err != nil {
		b.Fatal(err)
	}
	for off := 0; off < benchmarkLargeSize; off += len(data) {
		if n, err := f.Write(data); err != nil || n != len(data) {
			b.Fatalf("prepare Write: n=%d, err=%v", n, err)
		}
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
}

// verifyLargeBenchmarkFile also warms the OS page cache before read benchmarks.
func verifyLargeBenchmarkFile(b *testing.B, c *Client, data []byte) {
	b.Helper()
	f, err := c.Open("large")
	if err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, len(data))
	for off := 0; off < benchmarkLargeSize; off += len(buf) {
		if _, err := io.ReadFull(f, buf); err != nil {
			b.Fatal(err)
		}
		if !bytes.Equal(buf, data) {
			b.Fatalf("read back differs at offset %d", off)
		}
	}
	if info, err := f.Stat(); err != nil || info.Size() != benchmarkLargeSize {
		b.Fatalf("unexpected large file size: info=%v, err=%v", info, err)
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkClientSequential(b *testing.B) {
	b.Run("Write256MiB", func(b *testing.B) {
		c := newLargeBenchmarkClient(b)
		data := benchmarkLargeData()
		f, err := c.Create("large")
		if err != nil {
			b.Fatal(err)
		}
		b.SetBytes(benchmarkLargeSize)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				b.Fatal(err)
			}
			for off := 0; off < benchmarkLargeSize; off += len(data) {
				if n, err := f.Write(data); err != nil || n != len(data) {
					b.Fatalf("Write: n=%d, err=%v", n, err)
				}
			}
			// Include uploading and metadata commit in every measured pass.
			if err := f.Sync(); err != nil {
				b.Fatal(err)
			}
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
		verifyLargeBenchmarkFile(b, c, data)
	})
	b.Run("Read256MiB", func(b *testing.B) {
		c := newLargeBenchmarkClient(b)
		data := benchmarkLargeData()
		prepareLargeBenchmarkFile(b, c, data)
		verifyLargeBenchmarkFile(b, c, data)
		f, err := c.Open("large")
		if err != nil {
			b.Fatal(err)
		}
		buf := make([]byte, len(data))
		b.SetBytes(benchmarkLargeSize)
		b.ReportAllocs()
		for b.Loop() {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				b.Fatal(err)
			}
			for off := 0; off < benchmarkLargeSize; off += len(buf) {
				if _, err := io.ReadFull(f, buf); err != nil {
					b.Fatal(err)
				}
			}
		}
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
	})
}

func BenchmarkClientRandom(b *testing.B) {
	const blockSize = 4 << 10
	for _, write := range []bool{false, true} {
		name := "Read4KiB"
		if write {
			name = "Write4KiBSync"
		}
		b.Run(name, func(b *testing.B) {
			c := newLargeBenchmarkClient(b)
			data := benchmarkLargeData()
			prepareLargeBenchmarkFile(b, c, data)
			verifyLargeBenchmarkFile(b, c, data)
			flag := os.O_RDONLY
			if write {
				flag = os.O_RDWR
			}
			f, err := c.OpenFile("large", flag, 0)
			if err != nil {
				b.Fatal(err)
			}
			// A fixed random permutation visits aligned blocks across the full
			// file before repeating, without timing random-number generation.
			blocks := rand.New(rand.NewSource(1)).Perm(benchmarkLargeSize / blockSize)
			buf := bytes.Repeat([]byte{0x5a}, blockSize)
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				off := int64(blocks[i%len(blocks)] * blockSize)
				var n int
				if write {
					n, err = f.WriteAt(buf, off)
				} else {
					n, err = f.ReadAt(buf, off)
				}
				if err != nil || n != blockSize {
					b.Fatalf("random I/O at %d: n=%d, err=%v", off, n, err)
				}
				if write {
					// QD=1: report completed writes, not buffered WriteAt calls.
					if err := f.Sync(); err != nil {
						b.Fatal(err)
					}
				}
				i++
			}
			reportBenchmarkRate(b, "IOPS")
			if write {
				got := make([]byte, blockSize)
				for j := 0; j < min(i, len(blocks)); j++ {
					off := int64(blocks[j] * blockSize)
					if n, err := f.ReadAt(got, off); err != nil || n != blockSize || !bytes.Equal(got, buf) {
						b.Fatalf("read back at %d: n=%d, err=%v, data equal=%v", off, n, err, bytes.Equal(got, buf))
					}
				}
			} else {
				off := blocks[(i-1)%len(blocks)] * blockSize % len(data)
				if !bytes.Equal(buf, data[off:off+blockSize]) {
					b.Fatal("random read returned incorrect data")
				}
			}
			if err := f.Close(); err != nil {
				b.Fatal(err)
			}
		})
	}
}
