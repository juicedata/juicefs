/*
 * JuiceFS 删除性能基准测试工具
 *
 * 该工具直接调用 JuiceFS 的 Meta 接口进行删除性能测试，
 * 可以更精确地测量 batchunlink/batchclone 优化效果。
 *
 * 编译: go build -o delete_bench delete_bench.go
 * 使用: ./delete_bench -meta="redis://localhost:6379/1" -test=all
 */

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/sirupsen/logrus"
)

const (
	// 测试参数默认值
	DefaultSmallFileCount = 100000
	DefaultLargeFileCount = 1000
	DefaultLargeFileSize  = 100 * 1024 * 1024 // 100MB
	DefaultBatchSize      = 10000
	DefaultThreads        = 4
)

// 测试结果结构体（类似 C 的 struct）
type TestResult struct {
	TestName    string
	Engine      string
	Metric      string
	Value       float64
	Unit        string
	Duration    time.Duration
	Count       int64
	Description string
}

// 测试报告
type TestReport struct {
	Results []TestResult
	mu      sync.Mutex
}

func (r *TestReport) Add(result TestResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Results = append(r.Results, result)
}

func (r *TestReport) Print() {
	fmt.Println("\n========================================")
	fmt.Println("      JuiceFS 删除性能测试报告")
	fmt.Println("========================================")

	// 按测试名称分组
	groups := make(map[string][]TestResult)
	for _, r := range r.Results {
		groups[r.TestName] = append(groups[r.TestName], r)
	}

	// 按字母顺序输出
	var names []string
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		fmt.Printf("\n[%s]\n", name)
		for _, r := range groups[name] {
			fmt.Printf("  %-30s: %.2f %s", r.Metric, r.Value, r.Unit)
			if r.Description != "" {
				fmt.Printf(" (%s)", r.Description)
			}
			fmt.Println()
		}
	}
}

// 测试配置
type BenchConfig struct {
	MetaURL          string
	TestType         string
	SmallFileCount   int
	LargeFileCount   int
	LargeFileSize    int
	BatchSize        int
	Threads          int
	SkipCleanup      bool
	TrashDays        int
}

// 全局报告
var report TestReport

// 创建 Meta 客户端（类似 C 创建连接句柄）
func createMetaClient(metaURL string) meta.Meta {
	cfg := meta.DefaultConf()
	cfg.MaxDeletes = 10
	cfg.NoBGJob = true // 禁用后台任务，避免干扰测试

	client := meta.NewClient(metaURL, cfg)
	if client == nil {
		fmt.Fprintf(os.Stderr, "无法创建 Meta 客户端: %s\n", metaURL)
		os.Exit(1)
	}

	// 初始化文件系统
	format := &meta.Format{
		Name:      "delete-bench",
		DirStats:  true,
		TrashDays: 1,
	}
	if err := client.Init(format, true); err != nil {
		fmt.Fprintf(os.Stderr, "初始化文件系统失败: %v\n", err)
		os.Exit(1)
	}

	if err := client.NewSession(false); err != nil {
		fmt.Fprintf(os.Stderr, "创建 session 失败: %v\n", err)
		os.Exit(1)
	}

	return client
}

// 获取引擎名称
func getEngineName(metaURL string) string {
	if len(metaURL) > 8 && metaURL[:8] == "redis://" {
		return "redis"
	}
	if len(metaURL) > 8 && metaURL[:8] == "mysql://" {
		return "mysql"
	}
	if len(metaURL) > 7 && metaURL[:7] == "tikv://" {
		return "tikv"
	}
	if len(metaURL) > 10 && metaURL[:10] == "sqlite3://" {
		return "sqlite"
	}
	return "unknown"
}

