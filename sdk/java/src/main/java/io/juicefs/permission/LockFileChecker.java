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

import java.io.File;
import java.io.IOException;

public class LockFileChecker {

  public static boolean checkAndCreateLockFile(String directoryPath) {
    File directory = new File(directoryPath);

    if (!directory.exists()) {
      directory.mkdirs();
    }

    File lockFile = new File(directory, ".lock");

    if (lockFile.exists()) {
      return false;
    } else {
      try {
        lockFile.createNewFile();
        return true;
      } catch (IOException e) {
        throw new RuntimeException("ranger policies cache dir cannot created. ", e);
      }
    }
  }

  public static void cleanUp(String directoryPath) {
    File directory = new File(directoryPath + ".lock");
    directory.deleteOnExit();
  }

}
