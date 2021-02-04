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
