/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
import org.apache.hadoop.fs.*;
import org.apache.ranger.admin.client.RangerAdminClient;
import org.apache.ranger.plugin.contextenricher.RangerTagEnricher;
import org.apache.ranger.plugin.service.RangerBasePlugin;
import org.apache.ranger.plugin.util.RangerRoles;
import org.apache.ranger.plugin.util.RangerServiceNotFoundException;
import org.apache.ranger.plugin.util.ServicePolicies;
import org.apache.ranger.plugin.util.ServiceTags;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.FileNotFoundException;
import java.io.IOException;
import java.net.URI;
import java.util.Arrays;
import java.util.Comparator;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;


public class RangerAdminRefresher {
  private static final Logger LOG = LoggerFactory.getLogger(RangerAdminRefresher.class);

  private static final String JFS_RANGER_DIR = "/.sys/ranger";

  private RangerBasePlugin plugIn;
  private Path rangerDir;
  private Path rangerRulePath;
  private long lastMtime;
  private final long pollingIntervalMs;

  private final RangerAdminClient rangerAdmin;
  private final Gson gson = new GsonBuilder().setDateFormat("yyyyMMdd-HH:mm:ss.SSS-Z").create();
  private long lastKnownPolicyVersion = -1L;
  private long lastPolicyActivationTimeInMillis;
  private long lastKnownRoleVersion = -1L;
  private long lastRoleActivationTimeInMillis;
  private long lastKnownTagVersion = -1L;
  private long lastTagActivationTimeInMillis;

  private final FileSystem fs;
  private final ScheduledExecutorService refreshThread;

  public RangerAdminRefresher(RangerBasePlugin plugIn, RangerAdminClient rangerAdmin, FileSystem fs, String rangerUrl, long pollingIntervalMs) {

    this.plugIn = plugIn;
    this.rangerAdmin = rangerAdmin;
    this.fs = fs;
    String serviceName = plugIn.getServiceName();
    URI uri = URI.create(rangerUrl);
    String rangerDirName = uri.getHost().replace(".", "_") + "_" + uri.getPort() + "_" + serviceName;
    this.rangerDir = new Path(JFS_RANGER_DIR, rangerDirName);
    this.rangerRulePath = new Path(rangerDir, "rules");
    this.refreshThread = Executors.newScheduledThreadPool(1, r -> {
      Thread t = new Thread(r, "JuiceFS Ranger Refresher");
      t.setDaemon(true);
      return t;
    });
    this.pollingIntervalMs = pollingIntervalMs;
  }

  public void start() {
    loadRangerItem();
    refreshThread.scheduleAtFixedRate(this::loadRangerItem, pollingIntervalMs, pollingIntervalMs, TimeUnit.MILLISECONDS);
  }

  /**
   * 1. read rules from jfs
   * 2. choose one client to check ranger admin, if updated, download and save rules to jfs
   */
  public void loadRangerItem() {
    RangerRules rangerRules = null;
    // try to load rules from jfs
    try {
      rangerRules = loadRangerRules();
    } catch (IOException e) {
      LOG.debug("Load ranger rules failed", e);
    }

    if (rangerRules != null) {
      if (updateRules(rangerRules.getPolicies(), rangerRules.getTags(), rangerRules.getRoles())) {
        LOG.info("Ranger rules has been updated, use new rules from juicefs");
      }
    }

    boolean checkUpdate = checkUpdate(pollingIntervalMs);
    // load rules from ranger admin
    if (rangerRules == null || checkUpdate) {
      ServicePolicies policiesFromRanger = null;
      ServiceTags tagsFromRanger = null;
      RangerRoles rolesFromRanger = null;
      try {
        policiesFromRanger = rangerAdmin.getServicePoliciesIfUpdated(lastKnownPolicyVersion, lastPolicyActivationTimeInMillis);
        tagsFromRanger = rangerAdmin.getServiceTagsIfUpdated(lastKnownTagVersion, lastTagActivationTimeInMillis);
        rolesFromRanger = rangerAdmin.getRolesIfUpdated(lastKnownRoleVersion, lastRoleActivationTimeInMillis);
      } catch (RangerServiceNotFoundException e) {
        LOG.warn("Ranger service not found", e);
      } catch (Exception e) {
        LOG.warn("Load policies from ranger failed", e);
      }
      if (updateRules(policiesFromRanger, tagsFromRanger, rolesFromRanger)) {
        if (checkUpdate) {
          try {
            ServicePolicies p = rangerRules != null ? rangerRules.getPolicies() : null;
            ServiceTags t = rangerRules != null ? rangerRules.getTags() : null;
            RangerRoles r = rangerRules != null ? rangerRules.getRoles() : null;
            if (policiesFromRanger != null) {
              LOG.info("ServicePolicies updated from Ranger Admin");
              p = policiesFromRanger;
            }
            if (tagsFromRanger != null) {
              LOG.info("ServiceTags updated from Ranger Admin");
              t = tagsFromRanger;
            }
            if (rolesFromRanger != null) {
              LOG.info("RangerRoles updated from Ranger Admin");
              r = rolesFromRanger;
            }
            saveRangerRules(new RangerRules(p, t, r));
          } catch (IOException e) {
            LOG.warn("Save rules to juicefs failed", e);
          }
        }
      }
    }
  }

