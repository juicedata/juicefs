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
package io.juicefs;

import com.beust.jcommander.JCommander;
import com.beust.jcommander.Parameter;
import com.beust.jcommander.Parameters;
import com.sun.management.OperatingSystemMXBean;
import io.juicefs.bench.NNBench;
import io.juicefs.bench.TestDFSIO;
import org.apache.commons.cli.ParseException;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.util.Shell;
import org.apache.hadoop.util.VersionInfo;

import java.io.Closeable;
import java.io.IOException;
import java.lang.management.ManagementFactory;
import java.nio.file.*;
import java.text.DecimalFormat;
import java.util.*;
import java.util.stream.Stream;

public class Main {
  private static final Map<String, Command> COMMAND = new HashMap<>();

  @Parameter(names = {"--help", "-h", "-help"}, help = true)
  private boolean help = false;

  public abstract static class Command implements Closeable {
    @Parameter(names = {"--help", "-h", "-help"}, help = true)
    public boolean help;

    public Command() {
      COMMAND.put(getCommand(), this);
    }

    public abstract void init() throws IOException;

    public abstract void run() throws IOException;

    public abstract String getCommand();

  }

  @Parameters(commandDescription = "Show JuiceFS Information")
  private static class CommandShowInfo extends Command {
    @Override
    public void close() throws IOException {
    }

    static class CacheDisk {
      String name;
      List<String> cacheDirs;
      String type;
      long diskSize;
      long jfsUsedSize;
      long freeSize;

      public CacheDisk(String name, List<String> cacheDirs) {
        this.name = name;
        this.cacheDirs = cacheDirs;
        this.type = findDiskType(name);
        this.jfsUsedSize = cacheDirs.stream().mapToLong(d -> getDirectorySize(Paths.get(d))).sum();
        try {
          this.diskSize = Files.getFileStore(Paths.get(cacheDirs.get(0))).getTotalSpace();
          this.freeSize = Files.getFileStore(Paths.get(cacheDirs.get(0))).getUsableSpace();
        } catch (IOException e) {
          throw new RuntimeException(e);
        }
      }

      private String findDiskType(String deviceName) {
        if (deviceName.equals("RAM")) {
          return "MEM";
        }
        String s;
        try {
          s = Shell.execCommand("sh", "-c", "cat /sys/block/" + deviceName + "/queue/rotational").trim();
        } catch (IOException e) {
          throw new RuntimeException(e);
        }
        if (s.equals("1")) {
          return "HDD";
        } else if (s.equals("0")) {
          return "SSD";
        } else {
          throw new RuntimeException("unknown disk type");
        }
      }

      private long getDirectorySize(Path path) {
        long size;
        try (Stream<Path> walk = Files.walk(path)) {
          size = walk
                  .filter(Files::isRegularFile)
                  .mapToLong(p -> {
                    try {
                      return Files.size(p);
                    } catch (IOException e) {
                      System.err.printf("Failed to get size of %s%n%s", p, e);
                      return 0L;
                    }
                  })
                  .sum();
        } catch (IOException e) {
          throw new RuntimeException(e);
        }
        return size;
      }

      private static String parseSize(long size) {
        int GB = 1 << 30;
        int MB = 1 << 20;
        int KB = 1 << 10;
        DecimalFormat df = new DecimalFormat("0.0");
        String resultSize;
        if (size / GB >= 1) {
          resultSize = df.format(size / (float) GB) + "GiB";
        } else if (size / MB >= 1) {
          resultSize = df.format(size / (float) MB) + "MiB";
        } else if (size / KB >= 1) {
          resultSize = df.format(size / (float) KB) + "KiB";
        } else {
          resultSize = size + "B";
        }
        return resultSize;
      }

      @Override
      public String toString() {
        DecimalFormat df = new DecimalFormat("0.00");
        float freeRatio = (float) freeSize / diskSize;
        final StringJoiner sj = new StringJoiner("\n");
        sj.add("    " + name + ":");
        if (cacheDirs.size() == 1) {
          sj.add("\tcacheDir=" + cacheDirs.get(0));
        } else {
          sj.add("\tcacheDirs=" + cacheDirs);
        }
        sj.add("\ttype=" + type)
                .add("\tdiskSize=" + parseSize(diskSize))
                .add("\tjfsUsedSize=" + parseSize(jfsUsedSize))
                .add("\tfreeRatio=" + df.format(freeRatio));
        return sj.add("\n").toString();
      }
    }

    private final Configuration conf;

    public CommandShowInfo() {
      conf = new Configuration();
    }

    @Override
    public void init() throws IOException {

    }

    private void showJFSConf() {
      System.out.println("JUICEFS CONF:");
      Map<String, String> jfsConf = conf.getValByRegex("juicefs*");
      StringBuilder sb = new StringBuilder();
      for (Map.Entry<String, String> entry : jfsConf.entrySet()) {
        sb.append("\t").append(entry.getKey()).append("=").append(entry.getValue()).append("\n");
      }
      System.out.println(sb);
    }

