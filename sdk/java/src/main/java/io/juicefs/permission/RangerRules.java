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

import org.apache.ranger.plugin.util.RangerRoles;
import org.apache.ranger.plugin.util.ServicePolicies;
import org.apache.ranger.plugin.util.ServiceTags;

import java.io.Serializable;

public class RangerRules implements Serializable {
  private ServicePolicies policies;
  private ServiceTags tags;
  private RangerRoles roles;

  public RangerRules() {
  }

  public RangerRules(ServicePolicies policies, ServiceTags tags, RangerRoles roles) {
    this.policies = policies;
    this.tags = tags;
    this.roles = roles;
  }

  public ServicePolicies getPolicies() {
    return policies;
  }

  public void setPolicies(ServicePolicies policies) {
    this.policies = policies;
  }

  public ServiceTags getTags() {
    return tags;
  }

  public void setTags(ServiceTags tags) {
    this.tags = tags;
  }

  public RangerRoles getRoles() {
    return roles;
  }

  public void setRoles(RangerRoles roles) {
    this.roles = roles;
  }
}
