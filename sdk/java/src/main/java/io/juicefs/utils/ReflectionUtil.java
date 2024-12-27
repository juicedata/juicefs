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
package io.juicefs.utils;

import java.lang.reflect.Constructor;
import java.lang.reflect.Field;

public class ReflectionUtil {
  public static boolean hasMethod(String className, String method, String[] params) {
    try {
      Class<?>[] classes = null;
      if (params != null) {
        classes = new Class[params.length];
        for (int i = 0; i < params.length; i++) {
          classes[i] = Class.forName(params[i], false, Thread.currentThread().getContextClassLoader());
        }
      }
      return hasMethod(className, method, classes);
    } catch (ClassNotFoundException e) {
      return false;
    }
  }

  public static boolean hasMethod(String className, String method, Class<?>[] params) {
    try {
      Class<?> clazz = Class.forName(className, false, Thread.currentThread().getContextClassLoader());
      clazz.getDeclaredMethod(method, params);
    } catch (ClassNotFoundException | NoSuchMethodException e) {
      return false;
    }
    return true;
  }

  public static <T> Constructor<T> getConstructor(Class<T> clazz, Class<?>... params) {
    try {
      return clazz.getConstructor(params);
    } catch (NoSuchMethodException e) {
      return null;
    }
  }

  public static Object getField(String className, String field, Object obj) throws ClassNotFoundException, NoSuchFieldException, IllegalAccessException {
    Class<?> clazz = Class.forName(className);
    Field f = clazz.getDeclaredField(field);
    f.setAccessible(true);
    return f.get(obj);
  }
}
