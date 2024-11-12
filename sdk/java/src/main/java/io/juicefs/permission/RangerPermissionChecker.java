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

import com.google.common.collect.Sets;
import io.juicefs.JuiceFileSystemImpl;
import org.apache.commons.lang.StringUtils;
import org.apache.hadoop.fs.FileStatus;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.fs.permission.FsAction;
import org.apache.hadoop.security.AccessControlException;
import org.apache.ranger.authorization.hadoop.config.RangerPluginConfig;
import org.apache.ranger.plugin.policyengine.RangerAccessResult;
import org.apache.ranger.plugin.policyengine.RangerPolicyEngineOptions;
import org.apache.ranger.plugin.service.RangerBasePlugin;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.FileNotFoundException;
import java.io.IOException;
import java.util.*;
import java.util.stream.Collectors;

/**
 * for auth checker
 *
 * @author ming.li2
 **/
public class RangerPermissionChecker {

  private static final Logger LOG = LoggerFactory.getLogger(RangerPermissionChecker.class);

  private final HashMap<FsAction, Set<String>> fsAction2ActionMapper = new HashMap<FsAction, Set<String>>() {
    {
      put(FsAction.NONE, new HashSet<>());
      put(FsAction.ALL, Sets.newHashSet("read", "write", "execute"));
      put(FsAction.READ, Sets.newHashSet("read"));
      put(FsAction.READ_WRITE, Sets.newHashSet("read", "write"));
      put(FsAction.READ_EXECUTE, Sets.newHashSet("read", "execute"));
      put(FsAction.WRITE, Sets.newHashSet("write"));
      put(FsAction.WRITE_EXECUTE, Sets.newHashSet("write", "execute"));
      put(FsAction.EXECUTE, Sets.newHashSet("execute"));
    }
  };

  private final JuiceFileSystemImpl superGroupFileSystem;

  private final String name;

  private final String scheme;

  private final String user;

  private final Set<String> groups;

  private final String rangerCacheDir;

  private final RangerBasePlugin rangerPlugin;

  private static final String RANGER_SERVICE_TYPE = "hdfs";

  public RangerPermissionChecker(JuiceFileSystemImpl superGroupFileSystem, RangerConfig config, String scheme,
                                 String name, String user, String group) {
    this.superGroupFileSystem = superGroupFileSystem;
    this.name = name;
    this.scheme = scheme;
    this.user = user;
    this.groups = Arrays.stream(group.split(",")).collect(Collectors.toSet());

    this.rangerCacheDir = config.getCacheDir();
    boolean startRangerRefresher = LockFileChecker.checkAndCreateLockFile(rangerCacheDir);

    RangerPluginConfig rangerPluginContext = buildRangerPluginContext(RANGER_SERVICE_TYPE, config.getServiceName(), startRangerRefresher);
    rangerPlugin = new RangerBasePlugin(rangerPluginContext);
    rangerPlugin.getConfig().set("ranger.plugin.hdfs.policy.cache.dir", this.rangerCacheDir);
    rangerPlugin.getConfig().set("ranger.plugin.hdfs.service.name", config.getServiceName());
    rangerPlugin.getConfig().set("ranger.plugin.hdfs.policy.rest.url", config.getRangerRestUrl());
    rangerPlugin.init();
  }

  protected RangerPolicyEngineOptions buildRangerPolicyEngineOptions(boolean startRangerRefresher) {
    if (startRangerRefresher) {
      return null;
    }
    LOG.info("Other JuiceFS Client is refreshing ranger policy, will close the refresher here.");
    RangerPolicyEngineOptions options = new RangerPolicyEngineOptions();
    options.disablePolicyRefresher = true;
    return options;
  }

  protected RangerPluginConfig buildRangerPluginContext(String serviceType, String serviceName, boolean startRangerRefresher) {
    return new RangerPluginConfig(serviceType, serviceName, serviceName,
        null, null, buildRangerPolicyEngineOptions(startRangerRefresher));
  }

