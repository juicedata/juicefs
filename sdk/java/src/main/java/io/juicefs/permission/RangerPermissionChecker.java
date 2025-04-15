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

import com.google.common.collect.Sets;
import org.apache.commons.lang.StringUtils;
import org.apache.hadoop.fs.FileStatus;
import org.apache.hadoop.fs.FileSystem;
import org.apache.hadoop.fs.Path;
import org.apache.hadoop.fs.permission.FsAction;
import org.apache.hadoop.security.AccessControlException;
import org.apache.ranger.plugin.policyengine.RangerAccessResult;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.io.FileNotFoundException;
import java.io.IOException;
import java.util.HashMap;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.ConcurrentHashMap;

/**
 * for auth checker
 *
 * @author ming.li2
 **/
public class RangerPermissionChecker {

  private static final Logger LOG = LoggerFactory.getLogger(RangerPermissionChecker.class);

  private static final Map<String, RangerPermissionChecker> pcs = new ConcurrentHashMap<>();
  private static final Map<String, Set<Long>> runningInstance = new HashMap<>();

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

  private final FileSystem superGroupFileSystem;
  private final RangerJfsPlugin rangerPlugin;

  private RangerPermissionChecker(FileSystem superGroupFileSystem, RangerConfig config) {
    this.superGroupFileSystem = superGroupFileSystem;
    rangerPlugin = new RangerJfsPlugin(superGroupFileSystem, config.getServiceName(), config.getRangerRestUrl(), config.getPollIntervalMs());
    rangerPlugin.getConfig().set("ranger.plugin.hdfs.service.name", config.getServiceName());
    rangerPlugin.getConfig().set("ranger.plugin.hdfs.policy.rest.url", config.getRangerRestUrl());
    rangerPlugin.getConfig().setIsFallbackSupported(true);
    rangerPlugin.init();
  }

  public static RangerPermissionChecker acquire(String volName, long handle, FileSystem superGroupFileSystem, RangerConfig config) throws IOException {
    synchronized (runningInstance) {
      if (!runningInstance.containsKey(volName)) {
        if (pcs.containsKey(volName)) {
          throw new IOException("RangerPermissionChecker for volume: " + volName + " is already created, but no running instance found.");
        }
        RangerPermissionChecker pc = new RangerPermissionChecker(superGroupFileSystem, config);
        pcs.put(volName, pc);
        Set<Long> handles = new HashSet<>();
        handles.add(handle);
        runningInstance.put(volName, handles);
        return pc;
      } else {
        RangerPermissionChecker pc = pcs.get(volName);
        if (pc == null) {
          throw new IOException("RangerPermissionChecker for volume: " + volName + " is already created, but no instance found.");
        }
        runningInstance.get(volName).add(handle);
        return pc;
      }
    }
  }

  public static void release(String volName, long handle) {
    if (handle <= 0) {
      return;
    }
    synchronized (runningInstance) {
      if (!runningInstance.containsKey(volName)) {
        return;
      }
      Set<Long> handles = runningInstance.get(volName);
      boolean removed = handles.remove(handle);
      if (!removed) {
        return;
      }
      if (handles.size() == 0) {
        RangerPermissionChecker pc = pcs.remove(volName);
        pc.cleanUp();
        runningInstance.remove(volName);
      }
    }
  }