// ==================== 测试 1: 小文件创建+删除性能 ====================
func testSmallFileDelete(m meta.Meta, cfg *BenchConfig) {
	fmt.Println("\n========================================")
	fmt.Println("  测试: 小文件删除性能")
	fmt.Println("========================================")

	ctx := meta.Background()
	engine := getEngineName(cfg.MetaURL)

	// 创建父目录
	var parent meta.Ino
	if errno := m.Mkdir(ctx, 1, "small_files", 0755, 0, 0, &parent, nil); errno != 0 {
		fmt.Fprintf(os.Stderr, "创建目录失败: %s\n", errno)
		return
	}

	// 批量创建小文件
	fmt.Printf("创建 %d 个小文件 (4KB each)...\n", cfg.SmallFileCount)
	start := time.Now()

	batchSize := 1000
	created := int64(0)
	var entries []*meta.Entry

	for i := 0; i < cfg.SmallFileCount; i++ {
		name := fmt.Sprintf("file_%08d.txt", i)
		var inode meta.Ino
		var attr meta.Attr
		if errno := m.Create(ctx, parent, name, 0644, 022, 0, &inode, &attr); errno != 0 {
			fmt.Fprintf(os.Stderr, "创建文件失败 %s: %s\n", name, errno)
			continue
		}

		// 写入 4KB 数据
		var sliceId uint64
		if errno := m.NewSlice(ctx, &sliceId); errno != 0 {
			continue
		}
		if errno := m.Write(ctx, inode, 0, 0, meta.Slice{Id: sliceId, Size: 4096, Len: 4096}, time.Now()); errno != 0 {
			continue
		}

		entries = append(entries, &meta.Entry{Name: []byte(name), Inode: inode, Attr: attr})
		atomic.AddInt64(&created, 1)

		// 批量 unlink 测试
		if len(entries) >= batchSize {
			// 这里只是创建，unlink 在后面
			entries = entries[:0]
		}
	}

	createTime := time.Since(start)
	fmt.Printf("创建完成: %d 文件, 耗时: %v\n", created, createTime)
	report.Add(TestResult{
		TestName:    "small_file_delete",
		Engine:      engine,
		Metric:      "create_time",
		Value:       createTime.Seconds(),
		Unit:        "seconds",
		Count:       created,
		Description: fmt.Sprintf("%.0f files/sec", float64(created)/createTime.Seconds()),
	})

	// 重新读取目录项用于批量删除
	fmt.Println("读取目录项...")
	var dirEntries []*meta.Entry
	if errno := m.Readdir(ctx, parent, 1, &dirEntries); errno != 0 {
		fmt.Fprintf(os.Stderr, "读取目录失败: %s\n", errno)
		return
	}

	// 测试 1: 逐文件删除（模拟优化前）
	fmt.Printf("逐文件删除 %d 个文件...\n", len(dirEntries))
	start = time.Now()
	deleted := int64(0)
	for _, entry := range dirEntries {
		if errno := m.Unlink(ctx, parent, string(entry.Name)); errno != 0 {
			fmt.Fprintf(os.Stderr, "删除失败 %s: %s\n", entry.Name, errno)
			continue
		}
		atomic.AddInt64(&deleted, 1)
	}
	singleTime := time.Since(start)
	fmt.Printf("逐文件删除完成: %d 文件, 耗时: %v\n", deleted, singleTime)

	report.Add(TestResult{
		TestName:    "small_file_delete",
		Engine:      engine,
		Metric:      "single_unlink_time",
		Value:       singleTime.Seconds(),
		Unit:        "seconds",
		Count:       deleted,
		Description: fmt.Sprintf("%.0f files/sec", float64(deleted)/singleTime.Seconds()),
	})

	// 重新创建文件用于批量删除测试
	fmt.Println("重新创建文件用于批量删除测试...")
	var batchEntries []*meta.Entry
	for i := 0; i < cfg.SmallFileCount; i++ {
		name := fmt.Sprintf("file_%08d.txt", i)
		var inode meta.Ino
		var attr meta.Attr
		if errno := m.Create(ctx, parent, name, 0644, 022, 0, &inode, &attr); errno != 0 {
			continue
		}
		var sliceId uint64
		m.NewSlice(ctx, &sliceId)
		m.Write(ctx, inode, 0, 0, meta.Slice{Id: sliceId, Size: 4096, Len: 4096}, time.Now())
		batchEntries = append(batchEntries, &meta.Entry{Name: []byte(name), Inode: inode, Attr: attr})
	}

	// 测试 2: 批量删除（BatchUnlink 优化）
	fmt.Printf("批量删除 %d 个文件 (BatchUnlink)...\n", len(batchEntries))
	start = time.Now()
	var count uint64
	if errno := m.BatchUnlink(ctx, parent, batchEntries, &count, true); errno != 0 {
		fmt.Fprintf(os.Stderr, "批量删除失败: %s\n", errno)
	}
	batchTime := time.Since(start)
	fmt.Printf("批量删除完成: %d 文件, 耗时: %v\n", count, batchTime)

	report.Add(TestResult{
		TestName:    "small_file_delete",
		Engine:      engine,
		Metric:      "batch_unlink_time",
		Value:       batchTime.Seconds(),
		Unit:        "seconds",
		Count:       int64(count),
		Description: fmt.Sprintf("%.0f files/sec", float64(count)/batchTime.Seconds()),
	})

	// 计算提升
	if batchTime > 0 && singleTime > 0 {
		improvement := float64(singleTime) / float64(batchTime)
		fmt.Printf("BatchUnlink 提升: %.2fx\n", improvement)
		report.Add(TestResult{
			TestName:    "small_file_delete",
			Engine:      engine,
			Metric:      "batch_unlink_improvement",
			Value:       improvement,
			Unit:        "x",
			Description: fmt.Sprintf("%.0f%% faster", (improvement-1)*100),
		})
	}

	// 清理
	if !cfg.SkipCleanup {
		m.Rmdir(ctx, 1, "small_files")
	}
}