  public void checkPermission(Path path, boolean checkOwner, FsAction ancestorAccess, FsAction parentAccess,
                              FsAction access, String operationName) throws IOException {
    RangerPermissionContext context = new RangerPermissionContext(user, groups, operationName);
    PathObj obj = path2Obj(path);

    if (obj.parent.getPermission().getStickyBit() && access != null && parentAccess != null
        && parentAccess.implies(FsAction.WRITE) && obj.parent != null && obj.current != null) {
      if (!StringUtils.equals(obj.parent.getOwner(), user) && !StringUtils.equals(obj.current.getOwner(), user)) {
        throw new AccessControlException(
            assembleExceptionMessage(user, access.toString(), toPathString(obj.current.getPath())));
      }
    }

    if (ancestorAccess != null && obj.ancestor != null) {
      if (!isAccessAllowed(obj.ancestor, ancestorAccess, context)) {
        throw new AccessControlException(
            assembleExceptionMessage(user, ancestorAccess.toString(), toPathString(obj.ancestor.getPath())));
      }
    }

    if (parentAccess != null && obj.parent != null) {
      if (!isAccessAllowed(obj.parent, parentAccess, context)) {
        throw new AccessControlException(
            assembleExceptionMessage(user, parentAccess.toString(), toPathString(obj.parent.getPath())));
      }
    }

    if (access != null && obj.current != null) {
      if (!isAccessAllowed(obj.current, access, context)) {
        throw new AccessControlException(
            assembleExceptionMessage(user, access.toString(), toPathString(obj.current.getPath())));
      }
    }

    if (checkOwner) {
      String owner = null;
      if (obj.current != null) {
        owner = obj.current.getOwner();
      }
      if (!user.equals(owner)) {
        throw new AccessControlException(
            assembleExceptionMessage(user, getFirstNonNullAccess(ancestorAccess, parentAccess, access),
                toPathString(obj.current.getPath())));
      }
    }
  }

  public void cleanUp() {
    try {
      rangerPlugin.cleanup();
    } catch (Exception e) {
      LOG.warn("Error when clean up ranger plugin threads.", e);
    }
    LockFileChecker.cleanUp(rangerCacheDir);
  }

  private static String assembleExceptionMessage(String user, String action, String path) {
    return "Permission denied: user=" + user + ", access=" + action + ", path=\"" + path + "\"";
  }

  private static String getFirstNonNullAccess(FsAction ancestorAccess, FsAction parentAccess, FsAction access) {
    if (access != null) {
      return access.toString();
    }
    if (parentAccess != null) {
      return parentAccess.toString();
    }
    if (ancestorAccess != null) {
      return ancestorAccess.toString();
    }
    return FsAction.EXECUTE.toString();
  }

  private boolean isAccessAllowed(FileStatus file, FsAction access, RangerPermissionContext context) {

    String path = toPathString(file.getPath());
    String schemePath = scheme + "://" + name + path;
    Set<String> accessTypes = fsAction2ActionMapper.getOrDefault(access, new HashSet<>());
    String pathOwner = file.getOwner();
    for (String accessType : accessTypes) {
      RangerJfsAccessRequest request = new RangerJfsAccessRequest(schemePath, pathOwner, accessType,
          context.operationName, user, context.userGroups);
      LOG.debug(request.toString());
      boolean tmpRes = false;
      try {
        RangerAccessResult result = rangerPlugin.isAccessAllowed(request);
        tmpRes = result.getIsAllowed();
        LOG.debug(result.toString());
      } catch (Throwable e) {
        throw new RuntimeException("Check Permission Error. ", e);
      }
      if (!tmpRes) {
        return false;
      }
    }
    return true;
  }

  private static String toPathString(Path path) {
    return path.toUri().getPath();
  }

  private PathObj path2Obj(Path path) throws IOException {

    FileStatus current = getIfExist(path);
    FileStatus parent = getIfExist(path.getParent());
    FileStatus ancestor = null;
    if (parent != null) {
      ancestor = parent;
    } else {
      path = path.getParent();
      FileStatus tmp = null;
      while (path != null && tmp == null) {
        tmp = getIfExist(path);
        path = path.getParent();
      }
      ancestor = tmp;
    }

    return new PathObj(ancestor, parent, current);
  }

  private FileStatus getIfExist(Path path) throws IOException {
    try {
      if (path != null) {
        return superGroupFileSystem.getFileStatus(path);
      }
    } catch (FileNotFoundException ignored) {
    }
    return null;
  }

  public static class PathObj {

    FileStatus ancestor = null;

    FileStatus parent = null;

    FileStatus current = null;

    public PathObj(FileStatus ancestor, FileStatus parent, FileStatus current) {
      this.ancestor = ancestor;
      this.parent = parent;
      this.current = current;
    }
  }

}
