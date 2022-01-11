/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
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
