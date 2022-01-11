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

import org.apache.flink.configuration.Configuration;
import org.apache.flink.core.fs.FileSystem;
import org.apache.flink.runtime.fs.hdfs.HadoopFileSystem;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.IOException;
import java.net.URI;

public class FlinkFileSystemFactory implements org.apache.flink.core.fs.FileSystemFactory {
  private static final Logger LOG = LoggerFactory.getLogger(FlinkFileSystemFactory.class);
  private org.apache.hadoop.conf.Configuration conf;

  private static final String[] FLINK_CONFIG_PREFIXES = {"fs.", "juicefs."};
  private String scheme;

  @Override
  public void configure(Configuration config) {
    conf = new org.apache.hadoop.conf.Configuration();
    if (config != null) {
      for (String key : config.keySet()) {
        for (String prefix : FLINK_CONFIG_PREFIXES) {
          if (key.startsWith(prefix)) {
            String value = config.getString(key, null);
            if (value != null) {
              if ("io.juicefs.JuiceFileSystem".equals(value.trim())) {
                this.scheme = key.split("\\.")[1];
              }
              conf.set(key, value);
            }
          }
        }
      }
    }
  }

  @Override
  public String getScheme() {
    if (scheme == null) {
      return "jfs";
    }
    return scheme;
  }

  @Override
  public FileSystem create(URI fsUri) throws IOException {
    JuiceFileSystem fs = new JuiceFileSystem();
    fs.initialize(fsUri, conf);
    return new HadoopFileSystem(fs);
  }
}