  private boolean checkUpdate(long pollingIntervalMs) {
    try {
      boolean exists = fs.exists(rangerDir);
      if (!exists) {
        fs.mkdirs(rangerDir);
      }
      FileStatus[] lockFiles = fs.listStatus(rangerDir, path -> {
        String name = path.getName();
        return name.endsWith(".lock");
      });
      String prefix = String.valueOf((System.currentTimeMillis() / pollingIntervalMs) * pollingIntervalMs);
      Path lockPath = new Path(rangerDir, prefix + ".lock");
      if (lockFiles == null || lockFiles.length == 0) {
        try (FSDataOutputStream ignore = fs.create(lockPath, false)) {
          return true;
        }
      } else {
        if (lockFiles.length > 1) {
          Arrays.sort(lockFiles, Comparator.comparing(o -> o.getPath().getName()));
        }
        if (lockFiles[lockFiles.length - 1].getPath().getName().compareTo(lockPath.getName()) >= 0) {
          return false;
        }
        try (FSDataOutputStream ignore = fs.create(lockPath, false)) {
          for (FileStatus lockFile : lockFiles) {
            fs.delete(lockFile.getPath(), false);
          }
          return true;
        }
      }
    } catch (FileAlreadyExistsException ignored) {
      return false;
    }
    catch (IOException e) {
      LOG.warn("Check update failed", e);
      return false;
    }
  }

  private void saveRangerRules(RangerRules rules) throws IOException {
    String rulesJson = gson.toJson(rules, RangerRules.class);
    byte[] bytes = rulesJson.getBytes();
    try (FSDataOutputStream out = fs.create(rangerRulePath)) {
      out.write(bytes);
    } catch (FileNotFoundException e) {
      fs.mkdirs(rangerRulePath.getParent());
      try (FSDataOutputStream out = fs.create(rangerRulePath)) {
        out.write(bytes);
      }
    }
  }

  private RangerRules loadRangerRules() throws IOException {
    FileStatus fileStatus = fs.getFileStatus(rangerRulePath);
    long mtime = fileStatus.getModificationTime();
    if (lastMtime == mtime) {
      return null;
    }
    try (FSDataInputStream in = fs.open(rangerRulePath)) {
      byte[] bytes = new byte[(int) fileStatus.getLen()];
      in.readFully(bytes);
      String rulesJson = new String(bytes);
      RangerRules rangerRules = gson.fromJson(rulesJson, RangerRules.class);
      lastMtime = mtime;
      return rangerRules;
    }
  }

  private boolean updateRules(ServicePolicies newSvcPolicies, ServiceTags newTags, RangerRoles newRangerRoles) {
    boolean updated = false;
    if (newSvcPolicies != null) {
      long policyVersion = newSvcPolicies.getPolicyVersion() == null ? -1 : newSvcPolicies.getPolicyVersion();
      if (lastKnownPolicyVersion != policyVersion) {
        plugIn.setPolicies(newSvcPolicies);
        lastKnownPolicyVersion = policyVersion;
        lastPolicyActivationTimeInMillis = System.currentTimeMillis();
        updated = true;
      }
    }
    if (newTags != null) {
      long tagVersion = newTags.getTagVersion() == null ? -1 : newTags.getTagVersion();
      if (lastKnownTagVersion != tagVersion) {
        RangerTagEnricher tagEnricher = plugIn.getTagEnricher();
        if (tagEnricher != null) {
          tagEnricher.setServiceTags(newTags);
        }
        lastKnownTagVersion = tagVersion;
        lastTagActivationTimeInMillis = System.currentTimeMillis();
        updated = true;
      }
    }
    if (newRangerRoles != null) {
      long roleVersion = newRangerRoles.getRoleVersion() == null ? -1 : newRangerRoles.getRoleVersion();
      if (lastKnownRoleVersion != roleVersion) {
        plugIn.setRoles(newRangerRoles);
        lastKnownRoleVersion = roleVersion;
        lastRoleActivationTimeInMillis = System.currentTimeMillis();
        updated = true;
      }
    }
    return updated;
  }

  public void stop() {
    refreshThread.shutdownNow();
  }
}
