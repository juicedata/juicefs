/**
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 * <p>
 * http://www.apache.org/licenses/LICENSE-2.0
 * <p>
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package io.juicefs.utils;

import com.google.common.collect.ComparisonChain;
import com.google.common.collect.Lists;
import com.google.common.collect.Maps;
import com.google.common.collect.Ordering;
import org.apache.hadoop.fs.permission.*;

import java.io.IOException;
import java.util.*;

import static org.apache.hadoop.fs.permission.AclEntryScope.ACCESS;
import static org.apache.hadoop.fs.permission.AclEntryScope.DEFAULT;
import static org.apache.hadoop.fs.permission.AclEntryType.*;

/**
 * AclTransformation defines the operations that can modify an ACL.  All ACL
 * modifications take as input an existing ACL and apply logic to add new
 * entries, modify existing entries or remove old entries.  Some operations also
 * accept an ACL spec: a list of entries that further describes the requested
 * change.  Different operations interpret the ACL spec differently.  In the
 * case of adding an ACL to an inode that previously did not have one, the
 * existing ACL can be a "minimal ACL" containing exactly 3 entries for owner,
 * group and other, all derived from the {@link FsPermission} bits.
 * <p>
 * The algorithms implemented here require sorted lists of ACL entries.  For any
 * existing ACL, it is assumed that the entries are sorted.  This is because all
 * ACL creation and modification is intended to go through these methods, and
 * they all guarantee correct sort order in their outputs.  However, an ACL spec
 * is considered untrusted user input, so all operations pre-sort the ACL spec as
 * the first step.
 */
public final class AclTransformation {
  private static final int MAX_ENTRIES = 32;

  public static List<AclEntry> filterAclEntriesByAclSpec(List<AclEntry> existingAcl, List<AclEntry> inAclSpec) throws AclException {
    ValidatedAclSpec aclSpec = new ValidatedAclSpec(inAclSpec);
    ArrayList<AclEntry> aclBuilder = Lists.newArrayListWithCapacity(MAX_ENTRIES);
    EnumMap<AclEntryScope, AclEntry> providedMask = Maps.newEnumMap(AclEntryScope.class);
    EnumSet<AclEntryScope> maskDirty = EnumSet.noneOf(AclEntryScope.class);
    EnumSet<AclEntryScope> scopeDirty = EnumSet.noneOf(AclEntryScope.class);
    for (AclEntry existingEntry : existingAcl) {
      if (aclSpec.containsKey(existingEntry)) {
        scopeDirty.add(existingEntry.getScope());
        if (existingEntry.getType() == MASK) {
          maskDirty.add(existingEntry.getScope());
        }
      } else {
        if (existingEntry.getType() == MASK) {
          providedMask.put(existingEntry.getScope(), existingEntry);
        } else {
          aclBuilder.add(existingEntry);
        }
      }
    }
    copyDefaultsIfNeeded(aclBuilder);
    calculateMasks(aclBuilder, providedMask, maskDirty, scopeDirty);
    return buildAndValidateAcl(aclBuilder);
  }

