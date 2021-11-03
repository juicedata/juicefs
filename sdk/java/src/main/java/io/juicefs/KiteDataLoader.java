/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
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

import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.kitesdk.data.DatasetIOException;
import org.kitesdk.data.DatasetOperationException;
import org.kitesdk.data.spi.*;
import org.kitesdk.data.spi.filesystem.FileSystemDatasetRepository;

import java.io.IOException;
import java.net.URI;
import java.net.URISyntaxException;
import java.util.Map;

public class KiteDataLoader implements Loadable {
  private static class URIBuilder implements OptionBuilder<DatasetRepository> {

    @Override
    public DatasetRepository getFromOptions(Map<String, String> match) {
      String path = match.get("path");
      final Path root = (path == null || path.isEmpty()) ?
              new Path("/") : new Path("/", path);

      Configuration conf = DefaultConfiguration.get();
      FileSystem fs;
      try {
        fs = FileSystem.get(fileSystemURI(match), conf);
      } catch (IOException e) {
        throw new DatasetIOException("Could not get a FileSystem", e);
      }
      return new FileSystemDatasetRepository.Builder()
              .configuration(new Configuration(conf)) // make a modifiable copy
              .rootDirectory(fs.makeQualified(root))
              .build();
    }
  }

  @Override
  public void load() {
    try {
      // load hdfs-site.xml by loading HdfsConfiguration
      FileSystem.getLocal(DefaultConfiguration.get());
    } catch (IOException e) {
      throw new DatasetIOException("Cannot load default config", e);
    }

    OptionBuilder<DatasetRepository> builder = new URIBuilder();
    Registration.register(
            new URIPattern("jfs:/*path"),
            new URIPattern("jfs:/*path/:namespace/:dataset"),
            builder);
  }

  private static URI fileSystemURI(Map<String, String> match) {
    try {
      return new URI(match.get(URIPattern.SCHEME), null,
              match.get(URIPattern.HOST), -1, "/", null, null);
    } catch (URISyntaxException ex) {
      throw new DatasetOperationException("[BUG] Could not build FS URI", ex);
    }
  }
}