// ==================== 测试 2: 大文件删除性能 ====================
func testLargeFileDelete(m meta.Meta, cfg *BenchConfig) {
	fmt.Println("\n========================================")
	fmt.Println("  测试: 大文件删除性能")
	fmt.Println("========================================")

	ctx := meta.Background()
	engine := getEngineName(cfg.MetaURL)

	// 创建父目录
	var parent meta.Ino
	if errno := m.Mkdir(ctx, 1, "large_files", 0755, 0, 0, &parent, nil); errno != 0 {
		fmt.Fprintf(os.Stderr, "创建目录失败: %s\n", errno)
		return
	}

	// 创建大文件
	fmt.Printf("创建 %d 个大文件 (%d MB each)...\n", cfg.LargeFileCount, cfg.LargeFileSize/1024/1024)
	start := time.Now()
	created := int64(0)
	totalSize := int64(0)

	for i := 0; i < cfg.LargeFileCount; i++ {
		name := fmt.Sprintf("large_file_%04d.bin", i)
		var inode meta.Ino
		var attr meta.Attr
		if errno := m.Create(ctx, parent, name, 0644, 022, 0, &inode, &attr); errno != 0 {
			continue
		}

		// 写入多个 slice 模拟大文件
		chunkSize := 64 * 1024 * 1024 // 64MB per chunk
		nChunks := cfg.LargeFileSize / chunkSize
		if cfg.LargeFileSize%chunkSize > 0 {
			nChunks++
		}

		for j := 0; j < nChunks; j++ {
			var sliceId uint64
			if errno := m.NewSlice(ctx, &sliceId); errno != 0 {
				continue
			}
			size := chunkSize
			if j == nChunks-1 && cfg.LargeFileSize%chunkSize > 0 {
				size = cfg.LargeFileSize % chunkSize
			}
			m.Write(ctx, inode, uint32(j), 0, meta.Slice{Id: sliceId, Size: uint32(size), Len: uint32(size)}, time.Now())
		}

		atomic.AddInt64(&created, 1)
		atomic.AddInt64(&totalSize, int64(cfg.LargeFileSize))
	}

	createTime := time.Since(start)
	fmt.Printf("创建完成: %d 文件, 总大小: %.2f GB, 耗时: %v\n",
		created, float64(totalSize)/1024/1024/1024, createTime)

	report.Add(TestResult{
		TestName:    "large_file_delete",
		Engine:      engine,
		Metric:      "create_time",
		Value:       createTime.Seconds(),
		Unit:        "seconds",
		Count:       created,
		Description: fmt.Sprintf("%.2f GB total", float64(totalSize)/1024/1024/1024),
	})

	// 读取目录项
	var dirEntries []*meta.Entry
	if errno := m.Readdir(ctx, parent, 1, &dirEntries); errno != 0 {
		fmt.Fprintf(os.Stderr, "读取目录失败: %s\n", errno)
		return
	}

	// 批量删除
	fmt.Printf("批量删除 %d 个大文件...\n", len(dirEntries))
	start = time.Now()
	var count uint64
	if errno := m.BatchUnlink(ctx, parent, dirEntries, &count, true); errno != 0 {
		fmt.Fprintf(os.Stderr, "批量删除失败: %s\n", errno)
	}
	batchTime := time.Since(start)
	fmt.Printf("批量删除完成: %d 文件, 耗时: %v\n", count, batchTime)

	gbPerSec := float64(totalSize) / 1024 / 1024 / 1024 / batchTime.Seconds()
	gbPerHour := gbPerSec * 3600

	report.Add(TestResult{
		TestName:    "large_file_delete",
		Engine:      engine,
		Metric:      "batch_delete_time",
		Value:       batchTime.Seconds(),
		Unit:        "seconds",
		Count:       int64(count),
		Description: fmt.Sprintf("%.2f GB/sec, %.2f GB/hour", gbPerSec, gbPerHour),
	})

	report.Add(TestResult{
		TestName:    "large_file_delete",
		Engine:      engine,
		Metric:      "delete_rate",
		Value:       gbPerHour,
		Unit:        "GB/hour",
	})

	if !cfg.SkipCleanup {
		m.Rmdir(ctx, 1, "large_files")
	}
}