  public static List<AclEntry> mergeAclEntries(List<AclEntry> existingAcl, List<AclEntry> inAclSpec) throws AclException {
    ValidatedAclSpec aclSpec = new ValidatedAclSpec(inAclSpec);
    ArrayList<AclEntry> aclBuilder = Lists.newArrayListWithCapacity(MAX_ENTRIES);
    List<AclEntry> foundAclSpecEntries = Lists.newArrayListWithCapacity(MAX_ENTRIES);
    EnumMap<AclEntryScope, AclEntry> providedMask = Maps.newEnumMap(AclEntryScope.class);
    EnumSet<AclEntryScope> maskDirty = EnumSet.noneOf(AclEntryScope.class);
    EnumSet<AclEntryScope> scopeDirty = EnumSet.noneOf(AclEntryScope.class);
    for (AclEntry existingEntry : existingAcl) {
      AclEntry aclSpecEntry = aclSpec.findByKey(existingEntry);
      if (aclSpecEntry != null) {
        foundAclSpecEntries.add(aclSpecEntry);
        scopeDirty.add(aclSpecEntry.getScope());
        if (aclSpecEntry.getType() == MASK) {
          providedMask.put(aclSpecEntry.getScope(), aclSpecEntry);
          maskDirty.add(aclSpecEntry.getScope());
        } else {
          aclBuilder.add(aclSpecEntry);
        }
      } else {
        if (existingEntry.getType() == MASK) {
          providedMask.put(existingEntry.getScope(), existingEntry);
        } else {
          aclBuilder.add(existingEntry);
        }
      }
    }
    // ACL spec entries that were not replacements are new additions.
    for (AclEntry newEntry : aclSpec) {
      if (Collections.binarySearch(foundAclSpecEntries, newEntry, ACL_ENTRY_COMPARATOR) < 0) {
        scopeDirty.add(newEntry.getScope());
        if (newEntry.getType() == MASK) {
          providedMask.put(newEntry.getScope(), newEntry);
          maskDirty.add(newEntry.getScope());
        } else {
          aclBuilder.add(newEntry);
        }
      }
    }
    copyDefaultsIfNeeded(aclBuilder);
    calculateMasks(aclBuilder, providedMask, maskDirty, scopeDirty);
    return buildAndValidateAcl(aclBuilder);
  }

  public static List<AclEntry> replaceAclEntries(List<AclEntry> existingAcl, List<AclEntry> inAclSpec) throws AclException {
    ValidatedAclSpec aclSpec = new ValidatedAclSpec(inAclSpec);
    ArrayList<AclEntry> aclBuilder = Lists.newArrayListWithCapacity(MAX_ENTRIES);
    // Replacement is done separately for each scope: access and default.
    EnumMap<AclEntryScope, AclEntry> providedMask = Maps.newEnumMap(AclEntryScope.class);
    EnumSet<AclEntryScope> maskDirty = EnumSet.noneOf(AclEntryScope.class);
    EnumSet<AclEntryScope> scopeDirty = EnumSet.noneOf(AclEntryScope.class);
    for (AclEntry aclSpecEntry : aclSpec) {
      scopeDirty.add(aclSpecEntry.getScope());
      if (aclSpecEntry.getType() == MASK) {
        providedMask.put(aclSpecEntry.getScope(), aclSpecEntry);
        maskDirty.add(aclSpecEntry.getScope());
      } else {
        aclBuilder.add(aclSpecEntry);
      }
    }
    // Copy existing entries if the scope was not replaced.
    for (AclEntry existingEntry : existingAcl) {
      if (!scopeDirty.contains(existingEntry.getScope())) {
        if (existingEntry.getType() == MASK) {
          providedMask.put(existingEntry.getScope(), existingEntry);
        } else {
          aclBuilder.add(existingEntry);
        }
      }
    }
    copyDefaultsIfNeeded(aclBuilder);
    calculateMasks(aclBuilder, providedMask, maskDirty, scopeDirty);
    return buildAndValidateAcl(aclBuilder);
  }

  private AclTransformation() {
  }

  public static final Comparator<AclEntry> ACL_ENTRY_COMPARATOR = new Comparator<AclEntry>() {
    @Override
    public int compare(AclEntry entry1, AclEntry entry2) {
      return ComparisonChain.start().compare(entry1.getScope(), entry2.getScope(), Ordering.explicit(ACCESS, DEFAULT)).compare(entry1.getType(), entry2.getType(), Ordering.explicit(USER, GROUP, MASK, OTHER)).compare(entry1.getName(), entry2.getName(), Ordering.natural().nullsFirst()).result();
    }
  };

