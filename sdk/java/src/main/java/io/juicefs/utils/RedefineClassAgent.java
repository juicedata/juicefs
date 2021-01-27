/*
Copyright (c) 2017 Turn Inc
All rights reserved.
The contents of this file are subject to the MIT License as provided
below. Alternatively, the contents of this file may be used under
the terms of Mozilla Public License Version 1.1,
the terms of the GNU Lesser General Public License Version 2.1 or later,
or the terms of the Apache License Version 2.0.
License:
Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
the Software, and to permit persons to whom the Software is furnished to do so,
subject to the following conditions:
The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.
THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
*/

package io.juicefs.utils;


import com.sun.tools.attach.VirtualMachine;
import javassist.CannotCompileException;
import javassist.ClassPool;
import javassist.CtClass;
import javassist.NotFoundException;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.lang.instrument.ClassDefinition;
import java.lang.instrument.Instrumentation;
import java.lang.instrument.UnmodifiableClassException;
import java.lang.management.ManagementFactory;
import java.util.jar.Attributes;
import java.util.jar.JarEntry;
import java.util.jar.JarOutputStream;
import java.util.jar.Manifest;
import java.util.logging.Level;
import java.util.logging.Logger;

/**
 * Packages everything necessary to be able to redefine a class using {@link Instrumentation} as provided by
 * Java 1.6 or later. Class redefinition is the act of replacing a class' bytecode at runtime, after that class
 * has already been loaded.
 * <p>
 * The scheme employed by this class uses an agent (defined by this class) that, when loaded into the JVM, provides
 * an instance of {@link Instrumentation} which in turn provides a method to redefine classes.
 * <p>
 * Users of this class only need to call {@link #redefineClasses(ClassDefinition...)}. The agent stuff will be done
 * automatically (and lazily).
 * <p>
 * Note that classes cannot be arbitrarily redefined. The new version must retain the same schema; methods and fields
 * cannot be added or removed. In practice this means that method bodies can be changed.
 * <p>
 * Note that this is a replacement for javassist's {@code HotSwapper}. {@code HotSwapper} depends on the debug agent
 * to perform the hotswap. That agent is available since Java 1.3, but the JVM must be started with the agent enabled,
 * and the agent often fails to perform the swap if the machine is under heavy load. This class is both cleaner and more
 * reliable.
 *
 * @author Adam Lugowski
 * @see Instrumentation#redefineClasses(ClassDefinition...)
 */
public class RedefineClassAgent {
  /**
   * Use the Java logger to avoid any references to anything not supplied by the JVM. This avoids issues with
   * classpath when compiling/loading this class as an agent.
   */
  private static final Logger LOGGER = Logger.getLogger(RedefineClassAgent.class.getSimpleName());

  /**
   * Populated when this class is loaded into the JVM as an agent (via {@link #ensureAgentLoaded()}.
   */
  private static volatile Instrumentation instrumentation = null;

  /**
   * How long to wait for the agent to load before giving up and assuming the load failed.
   */
  private static final int AGENT_LOAD_WAIT_TIME_SEC = 3;

  /**
   * Agent entry point. Do not call this directly.
   * <p>
   * This method is called by the JVM when this class is loaded as an agent.
   * <p>
   * Sets {@link #instrumentation} to {@code inst}, provided {@code inst} supports class redefinition.
   *
   * @param agentArgs ignored.
   * @param inst      This is the reason this class exists. {@link Instrumentation} has the
   *                  {@link Instrumentation#redefineClasses(ClassDefinition...)} method.
   */
  public static void agentmain(String agentArgs, Instrumentation inst) {
    if (!inst.isRedefineClassesSupported()) {
      LOGGER.severe("Class redefinition not supported. Aborting.");
      return;
    }

    instrumentation = inst;
  }

  /**
   * Attempts to redefine class bytecode.
   * <p>
   * On first call this method will attempt to load an agent into the JVM to obtain an instance of
   * {@link Instrumentation}. This agent load can introduce a pause (in practice 1 to 2 seconds).
   *
   * @param definitions classes to redefine.
   * @throws UnmodifiableClassException as thrown by {@link Instrumentation#redefineClasses(ClassDefinition...)}
   * @throws ClassNotFoundException     as thrown by {@link Instrumentation#redefineClasses(ClassDefinition...)}
   * @throws FailedToLoadAgentException if agent either failed to load or if the agent wasn't able to get an
   *                                    instance of {@link Instrumentation} that allows class redefinitions.
   * @see Instrumentation#redefineClasses(ClassDefinition...)
   */
  public static void redefineClasses(ClassDefinition... definitions)
          throws UnmodifiableClassException, ClassNotFoundException, FailedToLoadAgentException {
    ensureAgentLoaded();
    instrumentation.redefineClasses(definitions);
  }

