package io.juicefs;

import com.beust.jcommander.JCommander;
import com.beust.jcommander.Parameter;
import com.beust.jcommander.Parameters;
import com.sun.management.OperatingSystemMXBean;
import org.apache.commons.cli.ParseException;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FSDataInputStream;
import org.apache.hadoop.fs.FSDataOutputStream;
import org.apache.hadoop.util.Shell;
import org.apache.hadoop.util.VersionInfo;

import java.io.Closeable;
import java.io.IOException;
import java.lang.management.ManagementFactory;
import java.net.URI;
import java.nio.file.*;
import java.text.DecimalFormat;
import java.util.*;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.SynchronousQueue;
import java.util.stream.Stream;

public class Main {
  private static final Map<String, Command> COMMAND = new HashMap<>();

  @Parameter(names = {"--help", "-h", "-help"}, help = true)
  private boolean help = false;

  @Parameters(commandDescription = "Show JuiceFS Information")
  private static class CommandShowInfo extends Command implements Closeable {

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
        sj.add(name + ":");
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

    private void showJFSConf() {
      System.out.println("######## JUICEFS CONF ########");
      Map<String, String> jfsConf = conf.getValByRegex("juicefs*");
      StringBuilder sb = new StringBuilder();
      for (Map.Entry<String, String> entry : jfsConf.entrySet()) {
        sb.append("\t").append(entry.getKey()).append("=").append(entry.getValue()).append("\n");
      }
      System.out.println(sb);
    }

