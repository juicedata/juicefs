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

package io.juicefs.permission;

public class RangerConfig {

  public RangerConfig(String rangerRestUrl, String serviceName, String cacheDir, String pollIntervalMs) {
    this.rangerRestUrl = rangerRestUrl;
    this.serviceName = serviceName;
    this.pollIntervalMs = pollIntervalMs;
    this.cacheDir = cacheDir;
  }

  private String rangerRestUrl;

  private String serviceName;

  private String pollIntervalMs = "30000";

  private String cacheDir;


  public String getRangerRestUrl() {
    return rangerRestUrl;
  }

  public void setRangerRestUrl(String rangerRestUrl) {
    this.rangerRestUrl = rangerRestUrl;
  }

  public String getServiceName() {
    return serviceName;
  }

  public void setServiceName(String serviceName) {
    this.serviceName = serviceName;
  }


  public String getCacheDir() {
    return cacheDir;
  }

  public void setCacheDir(String cacheDir) {
    this.cacheDir = cacheDir;
  }

  public String getPollIntervalMs() {
    return pollIntervalMs;
  }

  public void setPollIntervalMs(String pollIntervalMs) {
    this.pollIntervalMs = pollIntervalMs;
  }

}