    private void showCacheInfo() {
      System.out.println("CACHE INFO:");
      final Map<String, String> cacheDir = conf.getValByRegex("juicefs.*cache-dir");
      final Map<String, String> cacheSize = conf.getValByRegex("juicefs.*cache-size");

      for (Map.Entry<String, String> entry : cacheSize.entrySet()) {
        String jfsName = entry.getKey().split("\\.").length == 3 ? entry.getKey().split("\\.")[1] : "";
        if (!jfsName.equals("")) {
          System.out.println("- " + jfsName);
        }
        System.out.println("\tcacheSize=" + cacheSize.getOrDefault("juicefs." + jfsName + ".cache-size",
                cacheSize.getOrDefault("juicefs.cache-size", "1024")) + "MiB");
      }

      for (Map.Entry<String, String> entry : cacheDir.entrySet()) {
        String jfsName = entry.getKey().split("\\.").length == 3 ? entry.getKey().split("\\.")[1] : "";
        if (!jfsName.equals("")) {
          System.out.println("- " + jfsName);
        }
        Map<String, List<String>> disk2Dirs = new HashMap<>();
        List<String> expandDirs = new ArrayList<>();
        String[] patterns = entry.getValue().split(":");
        for (String pattern : patterns) {
          expandDirs.addAll(expandDir(pattern));
        }
        for (String dir : expandDirs) {
          String disk = findDisk(dir);
          disk2Dirs.computeIfAbsent(disk, s -> new ArrayList<>()).add(dir);
        }
        for (Map.Entry<String, List<String>> disk2Dir : disk2Dirs.entrySet()) {
          System.out.println(new CacheDisk(disk2Dir.getKey(), disk2Dir.getValue()));
        }
      }
    }

    private String findDisk(String dir) {
      if (dir.trim().startsWith("/dev/shm")) {
        return "RAM";
      }
      try {
        String pname = Shell.execCommand("sh", "-c", "df -P " + dir + " | tail -1 | cut -d' ' -f 1 | rev | cut -d '/' -f 1 | rev").trim();
        return Shell.execCommand("sh", "-c", "basename \"$(readlink -f \"/sys/class/block/" + pname + "/..\")\"").trim();
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
    }

    private boolean hasMeta(String path) {
      String chars = "*?[";
      if (!System.getProperty("os.name").toLowerCase().contains("windows")) {
        chars = "*?[\\";
      }
      for (char c : chars.toCharArray()) {
        if (path.contains(String.valueOf(c))) {
          return true;
        }
      }
      return false;
    }

    private List<String> expandDir(String path) {
      if (!hasMeta(path)) {
        return Collections.singletonList(path);
      }
      List<String> res = new ArrayList<>();
      String p = Paths.get(path).getParent().toString();
      String f = Paths.get(path).getFileName().toString();
      try (DirectoryStream<Path> paths = Files.newDirectoryStream(Paths.get(p), f)) {
        paths.iterator().forEachRemaining(i -> res.add(i.toString()));
        return res;
      } catch (NoSuchFileException e) {
        Path parent = Paths.get(path).getParent();
        List<String> expands = expandDir(parent.toString());
        for (String expand : expands) {
          String d = Paths.get(expand, f).toString();
          if (Files.exists(Paths.get(d))) {
            res.add(d);
          }
        }
        return res;
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
    }

    private void showEnv() throws IOException {
      System.out.println("ENV");
      Map<String, String> env = new LinkedHashMap<>();

      env.put("cpu", String.valueOf(Runtime.getRuntime().availableProcessors()));
      OperatingSystemMXBean osmxb = (OperatingSystemMXBean) ManagementFactory.getOperatingSystemMXBean();
      env.put("cpu_percent", String.format("%.1f%%", osmxb.getSystemCpuLoad() * 100));
      env.put("total_mem", (osmxb.getTotalPhysicalMemorySize() >> 30) + "GiB");
      env.put("free_mem", (osmxb.getFreePhysicalMemorySize() >> 30) + "GiB");
      env.put("file.encoding", System.getProperty("file.encoding"));
      env.put("linux", Shell.execCommand("uname", "-r").trim());
      env.put("hadoop", VersionInfo.getVersion());
      env.put("java.version", System.getProperty("java.version"));
      env.put("java.home", System.getProperty("java.home"));

      StringBuilder sb = new StringBuilder();
      for (Map.Entry<String, String> entry : env.entrySet()) {
        sb.append("\t").append(entry.getKey()).append("=").append(entry.getValue()).append("\n");
      }
      System.out.println(sb);
    }

    public void run() throws IOException {
      showJFSConf();
      showCacheInfo();
      showEnv();
    }

    @Override
    public String getCommand() {
      return "info";
    }
  }


  public static void main(String[] args) throws ParseException, IOException {
    Main main = new Main();
    Command showInfo = new CommandShowInfo();
    Command dfsio = new TestDFSIO();
    Command nnbench = new NNBench();
    JCommander jc = JCommander.newBuilder()
            .addObject(main)
            .addCommand(showInfo.getCommand(), showInfo)
            .addCommand(dfsio.getCommand(), dfsio)
            .addCommand(nnbench.getCommand(), nnbench)
            .build();
    jc.parse(args);

    if (main.help) {
      jc.usage();
      return;
    }

    Command command = COMMAND.get(jc.getParsedCommand());
    if (command.help) {
      jc.getCommands().get(jc.getParsedCommand()).usage();
      return;
    }
    command.init();
    command.run();
    command.close();
  }
}