// ==================== 测试 3: BatchClone 性能对比 ====================
func testBatchClone(m meta.Meta, cfg *BenchConfig) {
	fmt.Println("\n========================================")
	fmt.Println("  测试: BatchClone 性能对比")
	fmt.Println("========================================")

	ctx := meta.Background()
	engine := getEngineName(cfg.MetaURL)

	// 创建源目录
	var srcParent meta.Ino
	if errno := m.Mkdir(ctx, 1, "clone_src", 0755, 0, 0, &srcParent, nil); errno != 0 {
		fmt.Fprintf(os.Stderr, "创建源目录失败: %s\n", errno)
		return
	}

	// 创建源文件
	fmt.Printf("创建 %d 个源文件...\n", cfg.BatchSize)
	var srcEntries []*meta.Entry
	for i := 0; i < cfg.BatchSize; i++ {
		name := fmt.Sprintf("file_%08d.txt", i)
		var inode meta.Ino
		var attr meta.Attr
		if errno := m.Create(ctx, srcParent, name, 0644, 022, 0, &inode, &attr); errno != 0 {
			continue
		}
		var sliceId uint64
		m.NewSlice(ctx, &sliceId)
		m.Write(ctx, inode, 0, 0, meta.Slice{Id: sliceId, Size: 4096, Len: 4096}, time.Now())
		srcEntries = append(srcEntries, &meta.Entry{Name: []byte(name), Inode: inode, Attr: attr})
	}
	fmt.Printf("创建了 %d 个源文件\n", len(srcEntries))

	// 测试 1: 逐文件克隆（模拟优化前）
	var dstParent1 meta.Ino
	m.Mkdir(ctx, 1, "clone_dst_single", 0755, 0, 0, &dstParent1, nil)

	fmt.Printf("逐文件克隆 %d 个文件...\n", len(srcEntries))
	start := time.Now()
	cloned := int64(0)
	for _, entry := range srcEntries {
		var inode meta.Ino
		var attr meta.Attr
		if errno := m.Clone(ctx, entry.Inode, dstParent1, string(entry.Name), 0644, 022, 0, &inode, &attr); errno != 0 {
			// Clone 可能不支持，fallback 到创建
			var newInode meta.Ino
			var newAttr meta.Attr
			m.Create(ctx, dstParent1, string(entry.Name), 0644, 022, 0, &newInode, &newAttr)
		}
		atomic.AddInt64(&cloned, 1)
	}
	singleTime := time.Since(start)
	fmt.Printf("逐文件克隆完成: %d 文件, 耗时: %v\n", cloned, singleTime)

	report.Add(TestResult{
		TestName:    "batch_clone",
		Engine:      engine,
		Metric:      "single_clone_time",
		Value:       singleTime.Seconds(),
		Unit:        "seconds",
		Count:       cloned,
		Description: fmt.Sprintf("%.0f files/sec", float64(cloned)/singleTime.Seconds()),
	})

	// 测试 2: 批量克隆（BatchClone 优化）
	var dstParent2 meta.Ino
	m.Mkdir(ctx, 1, "clone_dst_batch", 0755, 0, 0, &dstParent2, nil)

	fmt.Printf("批量克隆 %d 个文件 (BatchClone)...\n", len(srcEntries))
	start = time.Now()
	var count uint64
	if errno := m.BatchClone(ctx, srcParent, dstParent2, srcEntries, 0, 0, &count); errno != 0 {
		fmt.Fprintf(os.Stderr, "批量克隆失败: %s\n", errno)
	}
	batchTime := time.Since(start)
	fmt.Printf("批量克隆完成: %d 文件, 耗时: %v\n", count, batchTime)

	report.Add(TestResult{
		TestName:    "batch_clone",
		Engine:      engine,
		Metric:      "batch_clone_time",
		Value:       batchTime.Seconds(),
		Unit:        "seconds",
		Count:       int64(count),
		Description: fmt.Sprintf("%.0f files/sec", float64(count)/batchTime.Seconds()),
	})

	// 计算提升
	if batchTime > 0 && singleTime > 0 {
		improvement := float64(singleTime) / float64(batchTime)
		fmt.Printf("BatchClone 提升: %.2fx\n", improvement)
		report.Add(TestResult{
			TestName:    "batch_clone",
			Engine:      engine,
			Metric:      "batch_clone_improvement",
			Value:       improvement,
			Unit:        "x",
			Description: fmt.Sprintf("%.0f%% faster", (improvement-1)*100),
		})
	}

	// 清理
	if !cfg.SkipCleanup {
		m.Rmdir(ctx, 1, "clone_src")
		m.Rmdir(ctx, 1, "clone_dst_single")
		m.Rmdir(ctx, 1, "clone_dst_batch")
	}
}