  public static List<AclEntry> buildAndValidateAcl(ArrayList<AclEntry> aclBuilder) throws AclException {
    aclBuilder.trimToSize();
    Collections.sort(aclBuilder, ACL_ENTRY_COMPARATOR);
    // Full iteration to check for duplicates and invalid named entries.
    AclEntry prevEntry = null;
    for (AclEntry entry : aclBuilder) {
      if (prevEntry != null && ACL_ENTRY_COMPARATOR.compare(prevEntry, entry) == 0) {
        throw new AclException("Invalid ACL: multiple entries with same scope, type and name.");
      }
      if (entry.getName() != null && (entry.getType() == MASK || entry.getType() == OTHER)) {
        throw new AclException("Invalid ACL: this entry type must not have a name: " + entry + ".");
      }
      prevEntry = entry;
    }

    ScopedAclEntries scopedEntries = new ScopedAclEntries(aclBuilder);
    checkMaxEntries(scopedEntries);

    // Search for the required base access entries.  If there is a default ACL,
    // then do the same check on the default entries.
    for (AclEntryType type : EnumSet.of(USER, GROUP, OTHER)) {
      AclEntry accessEntryKey = new AclEntry.Builder().setScope(ACCESS).setType(type).build();
      if (Collections.binarySearch(scopedEntries.getAccessEntries(), accessEntryKey, ACL_ENTRY_COMPARATOR) < 0) {
        throw new AclException("Invalid ACL: the user, group and other entries are required.");
      }
      if (!scopedEntries.getDefaultEntries().isEmpty()) {
        AclEntry defaultEntryKey = new AclEntry.Builder().setScope(DEFAULT).setType(type).build();
        if (Collections.binarySearch(scopedEntries.getDefaultEntries(), defaultEntryKey, ACL_ENTRY_COMPARATOR) < 0) {
          throw new AclException("Invalid default ACL: the user, group and other entries are required.");
        }
      }
    }
    return Collections.unmodifiableList(aclBuilder);
  }

  private static void checkMaxEntries(ScopedAclEntries scopedEntries) throws AclException {
    List<AclEntry> accessEntries = scopedEntries.getAccessEntries();
    List<AclEntry> defaultEntries = scopedEntries.getDefaultEntries();
    if (accessEntries.size() > MAX_ENTRIES) {
      throw new AclException("Invalid ACL: ACL has " + accessEntries.size() + " access entries, which exceeds maximum of " + MAX_ENTRIES + ".");
    }
    if (defaultEntries.size() > MAX_ENTRIES) {
      throw new AclException("Invalid ACL: ACL has " + defaultEntries.size() + " default entries, which exceeds maximum of " + MAX_ENTRIES + ".");
    }
  }

  private static void calculateMasks(List<AclEntry> aclBuilder, EnumMap<AclEntryScope, AclEntry> providedMask, EnumSet<AclEntryScope> maskDirty, EnumSet<AclEntryScope> scopeDirty) throws AclException {
    EnumSet<AclEntryScope> scopeFound = EnumSet.noneOf(AclEntryScope.class);
    EnumMap<AclEntryScope, FsAction> unionPerms = Maps.newEnumMap(AclEntryScope.class);
    EnumSet<AclEntryScope> maskNeeded = EnumSet.noneOf(AclEntryScope.class);
    // Determine which scopes are present, which scopes need a mask, and the
    // union of group class permissions in each scope.
    for (AclEntry entry : aclBuilder) {
      scopeFound.add(entry.getScope());
      if (entry.getType() == GROUP || entry.getName() != null) {
        FsAction scopeUnionPerms = unionPerms.get(entry.getScope());
        if (scopeUnionPerms == null) {
          scopeUnionPerms = FsAction.NONE;
        }
        unionPerms.put(entry.getScope(), scopeUnionPerms.or(entry.getPermission()));
      }
      if (entry.getName() != null) {
        maskNeeded.add(entry.getScope());
      }
    }
    // Add mask entry if needed in each scope.
    for (AclEntryScope scope : scopeFound) {
      if (!providedMask.containsKey(scope) && maskNeeded.contains(scope) && maskDirty.contains(scope)) {
        // Caller explicitly removed mask entry, but it's required.
        throw new AclException("Invalid ACL: mask is required and cannot be deleted.");
      } else if (providedMask.containsKey(scope) && (!scopeDirty.contains(scope) || maskDirty.contains(scope))) {
        // Caller explicitly provided new mask, or we are preserving the existing
        // mask in an unchanged scope.
        aclBuilder.add(providedMask.get(scope));
      } else if (maskNeeded.contains(scope) || providedMask.containsKey(scope)) {
        // Otherwise, if there are maskable entries present, or the ACL
        // previously had a mask, then recalculate a mask automatically.
        aclBuilder.add(new AclEntry.Builder().setScope(scope).setType(MASK).setPermission(unionPerms.get(scope)).build());
      }
    }
  }

