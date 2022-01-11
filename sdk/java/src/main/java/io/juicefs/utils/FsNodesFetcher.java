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

import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.FSDataInputStream;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;

public class FsNodesFetcher extends NodesFetcher {
  private static final Log LOG = LogFactory.getLog(FsNodesFetcher.class);

  public FsNodesFetcher(String jfsName) {
    super(jfsName);
  }

  @Override
  public List<String> fetchNodes(String uri) {
    Path path = new Path(uri);
    try {
      FileSystem fs = path.getFileSystem(new Configuration());
      if (!fs.exists(path)) return null;
      FSDataInputStream inputStream = fs.open(path);
      List<String> res = new BufferedReader(new InputStreamReader(inputStream))
              .lines().collect(Collectors.toList());
      inputStream.close();
      return res;
    } catch (Throwable e) {
      LOG.warn(e.getMessage());
    }
    return null;
  }

  @Override
  protected Set<String> parseNodes(String response) throws Exception {
    return null;
  }
}
