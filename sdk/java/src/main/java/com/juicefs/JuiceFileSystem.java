/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */
package com.juicefs;

import com.juicefs.utils.PatchUtil;
import javassist.ClassPool;
import javassist.CtClass;
import javassist.CtMethod;
import javassist.NotFoundException;
import org.apache.hadoop.classification.InterfaceAudience;
import org.apache.hadoop.classification.InterfaceStability;
import org.apache.hadoop.conf.Configuration;
import org.apache.hadoop.fs.*;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.xeustechnologies.jcl.JarClassLoader;
import org.xeustechnologies.jcl.JclObjectFactory;
import org.xeustechnologies.jcl.JclUtils;

import java.io.IOException;
import java.lang.instrument.ClassDefinition;
import java.net.URI;

/****************************************************************
 * Implement the FileSystem API for JuiceFS
 *****************************************************************/
@InterfaceAudience.Public
@InterfaceStability.Stable
public class JuiceFileSystem extends FilterFileSystem {
    private static final Logger LOG = LoggerFactory.getLogger(JuiceFileSystem.class);
    private static JarClassLoader jcl;

    private static boolean fileChecksumEnabled = false;
    private static boolean distcpPatched = false;

    // to bypass impala insert check
    static {
        jcl = new JarClassLoader();
        String path = JuiceFileSystem.class.getProtectionDomain().getCodeSource().getLocation().getPath();
        jcl.add(path); // Load jar file

        boolean runInImpala = false;
        String className = "org.apache.impala.common.FileSystemUtil";

        try {
            Class.forName(className);
            runInImpala = true;
        } catch (ClassNotFoundException ignored) {
        }

        if (runInImpala) {

            try {
                ClassPool classPool = ClassPool.getDefault();
                CtClass fsUtil = classPool.get(className);
                CtClass fsClass = classPool.get("org.apache.hadoop.fs.FileSystem");
                CtMethod method = fsUtil.getDeclaredMethod("isLocalFileSystem", new CtClass[]{fsClass});
                method.insertBefore("if (fs instanceof com.juicefs.JuiceFileSystem) return true;");
                byte[] bytecode = fsUtil.toBytecode();

                ClassDefinition definition = new ClassDefinition(Class.forName(className), bytecode);
                RedefineClassAgent.redefineClasses(definition);
                fsUtil.detach();
                fsClass.detach();
            } catch (NotFoundException | ClassNotFoundException e) {
                throw new RuntimeException("Impala version was incompatible. so only read was supported!", e);
            } catch (NoClassDefFoundError e) {
                if (e.getMessage().contains("VirtualMachine"))
                    throw new NoClassDefFoundError(
                            "You should add tools.jar to impala classpath, you can find it in $JAVA_HOME/lib");
                throw e;
            } catch (Exception e) {
                throw new RuntimeException("Unknown exception, so only read was supported!", e);
            }
        }

        PatchUtil.patchBefore("org.apache.flink.runtime.fs.hdfs.HadoopRecoverableFsDataOutputStream",
                "waitUntilLeaseIsRevoked",
                new String[]{"org.apache.hadoop.fs.FileSystem", "org.apache.hadoop.fs.Path"},
                "if (fs instanceof com.juicefs.JuiceFileSystem) {\n" +
                        "            return ((com.juicefs.JuiceFileSystem)fs).isFileClosed(path);\n" +
                        "        }");
    }

    private synchronized static void patchDistCpChecksum() {
        if (distcpPatched)
            return;
        ClassPool classPool = ClassPool.getDefault();
        try {
            String clsName = "org.apache.hadoop.tools.mapred.RetriableFileCopyCommand";
            CtClass copyClass = classPool.get(clsName);
            CtMethod method = copyClass.getDeclaredMethod("compareCheckSums");
            method.insertBefore(
                    "if (sourceFS.getFileStatus(source).getBlockSize() != targetFS.getFileStatus(target).getBlockSize()) {return ;}");
            byte[] bytecode = copyClass.toBytecode();
            ClassDefinition definition = new ClassDefinition(Class.forName(clsName), bytecode);
            RedefineClassAgent.redefineClasses(definition);
            copyClass.detach();
        } catch (NotFoundException | ClassNotFoundException e) {
        } catch (NoClassDefFoundError e) {
            StackTraceElement[] stackTrace = Thread.currentThread().getStackTrace();
            if (stackTrace.length > 1) {
                StackTraceElement element = stackTrace[stackTrace.length - 1];
                if (element.getClassName().contains("DistCp")) {
                    LOG.warn(
                            "Please add tools.jar to classpath to skip checksum check for files with different block sizes.");
                }
            }
        } catch (Throwable e) {
            LOG.warn("patch distcp failed!", e);
        }
        distcpPatched = true;
    }

    private static FileSystem createInstance() {
        // Create default factory
        JclObjectFactory factory = JclObjectFactory.getInstance();
        Object obj = factory.create(jcl, "com.juicefs.JuiceFileSystemImpl");
        return (FileSystem) JclUtils.deepClone(obj);
    }

    @Override
    public void initialize(URI uri, Configuration conf) throws IOException {
        super.initialize(uri, conf);
        fileChecksumEnabled = Boolean.parseBoolean(getConf(conf, "file.checksum", "false"));
    }

    private String getConf(Configuration conf, String key, String value) {
        String name = fs.getUri().getHost();
        String v = conf.get("juicefs." + key, value);
        if (name != null && !name.equals("")) {
            v = conf.get("juicefs." + name + "." + key, v);
        }
        if (v != null)
            v = v.trim();
        return v;
    }

    public JuiceFileSystem() {
        super(createInstance());
    }

    @Override
    public String getScheme() {
        StackTraceElement[] elements = Thread.currentThread().getStackTrace();
        if (elements[2].getClassName().equals("org.apache.flink.runtime.fs.hdfs.HadoopRecoverableWriter") &&
                elements[2].getMethodName().equals("<init>")) {
            return "hdfs";
        }
        return fs.getScheme();
    }

    @Override
    public ContentSummary getContentSummary(Path f) throws IOException {
        return fs.getContentSummary(f);
    }

    public boolean isFileClosed(final Path src) throws IOException {
        FileStatus st = fs.getFileStatus(src);
        return st.getLen() > 0;
    }

    @Override
    public FileChecksum getFileChecksum(Path f, long length) throws IOException {
        if (!fileChecksumEnabled)
            return null;
        patchDistCpChecksum();
        return super.getFileChecksum(f, length);
    }

    @Override
    public FileChecksum getFileChecksum(Path f) throws IOException {
        if (!fileChecksumEnabled)
            return null;
        patchDistCpChecksum();
        return super.getFileChecksum(f);
    }

    @Override
    public void close() throws IOException {
        super.close();
    }
}