    private void showCacheInfo() {
      System.out.println("######## CACHE INFO ########");
      final Map<String, String> cacheDir = conf.getValByRegex("juicefs.*cache-dir");
      final Map<String, String> cacheSize = conf.getValByRegex("juicefs.*cache-size");

      for (Map.Entry<String, String> entry : cacheSize.entrySet()) {
        String jfsName = entry.getKey().split("\\.").length == 3 ? entry.getKey().split("\\.")[1] : "";
        if (!jfsName.equals("")) {
          System.out.println("######## " + jfsName);
        }
        System.out.println("cacheSize=" + cacheSize.getOrDefault("juicefs." + jfsName + ".cache-size",
                cacheSize.getOrDefault("juicefs.cache-size", "1024")) + "MiB");
      }

      for (Map.Entry<String, String> entry : cacheDir.entrySet()) {
        String jfsName = entry.getKey().split("\\.").length == 3 ? entry.getKey().split("\\.")[1] : "";
        if (!jfsName.equals("")) {
          System.out.println("######## " + jfsName);
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
        System.out.println(pname + " xxxxxxxxxxxxxxxxx");
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
      System.out.println("######## ENV ########");
      Map<String, String> env = new LinkedHashMap<>();

      env.put("cpu", String.valueOf(Runtime.getRuntime().availableProcessors()));
      OperatingSystemMXBean osmxb = (OperatingSystemMXBean) ManagementFactory.getOperatingSystemMXBean();
      env.put("cpu_percent", String.format("%.1f%%", osmxb.getSystemCpuLoad() * 100));
      env.put("total_mem", (osmxb.getTotalPhysicalMemorySize() >> 30) + "GiB");
      env.put("free_mem", (osmxb.getFreePhysicalMemorySize() >> 30) + "GiB");
      env.put("file.encoding", System.getProperty("file.encoding"));
      env.put("os", System.getProperty("os.name"));
      env.put("linux", Shell.execCommand("uname", "-r").trim());
      env.put("hadoop", VersionInfo.getVersion());

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

  abstract static class Command implements Closeable {
    public Command() {
      COMMAND.put(getCommand(), this);
    }

    public abstract void run() throws IOException;

    public abstract String getCommand();
  }

  @Parameters(commandDescription = "BenchMark FileSystem")
  private static class CommandBenchmark extends Command {

    @Parameter(names = {"--fs", "-fs"}, description = "FileSystem")
    String fs;

    @Parameter(names = {"--big-file-size", "-big-file-size"})
    float bigFileSizeMiB = 1024;

    @Parameter(names = {"--small-file-size", "-small-file-size"})
    float smallFileSizeMiB = 0.1f;

    @Parameter(names = {"--small-file-count", "-small-file-count"})
    int smallFileCount = 100;

    @Parameter(names = {"--threads", "-threads"})
    int threads = 1;

    @Parameter(names = {"--bufferSize", "-bufferSize"})
    int bufferSizeMiB = 1;

    private final String BIG_FILE_BASE_PATH = "/tmp/jfs-bench-big-file";
    private final String SMALL_FILE_BASE_PATH = "/tmp/jfs-bench-small-file";

    private Configuration conf;
    private org.apache.hadoop.fs.FileSystem fileSystem;
    private ExecutorService threadPool;
    private byte[] buffer;
    private int bufferSize;

    private void init() {
      if (conf == null) {
        this.conf = new Configuration();
        this.threadPool = Executors.newFixedThreadPool(threads, r -> {
          Thread t = new Thread(r, "io thread");
          t.setDaemon(true);
          return t;
        });
        bufferSize = bufferSizeMiB << 20;
        buffer = new byte[bufferSize];
        try {
          fileSystem = org.apache.hadoop.fs.FileSystem.newInstance(URI.create(fs), conf);
        } catch (IOException e) {
          throw new RuntimeException(e);
        }
      }
    }

    private void write(String prefix, long fileSize, int count, int threads) throws IOException {
      int blocks = (int) ((fileSize + bufferSize - 1) / bufferSize) * threads * count;
      SynchronousQueue<Integer> queue = new SynchronousQueue<>();
      for (int i = 0; i < threads; i++) {
        String dir = prefix + "/thread-" + i;
        fileSystem.mkdirs(new org.apache.hadoop.fs.Path(dir));
        threadPool.execute(() -> {
          for (int j = 0; j < count; j++) {
            String fileName = dir + "/file-" + j;
            try (FSDataOutputStream out = fileSystem.create(new org.apache.hadoop.fs.Path(fileName), false, bufferSize)) {
              long nrRemaining;
              for (nrRemaining = fileSize; nrRemaining > 0; nrRemaining -= bufferSize) {
                int curSize = (bufferSize < nrRemaining) ? bufferSize : (int) nrRemaining;
                out.write(buffer, 0, curSize);
                queue.put(1);
              }
            } catch (IOException e) {
              e.printStackTrace();
              System.exit(1);
            } catch (InterruptedException ignored) {
            }
          }
        });
      }
      print(blocks, queue);
    }

    private void read(String prefix, long fileSize, int count, int threads) throws IOException {
      int blocks = (int) ((fileSize + bufferSize - 1) / bufferSize) * threads * count;
      SynchronousQueue<Integer> queue = new SynchronousQueue<>();
      for (int i = 0; i < threads; i++) {
        String dir = prefix + "/thread-" + i;
        threadPool.execute(() -> {
          for (int j = 0; j < count; j++) {
            String fileName = dir + "/file-" + j;
            try (FSDataInputStream in = fileSystem.open(new org.apache.hadoop.fs.Path(fileName), bufferSize)) {
              long nrRemaining;
              for (nrRemaining = fileSize; nrRemaining > 0; nrRemaining -= bufferSize) {
                int curSize = (bufferSize < nrRemaining) ? bufferSize : (int) nrRemaining;
                in.readFully(buffer, 0, curSize);
                queue.put(1);
              }
            } catch (IOException e) {
              e.printStackTrace();
              System.exit(1);
            } catch (InterruptedException ignored) {
            }
          }
        });
      }
      print(blocks, queue);
    }

    private void stat(String prefix, int count, int threads) throws IOException {
      int blocks = threads * count;
      SynchronousQueue<Integer> queue = new SynchronousQueue<>();
      for (int i = 0; i < threads; i++) {
        String dir = prefix + "/thread-" + i;
        fileSystem.mkdirs(new org.apache.hadoop.fs.Path(dir));
        threadPool.execute(() -> {
          for (int j = 0; j < count; j++) {
            String fileName = dir + "/file-" + j;
            try {
              fileSystem.getFileStatus(new org.apache.hadoop.fs.Path(fileName));
              queue.put(1);
            } catch (IOException e) {
              e.printStackTrace();
              System.exit(1);
            } catch (InterruptedException ignored) {
            }
          }
        });
      }
      print(blocks, queue);
    }

    private void print(int blocks, SynchronousQueue<Integer> queue) {
      int finished = 0;
      while (finished < blocks) {
        try {
          finished += queue.take();
        } catch (InterruptedException e) {
          return;
        }
        int ratio = finished * 100 / blocks;
        System.out.print(ratio + "%");
        for (int j = 0; j <= String.valueOf(ratio).length(); j++) {
          System.out.print("\b");
        }
      }
    }

    void writeBigFile() {
      System.out.print("writing files ");
      long cost;
      try {
        fileSystem.delete(new org.apache.hadoop.fs.Path(BIG_FILE_BASE_PATH), true);
        long start = System.currentTimeMillis();
        long fileSize = (long) bigFileSizeMiB * 1024 * 1024;
        write(BIG_FILE_BASE_PATH, fileSize, 1, threads);
        long end = System.currentTimeMillis();
        cost = end - start;
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
      System.out.printf("\rWritten %d * 1 big files (%.0f MiB): (%.1f MiB/s)%n",
              threads, bigFileSizeMiB, (threads * bigFileSizeMiB * 1000) / cost);
    }


    void readBigFile() {
      System.out.print("reading files ");
      long cost;
      try {
        long start = System.currentTimeMillis();
        long fileSize = (long) bigFileSizeMiB * 1024 * 1024;
        read(BIG_FILE_BASE_PATH, fileSize, 1, threads);
        long end = System.currentTimeMillis();
        cost = end - start;
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
      System.out.printf("\rRead %d * 1 big files (%.0f MiB): (%.1f MiB/s)%n",
              threads, bigFileSizeMiB, (threads * bigFileSizeMiB * 1000) / cost);

    }

    void writeSmallFiles() {
      System.out.print("writing small files ");
      long cost;
      try {
        fileSystem.delete(new org.apache.hadoop.fs.Path(SMALL_FILE_BASE_PATH), true);
        long start = System.currentTimeMillis();
        long fileSize = (long) (smallFileSizeMiB * 1024 * 1024);
        write(SMALL_FILE_BASE_PATH, fileSize, smallFileCount, threads);
        long end = System.currentTimeMillis();
        cost = end - start;
      } catch (IOException e) {
        throw new RuntimeException(e);
      }

      System.out.printf("\rWritten %d * %d small files (%.0f KiB): (%.1f files/s), %.2f ms for each file%n",
              threads, smallFileCount, smallFileSizeMiB * 1024, (float) (threads * smallFileCount) * 1000 / cost, (float) cost / (threads * smallFileCount));
    }

    void readSmallFiles() {
      System.out.print("reading small files ");
      long cost;
      try {
        long start = System.currentTimeMillis();
        long fileSize = (long) (smallFileSizeMiB * 1024 * 1024);
        read(SMALL_FILE_BASE_PATH, fileSize, smallFileCount, threads);
        long end = System.currentTimeMillis();
        cost = end - start;
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
      System.out.printf("\rRead %d * %d small files (%.0f KiB): (%.1f files/s), %.2f ms for each file%n",
              threads, smallFileCount, smallFileSizeMiB * 1024, (float) (threads * smallFileCount) * 1000 / cost, (float) cost / (threads * smallFileCount));
    }

    void statFiles() {
      System.out.print("stating files ");
      long cost;
      try {
        long start = System.currentTimeMillis();
        stat(SMALL_FILE_BASE_PATH, smallFileCount, threads);
        long end = System.currentTimeMillis();
        cost = end - start;
      } catch (IOException e) {
        throw new RuntimeException(e);
      }
      System.out.printf("\rStated %d * %d files: (%.1f files/s), %.2f ms for each file%n",
              threads, smallFileCount, (float) (threads * smallFileCount) * 1000 / cost, (float) cost / (threads * smallFileCount));
    }

    public void run() {
      init();
      writeBigFile();
      readBigFile();
      writeSmallFiles();
      readSmallFiles();
      statFiles();
    }

    @Override
    public String getCommand() {
      return "bench";
    }

    @Override
    public void close() throws IOException {
      threadPool.shutdown();
    }
  }

  public static void main(String[] args) throws ParseException, IOException {
    Main main = new Main();
    Command benchmark = new CommandBenchmark();
    Command showInfo = new CommandShowInfo();
    JCommander jc = JCommander.newBuilder()
            .addObject(main)
            .addCommand(benchmark.getCommand(), benchmark)
            .addCommand(showInfo.getCommand(), showInfo)
            .build();
    jc.parse(args);

    if (main.help) {
      jc.usage();
      return;
    }

    COMMAND.get(jc.getParsedCommand()).run();
    COMMAND.get(jc.getParsedCommand()).close();
  }
}