  /**
   * Lazy loads the agent that populates {@link #instrumentation}. OK to call multiple times.
   *
   * @throws FailedToLoadAgentException if agent either failed to load or if the agent wasn't able to get an
   *                                    instance of {@link Instrumentation} that allows class redefinitions.
   */
  private static void ensureAgentLoaded() throws FailedToLoadAgentException {
    if (instrumentation != null) {
      // already loaded
      return;
    }

    // load the agent
    try {
      File agentJar = createAgentJarFile();

      // Loading an agent requires the PID of the JVM to load the agent to. Find out our PID.
      String nameOfRunningVM = ManagementFactory.getRuntimeMXBean().getName();
      String pid = nameOfRunningVM.substring(0, nameOfRunningVM.indexOf('@'));

      // load the agent
      VirtualMachine vm = VirtualMachine.attach(pid);
      vm.loadAgent(agentJar.getAbsolutePath(), "");
      vm.detach();
    } catch (Exception e) {
      throw new FailedToLoadAgentException(e);
    }

    // wait for the agent to load
    for (int sec = 0; sec < AGENT_LOAD_WAIT_TIME_SEC; sec++) {
      if (instrumentation != null) {
        // success!
        return;
      }

      try {
        LOGGER.info("Sleeping for 1 second while waiting for agent to load.");
        Thread.sleep(1000);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw new FailedToLoadAgentException();
      }
    }

    // agent didn't load
    throw new FailedToLoadAgentException();
  }

  /**
   * An agent must be specified as a .jar where the manifest has an Agent-Class attribute. Additionally, in order
   * to be able to redefine classes, the Can-Redefine-Classes attribute must be true.
   * <p>
   * This method creates such an agent Jar as a temporary file. The Agent-Class is this class. If the returned Jar
   * is loaded as an agent then {@link #agentmain(String, Instrumentation)} will be called by the JVM.
   *
   * @return a temporary {@link File} that points at Jar that packages this class.
   * @throws IOException if agent Jar creation failed.
   */
  private static File createAgentJarFile() throws IOException {
    File jarFile = File.createTempFile("agent", ".jar");
    jarFile.deleteOnExit();

    // construct a manifest that allows class redefinition
    Manifest manifest = new Manifest();
    Attributes mainAttributes = manifest.getMainAttributes();
    mainAttributes.put(Attributes.Name.MANIFEST_VERSION, "1.0");
    mainAttributes.put(new Attributes.Name("Agent-Class"), RedefineClassAgent.class.getName());
    mainAttributes.put(new Attributes.Name("Can-Retransform-Classes"), "true");
    mainAttributes.put(new Attributes.Name("Can-Redefine-Classes"), "true");

    try (JarOutputStream jos = new JarOutputStream(new FileOutputStream(jarFile), manifest)) {
      // add the agent .class into the .jar
      JarEntry agent = new JarEntry(RedefineClassAgent.class.getName().replace('.', '/') + ".class");
      jos.putNextEntry(agent);

      // dump the class bytecode into the entry
      ClassPool pool = ClassPool.getDefault();
      CtClass ctClass = pool.get(RedefineClassAgent.class.getName());
      jos.write(ctClass.toBytecode());
      jos.closeEntry();
    } catch (CannotCompileException | NotFoundException e) {
      // Realistically this should never happen.
      LOGGER.log(Level.SEVERE, "Exception while creating RedefineClassAgent jar.", e);
      throw new IOException(e);
    }

    return jarFile;
  }

  /**
   * Marks a failure to load the agent and get an instance of {@link Instrumentation} that is able to redefine
   * classes.
   */
  public static class FailedToLoadAgentException extends Exception {
    public FailedToLoadAgentException() {
      super();
    }

    public FailedToLoadAgentException(Throwable cause) {
      super(cause);
    }
  }
}
