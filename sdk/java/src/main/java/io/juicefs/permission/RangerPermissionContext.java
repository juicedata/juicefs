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

import java.util.Set;

public class RangerPermissionContext {

  public final String user;

  public final Set<String> userGroups;

  public final String operationName;

  public RangerPermissionContext(String user, Set<String> groups, String operationName) {
    this.user = user;
    this.userGroups = groups;
    this.operationName = operationName;
  }

}
