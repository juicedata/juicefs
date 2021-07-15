package main

import (
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
	"github.com/urfave/cli/v2"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type diskInfo struct {
	Name        string
	CacheDirs   []string
	DiskType    string
	DiskSize    string
	JfsUsedSize string
	FreeSize    string
}

func getDiskInfo(pathPattern string) []*diskInfo {
	disk2Dirs := make(map[string][]string)
	paths := expandDir(pathPattern)
	for _, p := range paths {
		name := findDisk(p)
		if dirs, ok := disk2Dirs[name]; ok {
			disk2Dirs[name] = append(dirs, p)
		} else {
			disk2Dirs[name] = []string{p}
		}
	}
	logger.Debug(disk2Dirs)
	var res []*diskInfo
	for name, dirs := range disk2Dirs {
		di := &diskInfo{
			Name:      name,
			CacheDirs: dirs,
		}
		di.DiskType = findDiskType(name)
		total, free := getDiskUsage(dirs[0])
		di.DiskSize = parseSize(total)
		di.FreeSize = parseSize(free)
		var used uint64
		for _, p := range dirs {
			size, err := dirSize(p)
			if err != nil {
				logger.Fatal(err)
			}
			used += uint64(size)
		}
		di.JfsUsedSize = parseSize(used)
		logger.Debugf("adding disk %s", di.Name)
		res = append(res, di)
	}

	return res
}

func parseSize(size uint64) string {
	GiB := 1 << 30
	MiB := 1 << 20
	KiB := 1 << 10
	if float64(size)/float64(GiB) >= 1 {
		return fmt.Sprintf("%.2f GiB", float64(size)/float64(GiB))
	} else if float64(size)/float64(MiB) >= 1 {
		return fmt.Sprintf("%.2f MiB", float64(size)/float64(MiB))
	} else if float64(size)/float64(KiB) >= 1 {
		return fmt.Sprintf("%.2f KiB", float64(size)/float64(KiB))
	} else {
		return fmt.Sprintf("%d B", size)
	}
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})
	return size, err
}

func findDisk(p string) string {
	p, err := filepath.EvalSymlinks(p)
	if err != nil {
		logger.Fatal(err)
	}
	if strings.HasPrefix(p, "/dev/shm") {
		return "RAM"
	}
	dpo, err := exec.Command("sh", "-c", "df -P "+p+" | tail -1 | cut -d' ' -f 1 | rev | cut -d '/' -f 1 | rev").Output()
	if err != nil {
		logger.Fatal(err)
	}
	pname := strings.TrimSpace(string(dpo))
	do, err := exec.Command("sh", "-c", "basename \"$(readlink -f \"/sys/class/block/"+pname+"/..\")\"").Output()
	if err != nil {
		logger.Fatal(err)
	}
	return strings.TrimSpace(string(do))
}

func findDiskType(deviceName string) string {
	if deviceName == "RAM" {
		return "MEM"
	}
	output, err := exec.Command("sh", "-c", "cat /sys/block/"+deviceName+"/queue/rotational").Output()
	s := strings.TrimSpace(string(output))
	if err != nil {
		logger.Fatal(err)
	}
	if s == "1" {
		return "HDD"
	} else if s == "0" {
		return "SSD"
	} else {
		logger.Fatal("unknown disk type")
	}
	return ""
}

func getDiskUsage(path string) (uint64, uint64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err == nil {
		return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize)
	} else {
		return 1, 1
	}
}

func expandDir(pattern string) []string {
	for strings.HasSuffix(pattern, "/") {
		pattern = pattern[:len(pattern)-1]
	}
	if pattern == "" {
		return []string{"/"}
	}
	if !hasMeta(pattern) {
		return []string{pattern}
	}
	dir, f := filepath.Split(pattern)
	if hasMeta(f) {
		matched, err := filepath.Glob(pattern)
		if err != nil {
			logger.Errorf("glob %s: %s", pattern, err)
			return []string{pattern}
		}
		return matched
	}
	var rs []string
	for _, p := range expandDir(dir) {
		rs = append(rs, filepath.Join(p, f))
	}
	return rs
}

func hasMeta(path string) bool {
	magicChars := `*?[`
	if runtime.GOOS != "windows" {
		magicChars = `*?[\`
	}
	return strings.ContainsAny(path, magicChars)
}

func show(ctx *cli.Context) error {
	config, err := showConf(ctx)
	if err != nil {
		return err
	}

	err = showCache(config)
	if err != nil {
		return err
	}

	// show env
	err = showEnv()
	if err != nil {
		return err
	}

	return nil
}

func showEnv() error {
	fmt.Println()
	fmt.Println("#### ENV")
	env := make(map[string]string)
	env["cpu"] = strconv.Itoa(runtime.NumCPU())
	percent, _ := cpu.Percent(time.Second, false)
	env["cpu_percent"] = fmt.Sprintf("%.1f %%", percent[0])
	memory, _ := mem.VirtualMemory()
	env["total_mem"] = parseSize(memory.Total)
	env["used_mem"] = parseSize(memory.Used)
	out, _ := exec.Command("uname", "-r").Output()
	env["linux"] = strings.TrimSpace(string(out))
	env["jfs_version"] = version.Version()

	bytes, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(bytes))
	return nil
}

func showCache(config vfs.Config) error {
	// show detail cache info
	fmt.Println()
	fmt.Println("#### CACHE INFO")
	cacheDir := config.Chunk.CacheDir
	infos := getDiskInfo(cacheDir)
	logger.Debugf("disks: %d", len(infos))

	for _, info := range infos {
		logger.Debug(info)
		bytes, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(bytes))
	}
	return nil
}

func showConf(ctx *cli.Context) (vfs.Config, error) {
	fmt.Println("#### CONFIG INFO")
	setLoggerLevel(ctx)
	// show config
	if ctx.Args().Len() < 1 {
		return vfs.Config{}, fmt.Errorf("mountpoint is needed")
	}
	mountPoint := ctx.Args().Get(0)
	bytes, err := ioutil.ReadFile(filepath.Join(mountPoint, ".jfsconfig"))
	var config vfs.Config
	err = json.Unmarshal(bytes, &config)
	if err != nil {
		return vfs.Config{}, err
	}
	fmt.Println(string(bytes))
	return config, nil
}

func showConfFlags() *cli.Command {
	return &cli.Command{
		Name:      "show",
		Usage:     "show config of JuiceFS",
		ArgsUsage: "mount point",
		Action:    show,
	}
}