// ==================== 测试 4: GC 效率测试 ====================
func testGCEfficiency(m meta.Meta, cfg *BenchConfig) {
	fmt.Println("\n========================================")
	fmt.Println("  测试: GC 效率")
	fmt.Println("========================================")

	ctx := meta.Background()
	engine := getEngineName(cfg.MetaURL)

	// 创建大量文件然后删除，产生待清理的 slice
	fmt.Printf("创建并删除 %d 个文件以产生 GC 负载...\n", cfg.BatchSize)
	var parent meta.Ino
	m.Mkdir(ctx, 1, "gc_test", 0755, 0, 0, &parent, nil)

	var entries []*meta.Entry
	for i := 0; i < cfg.BatchSize; i++ {
		name := fmt.Sprintf("gc_file_%08d.txt", i)
		var inode meta.Ino
		var attr meta.Attr
		if errno := m.Create(ctx, parent, name, 0644, 022, 0, &inode, &attr); errno != 0 {
			continue
		}
		var sliceId uint64
		m.NewSlice(ctx, &sliceId)
		m.Write(ctx, inode, 0, 0, meta.Slice{Id: sliceId, Size: 4096, Len: 4096}, time.Now())
		entries = append(entries, &meta.Entry{Name: []byte(name), Inode: inode, Attr: attr})
	}

	// 删除所有文件
	fmt.Println("删除所有文件...")
	var count uint64
	m.BatchUnlink(ctx, parent, entries, &count, true)

	// 触发 GC（通过 ScanDeletedObject）
	fmt.Println("触发 GC 扫描...")
	start := time.Now()
	var scannedFiles int64
	var scannedSlices int64

	err := m.ScanDeletedObject(ctx,
		nil, // trash slices
		nil, // pending slices
		func(inode meta.Ino, size uint64, ts int64, inodes int64) (bool, error) {
			atomic.AddInt64(&scannedFiles, inodes)
			return false, nil
		},
		func(inode meta.Ino, size uint64, ts int64) (bool, error) {
			atomic.AddInt64(&scannedFiles, 1)
			return false, nil
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "GC 扫描失败: %v\n", err)
	}
	scanTime := time.Since(start)

	fmt.Printf("GC 扫描完成: %d 文件, 耗时: %v\n", scannedFiles, scanTime)
	report.Add(TestResult{
		TestName:    "gc_efficiency",
		Engine:      engine,
		Metric:      "scan_time",
		Value:       scanTime.Seconds(),
		Unit:        "seconds",
		Count:       scannedFiles,
		Description: fmt.Sprintf("%.0f files/sec", float64(scannedFiles)/scanTime.Seconds()),
	})

	if !cfg.SkipCleanup {
		m.Rmdir(ctx, 1, "gc_test")
	}
}

