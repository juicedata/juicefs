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

package io.juicefs;

import junit.framework.TestCase;

import java.io.File;
import java.nio.file.FileSystems;
import java.nio.file.Files;
import java.nio.file.attribute.PosixFilePermission;
import java.nio.file.attribute.PosixFilePermissions;
import java.util.Set;

public class NativeLibraryDirectoryTest extends TestCase {
  public void testDirectoryIsPrivateAndUnique() throws Exception {
    File first = JuiceFileSystemImpl.NativeLibraryDirectory.create();
    File second = JuiceFileSystemImpl.NativeLibraryDirectory.create();
    try {
      assertFalse(first.equals(second));
      assertTrue(first.isDirectory());
      assertFalse(Files.isSymbolicLink(first.toPath()));
      if (FileSystems.getDefault().supportedFileAttributeViews().contains("posix")) {
        Set<PosixFilePermission> permissions = Files.getPosixFilePermissions(first.toPath());
        assertEquals(PosixFilePermissions.fromString("rwx------"), permissions);
      }
    } finally {
      Files.deleteIfExists(first.toPath());
      Files.deleteIfExists(second.toPath());
    }
  }
}