  public boolean checkPermission(Path path, boolean checkOwner, FsAction ancestorAccess, FsAction parentAccess,
                                 FsAction access, String operationName, String user, Set<String> groups) throws IOException {
    RangerPermissionContext context = new RangerPermissionContext(user, groups, operationName);
    PathObj obj = path2Obj(path);

    boolean fallback = true;
    AuthzStatus authzStatus = AuthzStatus.ALLOW;

    if (access != null && parentAccess != null
        && parentAccess.implies(FsAction.WRITE) && obj.parent != null && obj.current != null && obj.parent.getPermission().getStickyBit()) {
      if (!StringUtils.equals(obj.parent.getOwner(), user) && !StringUtils.equals(obj.current.getOwner(), user)) {
        authzStatus = AuthzStatus.NOT_DETERMINED;
      }
    }

    if (authzStatus == AuthzStatus.ALLOW && ancestorAccess != null && obj.ancestor != null) {
      authzStatus = isAccessAllowed(obj.ancestor, ancestorAccess, context);
      if (checkResult(authzStatus, user, ancestorAccess.toString(), toPathString(obj.ancestor.getPath()))) {
        return fallback;
      }
    }

    if (authzStatus == AuthzStatus.ALLOW && parentAccess != null && obj.parent != null) {
      authzStatus = isAccessAllowed(obj.parent, parentAccess, context);
      if (checkResult(authzStatus, user, parentAccess.toString(), toPathString(obj.parent.getPath()))) {
        return fallback;
      }
    }

    if (authzStatus == AuthzStatus.ALLOW && access != null && obj.current != null) {
      authzStatus = isAccessAllowed(obj.current, access, context);
      if (checkResult(authzStatus, user, access.toString(), toPathString(obj.current.getPath()))) {
        return fallback;
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
    // check access by ranger success
    return !fallback;
  }

  public void cleanUp() {
    try {
      rangerPlugin.cleanup();
    } catch (Exception e) {
      LOG.warn("Error when clean up ranger plugin threads.", e);
    }
    try {
      superGroupFileSystem.close();
    } catch (Exception e) {
      LOG.warn("Error when close super group file system.", e);
    }
  }

  private static boolean checkResult(AuthzStatus authzStatus, String user, String action, String path) throws AccessControlException {
    if (authzStatus == AuthzStatus.DENY) {
      throw new AccessControlException(assembleExceptionMessage(user, action, path));
    } else {
      return authzStatus == AuthzStatus.NOT_DETERMINED;
    }
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

  private AuthzStatus isAccessAllowed(FileStatus file, FsAction access, RangerPermissionContext context) {
    String path = toPathString(file.getPath());
    Set<String> accessTypes = fsAction2ActionMapper.getOrDefault(access, new HashSet<>());
    String pathOwner = file.getOwner();
    AuthzStatus authzStatus = null;
    for (String accessType : accessTypes) {
      RangerJfsAccessRequest request = new RangerJfsAccessRequest(path, pathOwner, accessType, context.operationName, context.user, context.userGroups);
      LOG.debug(request.toString());
      RangerAccessResult result = rangerPlugin.isAccessAllowed(request);
      if (result != null) {
        LOG.debug(result.toString());
      }
      if (result == null || !result.getIsAccessDetermined()) {
        authzStatus = AuthzStatus.NOT_DETERMINED;
      } else if (!result.getIsAllowed()) {
        authzStatus = AuthzStatus.DENY;
        break;
      } else {
        if (!AuthzStatus.NOT_DETERMINED.equals(authzStatus)) {
          authzStatus = AuthzStatus.ALLOW;
        }
      }

    }
    if (authzStatus == null) {
      authzStatus = AuthzStatus.NOT_DETERMINED;
    }
    return authzStatus;
  }

  private enum AuthzStatus {ALLOW, DENY, NOT_DETERMINED}

  ;

  private static String toPathString(Path path) {
    return path.toUri().getPath();
  }

  private PathObj path2Obj(Path path) throws IOException {

    FileStatus current = getIfExist(path);
    FileStatus parent = getIfExist(path.getParent());
    FileStatus ancestor = getAncestor(path);

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

  public FileStatus getAncestor(Path path) throws IOException {
    if (path.getParent() != null) {
      return getIfExist(path.getParent());
    }
    path = path.getParent();
    FileStatus tmp = null;
    while (path != null && tmp == null) {
      tmp = getIfExist(path);
      path = path.getParent();
    }
    return tmp;
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
