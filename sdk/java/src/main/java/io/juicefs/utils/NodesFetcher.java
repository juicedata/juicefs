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

import java.io.*;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.util.ArrayList;
import java.util.List;
import java.util.Set;
import java.util.stream.Collectors;

/**
 * fetch calculate nodes of the cluster
 */
public abstract class NodesFetcher {
  private static final Log LOG = LogFactory.getLog(NodesFetcher.class);

  protected File cacheFolder = new File("/tmp/.juicefs");
  protected File cacheFile;
  private String jfsName;

  public NodesFetcher(String jfsName) {
    this.jfsName = jfsName;
    if (!cacheFolder.exists()) {
      cacheFolder.mkdirs();
    }
    cacheFile = new File(cacheFolder, jfsName + ".nodes");
    cacheFolder.setWritable(true, false);
    cacheFolder.setReadable(true, false);
    cacheFolder.setExecutable(true, false);
    cacheFile.setWritable(true, false);
    cacheFile.setReadable(true, false);
    cacheFile.setExecutable(true, false);
  }

  public List<String> fetchNodes(String urls) {
    List<String> result = readCache();

    // refresh local disk cache every 10 mins
    long duration = System.currentTimeMillis() - cacheFile.lastModified();
    if (duration > 10 * 60 * 1000L || result == null) {
      Set<String> nodes = getNodes(urls.split(","));
      if (nodes == null) return result;
      result = new ArrayList<>(nodes);
      cache(result);
    }

    return result;
  }

  public List<String> readCache() {
    try {
      if (!cacheFile.exists()) return null;
      return Files.readAllLines(cacheFile.toPath());
    } catch (IOException e) {
      LOG.warn("read cache failed due to: ", e);
      return null;
    }
  }

  public void cache(List<String> hostnames) {
    File tmpFile = new File(cacheFolder, System.getProperty("user.name") + "-" + jfsName + ".nodes.tmp");
    try (RandomAccessFile writer = new RandomAccessFile(tmpFile, "rws")) {
      tmpFile.setWritable(true, false);
      tmpFile.setReadable(true, false);
      if (hostnames != null) {
        String content = String.join("\n", hostnames);
        writer.write(content.getBytes());
      }
      tmpFile.renameTo(cacheFile);
    } catch (IOException e) {
      LOG.warn("wirte cache failed due to: ", e);
    }
  }

  public Set<String> getNodes(String[] urls) {
    if (urls == null) {
      return null;
    }
    for (String url : urls) {
      try {
        String response = doGet(url);
        if (response == null) {
          continue;
        }
        return parseNodes(response);
      } catch (Throwable e) {
        LOG.warn("fetch from:" + url + " failed, switch to another url", e);
      }
    }
    return null;
  }

  protected abstract Set<String> parseNodes(String response) throws Exception;

  protected String doGet(String url) {
    int timeout = 3; // seconds

    HttpURLConnection con = null;
    try {
      con = (HttpURLConnection) new URL(url).openConnection();
      con.setConnectTimeout(timeout * 1000);
      con.setReadTimeout(timeout * 1000);

      int status = con.getResponseCode();
      if (status != 200) return null;

      BufferedReader in = new BufferedReader(
              new InputStreamReader(con.getInputStream(), StandardCharsets.UTF_8));
      String content = in.lines().collect(Collectors.joining("\n"));
      in.close();
      return content;
    } catch (IOException e) {
      LOG.warn(e);
      return null;
    } finally {
      if (con != null) {
        con.disconnect();
      }
    }
  }
}