  private static void copyDefaultsIfNeeded(List<AclEntry> aclBuilder) {
    Collections.sort(aclBuilder, ACL_ENTRY_COMPARATOR);
    ScopedAclEntries scopedEntries = new ScopedAclEntries(aclBuilder);
    if (!scopedEntries.getDefaultEntries().isEmpty()) {
      List<AclEntry> accessEntries = scopedEntries.getAccessEntries();
      List<AclEntry> defaultEntries = scopedEntries.getDefaultEntries();
      List<AclEntry> copiedEntries = Lists.newArrayListWithCapacity(3);
      for (AclEntryType type : EnumSet.of(USER, GROUP, OTHER)) {
        AclEntry defaultEntryKey = new AclEntry.Builder().setScope(DEFAULT).setType(type).build();
        int defaultEntryIndex = Collections.binarySearch(defaultEntries, defaultEntryKey, ACL_ENTRY_COMPARATOR);
        if (defaultEntryIndex < 0) {
          AclEntry accessEntryKey = new AclEntry.Builder().setScope(ACCESS).setType(type).build();
          int accessEntryIndex = Collections.binarySearch(accessEntries, accessEntryKey, ACL_ENTRY_COMPARATOR);
          if (accessEntryIndex >= 0) {
            copiedEntries.add(new AclEntry.Builder().setScope(DEFAULT).setType(type).setPermission(accessEntries.get(accessEntryIndex).getPermission()).build());
          }
        }
      }
      // Add all copied entries when done to prevent potential issues with binary
      // search on a modified aclBulider during the main loop.
      aclBuilder.addAll(copiedEntries);
    }
  }

  private static final class ValidatedAclSpec implements Iterable<AclEntry> {
    private final List<AclEntry> aclSpec;

    /**
     * Creates a ValidatedAclSpec by pre-validating and sorting the given ACL
     * entries.  Pre-validation checks that it does not exceed the maximum
     * entries.  This check is performed before modifying the ACL, and it's
     * actually insufficient for enforcing the maximum number of entries.
     * Transformation logic can create additional entries automatically,such as
     * the mask and some of the default entries, so we also need additional
     * checks during transformation.  The up-front check is still valuable here
     * so that we don't run a lot of expensive transformation logic while
     * holding the namesystem lock for an attacker who intentionally sent a huge
     * ACL spec.
     *
     * @param aclSpec List<AclEntry> containing unvalidated input ACL spec
     * @throws AclException if validation fails
     */
    public ValidatedAclSpec(List<AclEntry> aclSpec) throws AclException {
      Collections.sort(aclSpec, ACL_ENTRY_COMPARATOR);
      checkMaxEntries(new ScopedAclEntries(aclSpec));
      this.aclSpec = aclSpec;
    }

    /**
     * Returns true if this contains an entry matching the given key.  An ACL
     * entry's key consists of scope, type and name (but not permission).
     *
     * @param key AclEntry search key
     * @return boolean true if found
     */
    public boolean containsKey(AclEntry key) {
      return Collections.binarySearch(aclSpec, key, ACL_ENTRY_COMPARATOR) >= 0;
    }

    /**
     * Returns the entry matching the given key or null if not found.  An ACL
     * entry's key consists of scope, type and name (but not permission).
     *
     * @param key AclEntry search key
     * @return AclEntry entry matching the given key or null if not found
     */
    public AclEntry findByKey(AclEntry key) {
      int index = Collections.binarySearch(aclSpec, key, ACL_ENTRY_COMPARATOR);
      if (index >= 0) {
        return aclSpec.get(index);
      }
      return null;
    }

    @Override
    public Iterator<AclEntry> iterator() {
      return aclSpec.iterator();
    }
  }

  public static class AclException extends IOException {
    private static final long serialVersionUID = 1L;

    /**
     * Creates a new AclException.
     *
     * @param message String message
     */
    public AclException(String message) {
      super(message);
    }

    /**
     * Creates a new AclException.
     *
     * @param message String message
     * @param cause   The cause of the exception
     */
    public AclException(String message, Throwable cause) {
      super(message, cause);
    }
  }
}