// ==================== 测试 5: 后台清理任务并发测试 ====================
func testBackgroundCleanup(m meta.Meta, cfg *BenchConfig) {
	fmt.Println("\n========================================")
	fmt.Println("  测试: 后台清理任务并发")
	fmt.Println("========================================")

	ctx := meta.Background()
	engine := getEngineName(cfg.MetaURL)

	// 创建大量文件并删除到 trash
	fmt.Printf("创建 %d 个文件并删除到 trash...\n", cfg.BatchSize)
	var parent meta.Ino
	m.Mkdir(ctx, 1, "trash_test", 0755, 0, 0, &parent, nil)

	for i := 0; i < cfg.BatchSize; i++ {
		name := fmt.Sprintf("trash_file_%08d.txt", i)
		var inode meta.Ino
		var attr meta.Attr
		if errno := m.Create(ctx, parent, name, 0644, 022, 0, &inode, &attr); errno != 0 {
			continue
		}
		var sliceId uint64
		m.NewSlice(ctx, &sliceId)
		m.Write(ctx, inode, 0, 0, meta.Slice{Id: sliceId, Size: 4096, Len: 4096}, time.Now())
	}

	// 删除到 trash（不跳过 trash）
	var entries []*meta.Entry
	m.Readdir(ctx, parent, 1, &entries)
	for _, entry := range entries {
		m.Unlink(ctx, parent, string(entry.Name)) // 进入 trash
	}

	// 测试 CleanupTrashBefore
	fmt.Println("测试 CleanupTrashBefore...")
	start := time.Now()
	var cleaned int
	stats := &meta.CleanupTrashStats{}
	m.CleanupTrashBefore(ctx, time.Now().Add(time.Hour), func(n int) {
		cleaned += n
	}, stats)
	cleanupTime := time.Since(start)

	fmt.Printf("Trash 清理完成: %d 文件, 耗时: %v\n", cleaned, cleanupTime)
	report.Add(TestResult{
		TestName:    "background_cleanup",
		Engine:      engine,
		Metric:      "trash_cleanup_time",
		Value:       cleanupTime.Seconds(),
		Unit:        "seconds",
		Count:       int64(cleaned),
		Description: fmt.Sprintf("%.0f files/sec", float64(cleaned)/cleanupTime.Seconds()),
	})

	if !cfg.SkipCleanup {
		m.Rmdir(ctx, 1, "trash_test")
	}
}

// ==================== 主函数 ====================
func main() {
	// 设置日志级别
	utils.SetLogLevel(logrus.WarnLevel)

	// 解析命令行参数（类似 C 的 getopt）
	cfg := &BenchConfig{}
	flag.StringVar(&cfg.MetaURL, "meta", "redis://localhost:6379/1", "元数据引擎连接串")
	flag.StringVar(&cfg.TestType, "test", "all", "测试类型: all|small|large|clone|gc|cleanup")
	flag.IntVar(&cfg.SmallFileCount, "small-count", DefaultSmallFileCount, "小文件数量")
	flag.IntVar(&cfg.LargeFileCount, "large-count", DefaultLargeFileCount, "大文件数量")
	flag.IntVar(&cfg.LargeFileSize, "large-size", DefaultLargeFileSize, "大文件大小(字节)")
	flag.IntVar(&cfg.BatchSize, "batch-size", DefaultBatchSize, "批量操作文件数量")
	flag.IntVar(&cfg.Threads, "threads", DefaultThreads, "并发线程数")
	flag.BoolVar(&cfg.SkipCleanup, "skip-cleanup", false, "跳过清理（用于调试）")
	flag.IntVar(&cfg.TrashDays, "trash-days", 1, "Trash 保留天数")
	flag.Parse()

	fmt.Println("JuiceFS 删除性能基准测试")
	fmt.Printf("元数据引擎: %s\n", cfg.MetaURL)
	fmt.Printf("测试类型: %s\n", cfg.TestType)
	fmt.Printf("Go 运行时: %s\n", runtime.Version())

	// 创建 Meta 客户端
	m := createMetaClient(cfg.MetaURL)
	defer m.CloseSession()

	// 运行测试
	switch cfg.TestType {
	case "all":
		testSmallFileDelete(m, cfg)
		testLargeFileDelete(m, cfg)
		testBatchClone(m, cfg)
		testGCEfficiency(m, cfg)
		testBackgroundCleanup(m, cfg)
	case "small":
		testSmallFileDelete(m, cfg)
	case "large":
		testLargeFileDelete(m, cfg)
	case "clone":
		testBatchClone(m, cfg)
	case "gc":
		testGCEfficiency(m, cfg)
	case "cleanup":
		testBackgroundCleanup(m, cfg)
	default:
		fmt.Fprintf(os.Stderr, "未知测试类型: %s\n", cfg.TestType)
		os.Exit(1)
	}

	// 输出报告
	report.Print()
}
