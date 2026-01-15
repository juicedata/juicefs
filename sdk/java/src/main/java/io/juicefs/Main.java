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
package io.juicefs;

import com.beust.jcommander.JCommander;
import com.beust.jcommander.Parameter;
import io.juicefs.bench.NNBench;
import io.juicefs.bench.TestDFSIO;
import io.juicefs.tools.RangerDownloader;
import org.apache.commons.cli.ParseException;

import java.io.Closeable;
import java.io.IOException;
import java.util.HashMap;
import java.util.Map;

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

  public static void main(String[] args) throws ParseException, IOException {
    Main main = new Main();
    Command dfsio = new TestDFSIO();
    Command nnbench = new NNBench();
    Command ranger = new RangerDownloader();
    JCommander jc = JCommander.newBuilder()
        .addObject(main)
        .addCommand(dfsio.getCommand(), dfsio)
        .addCommand(nnbench.getCommand(), nnbench)
        .addCommand(ranger.getCommand(), ranger)
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
