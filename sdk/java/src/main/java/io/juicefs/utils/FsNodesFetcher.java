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

package io.juicefs.utils;

import io.juicefs.JuiceFileSystem;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FSDataInputStream;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;

public class FsNodesFetcher extends NodesFetcher {
  private static final Logger LOG = LoggerFactory.getLogger(FsNodesFetcher.class);

  private FileSystem jfs;
  private Configuration conf;

  public FsNodesFetcher(String jfsName, FileSystem jfs) {
    super(jfsName);
    this.jfs = jfs;
    this.conf = jfs.getConf();
  }

  @Override
  public List<String> fetchNodes(String uri) {
    FileSystem fs;

    Path path = new Path(uri);
    String scheme = path.toUri().getScheme();
    if (scheme == null) {
      scheme = FileSystem.getDefaultUri(conf).getScheme();
    }
    if (scheme != null) {
      try {
        String classStr = conf.getTrimmed("fs." + scheme + ".impl");
        if (JuiceFileSystem.class.getName().equals(classStr)) {
          fs = jfs;
        } else {
          fs = path.getFileSystem(conf);
        }
        if (!fs.exists(path)) {
          return null;
        }
        FSDataInputStream inputStream = fs.open(path);
        List<String> res = new BufferedReader(new InputStreamReader(inputStream))
            .lines().collect(Collectors.toList());
        inputStream.close();
        return res;
      } catch (Exception e) {
        LOG.warn("fetch nodes from {} failed", uri, e);
        return null;
      }
    }
    return null;
  }

  @Override
  protected Set<String> parseNodes(String response) throws Exception {
    return null;
  }
}
