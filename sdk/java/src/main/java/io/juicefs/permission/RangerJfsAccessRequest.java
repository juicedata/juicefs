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

import org.apache.ranger.plugin.policyengine.RangerAccessRequestImpl;

import java.util.Date;
import java.util.Set;

class RangerJfsAccessRequest extends RangerAccessRequestImpl {

  RangerJfsAccessRequest(String path, String pathOwner, String accessType, String action, String user,
                         Set<String> groups) {
    setResource(new RangerJfsResource(path, pathOwner));
    setAccessType(accessType);
    setUser(user);
    setUserGroups(groups);
    setAccessTime(new Date());
    setAction(action);
    setForwardedAddresses(null);
  }

}
